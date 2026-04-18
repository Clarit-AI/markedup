package index

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Clarit-AI/markedup/embed"
	"github.com/Clarit-AI/markedup/rerank"
	"github.com/Clarit-AI/markedup/schema"
	"github.com/Clarit-AI/markedup/temporal"
)

// VectorCacheLookup is the subset of cache.VectorCache needed by the search
// pipeline. Defining it here avoids an import cycle between index and cache.
type VectorCacheLookup interface {
	LoadVectors(fileID string, contentHash string) ([]float32, error)
}

// MatchType classifies how a query matched a field value.
type MatchType int

const (
	// MatchExact means the query equals the field value (case-insensitive).
	MatchExact MatchType = iota // score multiplier 1.0
	// MatchPrefix means the field value starts with the query.
	MatchPrefix // 0.8
	// MatchContains means the field value contains the query.
	MatchContains // 0.5
	// MatchFuzzy means levenshtein distance ≤ 2 between query and field value.
	MatchFuzzy // 0.3
)

// matchTypeScore maps a MatchType to its score multiplier.
func matchTypeScore(mt MatchType) float64 {
	switch mt {
	case MatchExact:
		return 1.0
	case MatchPrefix:
		return 0.8
	case MatchContains:
		return 0.5
	case MatchFuzzy:
		return 0.3
	default:
		return 0
	}
}

// Match describes a single field-level match between the query and a page.
type Match struct {
	Field string
	Value string
	Type  MatchType
}

// Result holds a scored search result with the matched page and match details.
type Result struct {
	Page    *schema.Page
	Score   float64
	Matches []Match
}

