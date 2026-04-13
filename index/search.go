package index

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KHAEntertainment/markedup/schema"
	"github.com/KHAEntertainment/markedup/temporal"
)

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

// Reranker is a Phase 2 extension point. Implementations can reorder or
// re-score results using external signals (e.g. embeddings).
type Reranker interface {
	Rerank(query string, results []Result) ([]Result, error)
}

// SearchOption configures the search pipeline.
type SearchOption func(*searchConfig)

type searchConfig struct {
	limit    int
	minScore float64
	reranker Reranker
}

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

// WithReranker sets a reranker to post-process results (Phase 2 hook).
func WithReranker(r Reranker) SearchOption {
	return func(cfg *searchConfig) {
		cfg.reranker = r
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
// weighting, match-type multipliers, and temporal decay. Results are returned
// sorted by descending FinalScore.
func Search(idx *KnowledgeIndex, query string, opts ...SearchOption) []Result {
	if query == "" {
		return nil
	}

	cfg := &searchConfig{}
	for _, o := range opts {
		o(cfg)
	}

	q := strings.ToLower(strings.TrimSpace(query))
	now := time.Now()

	var results []Result
	for _, page := range idx.All() {
		matches, baseScore := scorePage(page, q)
		if baseScore == 0 {
			continue
		}

		tm := temporalMultiplier(page, now)
		finalScore := baseScore * tm

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
		reranked, err := cfg.reranker.Rerank(query, results)
		if err == nil {
			results = reranked
		}
	}

	// Apply limit.
	if cfg.limit > 0 && len(results) > cfg.limit {
		results = results[:cfg.limit]
	}

	return results
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