// FormatForLLM produces a structured text block suitable for LLM consumption.
func (r Result) FormatForLLM() string {
	fm := r.Page.Frontmatter
	var b strings.Builder

	// Title line with entity type.
	if fm.EntityType != "" {
		fmt.Fprintf(&b, "# %s (%s)\n", fm.Title, fm.EntityType)
	} else {
		fmt.Fprintf(&b, "# %s\n", fm.Title)
	}

	fmt.Fprintf(&b, "Confidence: %.2f\n", fm.Confidence)

	// Relationships.
	if len(fm.Relationships) > 0 {
		parts := make([]string, len(fm.Relationships))
		for i, rel := range fm.Relationships {
			parts[i] = fmt.Sprintf("%s (%s)", rel.Target, rel.Type)
		}
		fmt.Fprintf(&b, "Relationships: %s\n", strings.Join(parts, ", "))
	}

	// Semantic hints.
	if len(fm.SemanticHints) > 0 {
		fmt.Fprintf(&b, "Semantic hints: %s\n", strings.Join(fm.SemanticHints, ", "))
	}

	// Body excerpt.
	body := strings.TrimSpace(r.Page.Body)
	if body != "" {
		b.WriteString("\n")
		if len(body) > 500 {
			b.WriteString(body[:500])
		} else {
			b.WriteString(body)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// SearchOption configures the search pipeline.
type SearchOption func(*searchConfig)

type searchConfig struct {
	limit           int
	minScore        float64
	embedder        embed.Embedder
	vectorCache     VectorCacheLookup
	reranker        rerank.Reranker
	embeddingWeight float64
	ctx             context.Context
}

const defaultEmbeddingWeight = 0.3

// WithLimit sets the maximum number of results returned.
func WithLimit(n int) SearchOption {
	return func(cfg *searchConfig) {
		cfg.limit = n
	}
}

// WithMinScore sets the minimum score threshold; results below are discarded.
func WithMinScore(f float64) SearchOption {
	return func(cfg *searchConfig) {
		cfg.minScore = f
	}
}

// WithEmbedder configures an embedder for semantic similarity scoring.
// Requires a VectorCache to be set via WithVectorCache.
func WithEmbedder(e embed.Embedder) SearchOption {
	return func(cfg *searchConfig) {
		cfg.embedder = e
	}
}

// WithVectorCache sets the vector cache used to look up pre-computed
// embeddings for pages.
func WithVectorCache(vc VectorCacheLookup) SearchOption {
	return func(cfg *searchConfig) {
		cfg.vectorCache = vc
	}
}

// WithReranker sets a reranker to post-process results. After initial
// scoring, the top candidates are sent to the reranker for re-ordering.
func WithReranker(r rerank.Reranker) SearchOption {
	return func(cfg *searchConfig) {
		cfg.reranker = r
	}
}

// WithEmbeddingWeight controls the balance between keyword and embedding
// scores. The final score is (1-w)*keyword + w*embedding. Default is 0.3.
// Values are clamped to [0.0, 1.0].
func WithEmbeddingWeight(w float64) SearchOption {
	return func(cfg *searchConfig) {
		if w < 0 {
			w = 0
		}
		if w > 1 {
			w = 1
		}
		cfg.embeddingWeight = w
	}
}

// WithContext sets the context for operations that may need it (embedding,
// reranking). If not set, context.Background() is used.
func WithContext(ctx context.Context) SearchOption {
	return func(cfg *searchConfig) {
		cfg.ctx = ctx
	}
}

// Field weights used in scoring.
const (
	weightEntityName  = 10
	weightAlias       = 8
	weightTitle       = 7
	weightTag         = 5
	weightSemanticHint = 3
	weightRelTarget   = 2
	weightBody        = 1
)

// Search scores every page in idx against query using multi-signal field
// weighting, match-type multipliers, and temporal decay. When an embedder
// and vector cache are configured, cosine similarity is blended with keyword
// scores. When a reranker is configured, top results are re-scored.
// Results are returned sorted by descending score.
func Search(idx *KnowledgeIndex, query string, opts ...SearchOption) []Result {
	if query == "" {
		return nil
	}

	cfg := &searchConfig{
		embeddingWeight: defaultEmbeddingWeight,
	}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.ctx == nil {
		cfg.ctx = context.Background()
	}

	q := strings.ToLower(strings.TrimSpace(query))
	now := time.Now()

	// Compute query embedding if embedder is configured.
	var queryVec []float32
	useEmbeddings := cfg.embedder != nil && cfg.vectorCache != nil
	if useEmbeddings {
		vecs, err := cfg.embedder.Embed(cfg.ctx, []string{query})
		if err != nil {
			log.Printf("search: failed to embed query, falling back to keyword-only: %v", err)
			useEmbeddings = false
		} else if len(vecs) > 0 {
			queryVec = vecs[0]
		}
	}

	var results []Result
	for _, page := range idx.All() {
		matches, keywordScore := scorePage(page, q)
		if keywordScore == 0 && !useEmbeddings {
			continue
		}

		tm := temporalMultiplier(page, now)
		keywordFinal := keywordScore * tm

		finalScore := keywordFinal

		// Blend embedding score if available.
		if useEmbeddings && queryVec != nil {
			pageVec, err := cfg.vectorCache.LoadVectors(
				page.Frontmatter.ID,
				contentHashForPage(page),
			)
			if err != nil {
				// No cached vectors for this page — use keyword-only.
				if keywordScore == 0 {
					continue
				}
			} else {
				sim := embed.CosineSimilarity(queryVec, pageVec)
				// Normalize cosine similarity from [-1,1] to [0,1].
				embScore := (sim + 1.0) / 2.0
				w := cfg.embeddingWeight
				finalScore = (1-w)*keywordFinal + w*embScore

				// Include pages that have high embedding similarity even
				// if keyword score was zero.
				if keywordScore == 0 && embScore < 0.5 {
					continue
				}
			}
		}

		if cfg.minScore > 0 && finalScore < cfg.minScore {
			continue
		}

		results = append(results, Result{
			Page:    page,
			Score:   finalScore,
			Matches: matches,
		})
	}

	// Sort descending by score, break ties by title for determinism.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Page.Frontmatter.Title < results[j].Page.Frontmatter.Title
	})

	// Apply reranker if provided.
	if cfg.reranker != nil {
		topN := 20
		if len(results) < topN {
			topN = len(results)
		}
		if topN > 0 {
			candidates := make([]rerank.Candidate, topN)
			for i := 0; i < topN; i++ {
				candidates[i] = rerank.Candidate{
					ID:            results[i].Page.Frontmatter.ID,
					Text:          results[i].Page.Body,
					OriginalScore: results[i].Score,
				}
			}
			ranked, err := cfg.reranker.Rerank(cfg.ctx, query, candidates)
			if err != nil {
				log.Printf("search: reranker failed, keeping original order: %v", err)
			} else {
				reranked := make([]Result, 0, len(ranked)+len(results)-topN)
				for _, rr := range ranked {
					if page, ok := idx.Get(rr.ID); ok {
						reranked = append(reranked, Result{
							Page:  page,
							Score: rr.Score,
						})
					}
				}
				// Append remaining results that were not sent to reranker.
				reranked = append(reranked, results[topN:]...)
				results = reranked
			}
		}
	}

	// Apply limit.
	if cfg.limit > 0 && len(results) > cfg.limit {
		results = results[:cfg.limit]
	}

	return results
}

// contentHashForPage returns a content hash for a page. This is a simple
// hash based on the page ID for now; the real implementation will come from
// the index loader when content hashing is fully integrated.
func contentHashForPage(page *schema.Page) string {
	// Use source path as a stable identifier for cache lookups.
	if page.SourcePath != "" {
		return page.SourcePath
	}
	return page.Frontmatter.ID
}

// scorePage computes the base score for a page against the query. For
// multi-value fields (tags, entities, aliases, semantic-hints, relationships)
// the max match score is used (not sum) to prevent stuffing.
func scorePage(page *schema.Page, q string) ([]Match, float64) {
	fm := page.Frontmatter
	var matches []Match
	var totalScore float64

	// --- Single-value fields: title ---
	if m, ok := bestMatch(q, fm.Title); ok {
		matches = append(matches, Match{Field: "title", Value: fm.Title, Type: m})
		totalScore += float64(weightTitle) * matchTypeScore(m)
	}

	// --- Multi-value: entity names ---
	bestEnt, bestEntVal, bestEntMatch := bestOfMulti(q, entityNames(fm.Entities))
	if bestEnt {
		matches = append(matches, Match{Field: "entity-name", Value: bestEntVal, Type: bestEntMatch})
		totalScore += float64(weightEntityName) * matchTypeScore(bestEntMatch)
	}

	// --- Multi-value: aliases ---
	bestAl, bestAlVal, bestAlMatch := bestOfMulti(q, entityAliases(fm.Entities))
	if bestAl {
		matches = append(matches, Match{Field: "alias", Value: bestAlVal, Type: bestAlMatch})
		totalScore += float64(weightAlias) * matchTypeScore(bestAlMatch)
	}

	// --- Multi-value: tags ---
	bestT, bestTVal, bestTMatch := bestOfMulti(q, fm.Tags)
	if bestT {
		matches = append(matches, Match{Field: "tag", Value: bestTVal, Type: bestTMatch})
		totalScore += float64(weightTag) * matchTypeScore(bestTMatch)
	}

	// --- Multi-value: semantic hints ---
	bestSH, bestSHVal, bestSHMatch := bestOfMulti(q, fm.SemanticHints)
	if bestSH {
		matches = append(matches, Match{Field: "semantic-hint", Value: bestSHVal, Type: bestSHMatch})
		totalScore += float64(weightSemanticHint) * matchTypeScore(bestSHMatch)
	}

	// --- Multi-value: relationship targets ---
	targets := make([]string, len(fm.Relationships))
	for i, rel := range fm.Relationships {
		targets[i] = rel.Target
	}
	bestR, bestRVal, bestRMatch := bestOfMulti(q, targets)
	if bestR {
		matches = append(matches, Match{Field: "relationship-target", Value: bestRVal, Type: bestRMatch})
		totalScore += float64(weightRelTarget) * matchTypeScore(bestRMatch)
	}

	// --- Body: check each word ---
	if bodyMatch, ok := matchBody(q, page.Body); ok {
		matches = append(matches, Match{Field: "body", Value: bodyMatch.Value, Type: bodyMatch.Type})
		totalScore += float64(weightBody) * matchTypeScore(bodyMatch.Type)
	}

	return matches, totalScore
}

// temporalMultiplier computes the temporal decay multiplier for a page.
// If no temporal info is present, returns confidence (or 1.0 if confidence is 0).
func temporalMultiplier(page *schema.Page, now time.Time) float64 {
	fm := page.Frontmatter
	confidence := fm.Confidence
	decayRate := fm.Temporal.DecayRate
	lastVerifiedStr := fm.Temporal.LastVerified

	if lastVerifiedStr == "" || decayRate == 0 {
		if confidence == 0 {
			return 1.0
		}
		return confidence
	}

	lastVerified, err := time.Parse("2006-01-02", lastVerifiedStr)
	if err != nil {
		if confidence == 0 {
			return 1.0
		}
		return confidence
	}

	return temporal.DecayedConfidenceAt(confidence, decayRate, lastVerified, now)
}

// bestMatch returns the best MatchType for query against a single value.
func bestMatch(q, value string) (MatchType, bool) {
	v := strings.ToLower(value)
	if v == q {
		return MatchExact, true
	}
	if strings.HasPrefix(v, q) {
		return MatchPrefix, true
	}
	if strings.Contains(v, q) {
		return MatchContains, true
	}
	if levenshtein(q, v) <= 2 {
		return MatchFuzzy, true
	}
	return 0, false
}

// bestOfMulti finds the best match across multiple values (max, not sum).
func bestOfMulti(q string, values []string) (found bool, bestVal string, bestMT MatchType) {
	bestScore := -1.0
	for _, v := range values {
		mt, ok := bestMatch(q, v)
		if !ok {
			continue
		}
		s := matchTypeScore(mt)
		if s > bestScore {
			bestScore = s
			bestVal = v
			bestMT = mt
			found = true
		}
	}
	return
}

// matchBody checks the body text word-by-word for a match. Returns the best
// single match (max, not sum).
func matchBody(q, body string) (Match, bool) {
	words := strings.Fields(strings.ToLower(body))
	bestScore := -1.0
	var bestMatch Match
	found := false

	for _, w := range words {
		mt, ok := matchWord(q, w)
		if !ok {
			continue
		}
		s := matchTypeScore(mt)
		if s > bestScore {
			bestScore = s
			bestMatch = Match{Field: "body", Value: w, Type: mt}
			found = true
		}
		if s == 1.0 {
			break // can't do better than exact
		}
	}
	return bestMatch, found
}

// matchWord matches a query against a single word.
func matchWord(q, word string) (MatchType, bool) {
	if word == q {
		return MatchExact, true
	}
	if strings.HasPrefix(word, q) {
		return MatchPrefix, true
	}
	if strings.Contains(word, q) {
		return MatchContains, true
	}
	if levenshtein(q, word) <= 2 {
		return MatchFuzzy, true
	}
	return 0, false
}

// entityNames extracts all entity names.
func entityNames(entities []schema.Entity) []string {
	names := make([]string, len(entities))
	for i, e := range entities {
		names[i] = e.Name
	}
	return names
}

// entityAliases extracts all aliases from all entities.
func entityAliases(entities []schema.Entity) []string {
	var aliases []string
	for _, e := range entities {
		aliases = append(aliases, e.Aliases...)
	}
	return aliases
}

// levenshtein computes the Levenshtein edit distance between two strings
// using the iterative Wagner-Fischer algorithm.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Early termination: if length difference > 2, distance is > 2.
	diff := len(a) - len(b)
	if diff > 2 || diff < -2 {
		if diff < 0 {
			return -diff
		}
		return diff
	}

	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)

	// Use single row optimization.
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(
				prev[j]+1,     // deletion
				curr[j-1]+1,   // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev = curr
	}

	return prev[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
