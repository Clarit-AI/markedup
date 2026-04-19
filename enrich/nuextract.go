package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/Clarit-AI/markedup/llm"
	"github.com/Clarit-AI/markedup/schema"
	"golang.org/x/sync/errgroup"
)

// NuExtract-2.0 default predicate enum, used when config.NuExtract.Predicates
// and the Extract predicates arg are both empty. Enums must be small and
// stable because the model constrains outputs to them.
var defaultNuExtractPredicates = []string{
	"works_for", "located_in", "part_of", "depends_on",
	"created_by", "uses", "relates_to", "mentions", "precedes",
}

// defaultNuExtractEntityTypes mirrors DefaultEntityTypes but uppercased for
// NuExtract's verbatim-string convention.
var defaultNuExtractEntityTypes = []string{
	"PERSON", "ORGANIZATION", "LOCATION", "CONCEPT",
	"PROJECT", "TECHNOLOGY", "EVENT", "DATE", "OTHER",
}

// nuExtractStrength is the default relationship strength for NuExtract output.
// Slightly lower than Triplex's 0.8 because NuExtract has no co-reference pass.
const nuExtractStrength = 0.7

// nuExtractManualTemplate is the client-side chat template for GGUF runtimes
// (llama.cpp, LM Studio, Ollama) that don't honor chat_template_kwargs.
// The shape — "# Template:\n...\n# Context:\n..." — matches NuMind's
// recommended rendering from the model card.
const nuExtractManualTemplate = "# Template:\n%s\n\n# Context:\n%s"

// runNuExtract is the dispatcher invoked by ModelExtractor.Extract when
// cfg.Format == FormatNuExtract. It honors NuExtractOptions overrides and
// resolves mode + transport before delegating.
func (m *ModelExtractor) runNuExtract(ctx context.Context, entityTypes, predicates []string, body string) (*ModelResult, error) {
	opts := m.cfg.NuExtract
	if len(opts.EntityTypes) > 0 {
		entityTypes = opts.EntityTypes
	} else if len(entityTypes) == 0 {
		entityTypes = defaultNuExtractEntityTypes
	}
	if len(opts.Predicates) > 0 {
		predicates = opts.Predicates
	} else if len(predicates) == 0 {
		predicates = defaultNuExtractPredicates
	}

	transport := opts.Transport
	if transport == "" {
		transport = autodetectTransport(m.cfg.Endpoint)
	}

	if opts.Mode == "single" {
		return m.nuExtractSingle(ctx, entityTypes, predicates, body, transport)
	}
	return m.nuExtractParallel(ctx, entityTypes, predicates, body, transport)
}

// autodetectTransport picks "native" for cloud URLs and "manual" for
// localhost / private-network endpoints where GGUF runtimes typically live.
// Matches RFC1918 IPv4 ranges, IPv6 loopback, 0.0.0.0, and .local/.lan/.home
// DNS suffixes. Err on the side of "manual" — a false positive on a
// GGUF-local endpoint would send unsupported chat_template_kwargs.
func autodetectTransport(endpoint string) string {
	host := hostFromEndpoint(endpoint)
	if host == "" {
		return "native"
	}
	lower := strings.ToLower(host)

	if lower == "localhost" || lower == "127.0.0.1" || lower == "0.0.0.0" ||
		lower == "::1" {
		return "manual"
	}
	for _, suffix := range []string{".local", ".lan", ".home", ".internal"} {
		if strings.HasSuffix(lower, suffix) {
			return "manual"
		}
	}
	if ip := net.ParseIP(lower); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return "manual"
		}
	}
	return "native"
}

// hostFromEndpoint extracts the hostname from a URL-like endpoint, stripping
// scheme, path, port, and IPv6 brackets. Falls back to the raw string.
func hostFromEndpoint(endpoint string) string {
	s := strings.TrimSpace(endpoint)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "[") {
		if end := strings.Index(s, "]"); end >= 0 {
			return s[1:end]
		}
	}
	if strings.Count(s, ":") == 1 {
		s = strings.SplitN(s, ":", 2)[0]
	}
	return s
}

func buildNuExtractEntitiesTemplate(entityTypes []string) string {
	tmpl := map[string]any{
		"entities": []map[string]any{
			{
				"name": "verbatim-string",
				"type": entityTypes,
			},
		},
	}
	b, _ := json.Marshal(tmpl)
	return string(b)
}

func buildNuExtractRelationsTemplate(predicates []string) string {
	tmpl := map[string]any{
		"relationships": []map[string]any{
			{
				"subject":   "verbatim-string",
				"predicate": predicates,
				"object":    "verbatim-string",
			},
		},
	}
	b, _ := json.Marshal(tmpl)
	return string(b)
}

func buildNuExtractCombinedTemplate(entityTypes, predicates []string) string {
	tmpl := map[string]any{
		"entities": []map[string]any{
			{"name": "verbatim-string", "type": entityTypes},
		},
		"relationships": []map[string]any{
			{"subject": "verbatim-string", "predicate": predicates, "object": "verbatim-string"},
		},
	}
	b, _ := json.Marshal(tmpl)
	return string(b)
}

// callNuExtract sends a template + body to the model and returns the raw
// response content. Transport selects native (chat_template_kwargs) vs.
// manual (client-rendered prompt).
func (m *ModelExtractor) callNuExtract(ctx context.Context, template, body, transport string) (string, error) {
	if transport == "native" {
		req := llm.Request{
			Messages: []llm.Message{
				{Role: "user", Content: body},
			},
			Extra: map[string]any{
				"chat_template_kwargs": map[string]any{"template": template},
				"temperature":          0,
			},
		}
		return m.llmClient.ChatCompletionWith(ctx, req)
	}
	// manual
	content := fmt.Sprintf(nuExtractManualTemplate, template, body)
	return m.llmClient.ChatCompletion(ctx, []llm.Message{{Role: "user", Content: content}})
}

func (m *ModelExtractor) nuExtractParallel(ctx context.Context, entityTypes, predicates []string, body, transport string) (*ModelResult, error) {
	entTmpl := buildNuExtractEntitiesTemplate(entityTypes)
	relTmpl := buildNuExtractRelationsTemplate(predicates)

	var (
		entContent, relContent string
		mu                     sync.Mutex
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		c, err := m.callNuExtract(gctx, entTmpl, body, transport)
		if err != nil {
			return fmt.Errorf("nuextract entities: %w", err)
		}
		mu.Lock()
		entContent = c
		mu.Unlock()
		return nil
	})
	g.Go(func() error {
		c, err := m.callNuExtract(gctx, relTmpl, body, transport)
		if err != nil {
			return fmt.Errorf("nuextract relations: %w", err)
		}
		mu.Lock()
		relContent = c
		mu.Unlock()
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	entities, entType, err := parseNuExtractEntities(entContent)
	if err != nil {
		return nil, err
	}
	relationships, err := parseNuExtractRelations(relContent)
	if err != nil {
		return nil, err
	}

	return &ModelResult{
		Entities:      dedupEntities(entities),
		Relationships: dedupRelationships(relationships),
		EntityType:    entType,
	}, nil
}

func (m *ModelExtractor) nuExtractSingle(ctx context.Context, entityTypes, predicates []string, body, transport string) (*ModelResult, error) {
	tmpl := buildNuExtractCombinedTemplate(entityTypes, predicates)
	content, err := m.callNuExtract(ctx, tmpl, body, transport)
	if err != nil {
		return nil, fmt.Errorf("nuextract combined: %w", err)
	}
	entities, rels, entType, err := parseNuExtractCombined(content)
	if err != nil {
		return nil, err
	}
	return &ModelResult{
		Entities:      dedupEntities(entities),
		Relationships: dedupRelationships(rels),
		EntityType:    entType,
	}, nil
}

// repairNuExtractJSON applies a conservative, pattern-targeted sequence of
// textual repairs to NuExtract responses. It is a best-effort pre-pass in
// front of json.Unmarshal: every repair first checks for a specific
// known-bad signature and only mutates bytes when matched. Valid JSON flows
// through unchanged.
//
// Handled patterns (see issue #107):
//
//	A. Two top-level objects concatenated by a comma:
//	   '{"entities":[…]}, {"relationships":[…]}' → merged into one object.
//	B. Missing colon between an object key and its array value:
//	   '{"entities […]}' → '{"entities": […]}'. Also covers the
//	   "key lost its closing quote" variant (Pattern C) when followed by ' ['.
//	C. Missing closing quote on the key (covered by B's detector).
//	D. Bracket/brace mismatch inside an array element, e.g.
//	   '"type":["CONCEPT"}]' → '"type":["CONCEPT"]}'. We flip the pair
//	   only when we detect `"]` immediately followed by `}` where an
//	   earlier `{` is still open at the same nesting level.
//	E. Truncated trailing close: when the payload opens N braces and
//	   closes only N-1, append a single '}'. We only append one brace —
//	   deeper truncation is left for the caller's error path.
//
// The function always returns a []byte (never nil); callers can safely
// pass the result straight to json.Unmarshal.
func repairNuExtractJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	out := raw

	// Pattern A — "}, {" joining two top-level objects.
	out = repairConcatenatedObjects(out)

	// Patterns B + C — missing colon (and/or closing key quote) before '['.
	out = repairMissingColonBeforeArray(out)

	// Pattern D — mismatched '}' where a ']' is expected inside an array.
	out = repairBracketBraceMismatch(out)

	// Pattern E — single missing trailing '}'.
	out = repairTruncatedTrailingBrace(out)

	return out
}

// repairConcatenatedObjects collapses the exact NuExtract "single-mode
// concatenated" pattern: `}, {` joining two top-level objects that each
// hold one of the expected keys ("entities" / "relationships"). We only
// rewrite when BOTH halves look like the well-known NuExtract shape,
// otherwise a legitimate `}, {` inside an array (impossible at top level
// but possible for future schemas) would be corrupted.
func repairConcatenatedObjects(raw []byte) []byte {
	s := string(raw)
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return raw
	}
	// Find a top-level `}, {` — i.e. at brace-depth 0 outside of strings.
	idx, ok := findTopLevelObjectJoin(trimmed)
	if !ok {
		return raw
	}
	left := trimmed[:idx]    // includes closing '}'
	right := trimmed[idx+1:] // starts with ' {' or ', {' remnant
	right = strings.TrimLeft(right, ", \t\n\r")
	if !strings.HasPrefix(right, "{") {
		return raw
	}
	// Require both halves to hold a NuExtract-shaped key.
	if !containsAnyKey(left, `"entities"`, `"relationships"`) ||
		!containsAnyKey(right, `"entities"`, `"relationships"`) {
		return raw
	}
	// Drop closing '}' of left, drop opening '{' of right, join with ','.
	leftInner := strings.TrimSuffix(left, "}")
	rightInner := strings.TrimPrefix(right, "{")
	merged := leftInner + "," + rightInner
	return []byte(merged)
}

// findTopLevelObjectJoin returns the index of the '}' in the first
// top-level `}, {` sequence (depth == 0 immediately after the '}').
func findTopLevelObjectJoin(s string) (int, bool) {
	depth := 0
	inStr := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if c == '}' && depth == 0 {
				// Look ahead for ", {" — possibly with whitespace.
				j := i + 1
				for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
					j++
				}
				if j < len(s) && s[j] == ',' {
					k := j + 1
					for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\n' || s[k] == '\r') {
						k++
					}
					if k < len(s) && s[k] == '{' {
						return i, true
					}
				}
			}
		}
	}
	return 0, false
}

func containsAnyKey(s string, keys ...string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// repairMissingColonBeforeArray fixes Patterns B and C: the model writes
// `{"entities [` (no colon, and sometimes no closing quote on the key)
// instead of `{"entities": [`. We look for an identifier-like token that
// should be a quoted key, followed by optional whitespace and `[`, with
// no `:` between them. Only the three NuExtract top-level keys are
// candidates — we refuse to speculate about arbitrary identifiers.
func repairMissingColonBeforeArray(raw []byte) []byte {
	s := string(raw)
	candidates := []string{"entities", "relationships"}
	changed := false
	for _, key := range candidates {
		// Variant 1 (Pattern B): "entities [   — closing quote present, colon missing.
		bad := `"` + key + `" [`
		good := `"` + key + `": [`
		if strings.Contains(s, bad) && !strings.Contains(s, good) {
			s = strings.ReplaceAll(s, bad, good)
			changed = true
		}
		// Variant 2 (Pattern C): "entities [   — closing quote missing too.
		badNoClose := `"` + key + ` [`
		if strings.Contains(s, badNoClose) && !strings.Contains(s, good) {
			s = strings.ReplaceAll(s, badNoClose, good)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return []byte(s)
}

// repairBracketBraceMismatch fixes Pattern D: a `}` appears where the
// current open frame is an array, meaning the model swapped `]` for `}`.
// We walk the byte stream keeping a stack of open `{`/`[` frames; when a
// `}` is seen while the top frame is `[`, we flip it to `]`. A matching
// `]` appearing later while the top frame is `{` is flipped back to `}`.
// The walker respects string literals (with escapes) so it never rewrites
// characters inside a JSON string value. If no mismatch is observed,
// the input is returned byte-for-byte unchanged.
func repairBracketBraceMismatch(raw []byte) []byte {
	buf := make([]byte, len(raw))
	copy(buf, raw)
	stack := make([]byte, 0, 16)
	inStr := false
	escape := false
	changed := false
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if inStr {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			stack = append(stack, c)
		case '}':
			if n := len(stack); n > 0 {
				top := stack[n-1]
				if top == '[' {
					// Mismatch — model emitted '}' where ']' was expected.
					buf[i] = ']'
					stack = stack[:n-1]
					changed = true
					continue
				}
				stack = stack[:n-1]
			}
		case ']':
			if n := len(stack); n > 0 {
				top := stack[n-1]
				if top == '{' {
					// Mismatch — model emitted ']' where '}' was expected.
					buf[i] = '}'
					stack = stack[:n-1]
					changed = true
					continue
				}
				stack = stack[:n-1]
			}
		}
	}
	if !changed {
		return raw
	}
	return buf
}

// repairTruncatedTrailingBrace handles Pattern E: a payload missing
// exactly one trailing '}'. We only append when the overall brace count
// is off-by-one positive AND bracket count is balanced — anything more
// is too risky to synthesize.
func repairTruncatedTrailingBrace(raw []byte) []byte {
	brace, bracket := countBalance(raw)
	if brace == 1 && bracket == 0 {
		return append(append([]byte{}, raw...), '}')
	}
	return raw
}

// countBalance returns (openBraces-closeBraces, openBrackets-closeBrackets),
// ignoring characters inside JSON strings (quote handling with escapes).
func countBalance(raw []byte) (int, int) {
	braces, brackets := 0, 0
	inStr := false
	escape := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			braces++
		case '}':
			braces--
		case '[':
			brackets++
		case ']':
			brackets--
		}
	}
	return braces, brackets
}

// stripJSONFences removes surrounding ```json ... ``` if present.
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 3 {
		return s
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

type nuextractEntity struct {
	Name string        `json:"name"`
	Type nuextractType `json:"type"`
}

// nuextractType tolerates both string ("PERSON") and array-of-string
// (["PERSON"]) values for the "type" field. NuExtract's template declares
// the type enum as an array, and MLX quants occasionally echo the array
// literal back verbatim instead of selecting a single enum value. We accept
// either and collapse arrays to their first non-empty string.
type nuextractType string

func (t *nuextractType) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*t = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = nuextractType(s)
		return nil
	}
	if trimmed[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err == nil {
			for _, s := range arr {
				if strings.TrimSpace(s) != "" {
					*t = nuextractType(s)
					return nil
				}
			}
			*t = ""
			return nil
		}
		// Fall through to any-slice fallback for [null, "X"] etc.
		var anyArr []any
		if err := json.Unmarshal(data, &anyArr); err != nil {
			return err
		}
		for _, v := range anyArr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				*t = nuextractType(s)
				return nil
			}
		}
		*t = ""
		return nil
	}
	// unknown shape — leave empty, don't fail parse
	*t = ""
	return nil
}

type nuextractRelation struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

func parseNuExtractEntities(content string) ([]schema.Entity, string, error) {
	var raw struct {
		Entities []nuextractEntity `json:"entities"`
	}
	content = stripJSONFences(content)
	repaired := repairNuExtractJSON([]byte(content))
	if err := json.Unmarshal(repaired, &raw); err != nil {
		return nil, "", fmt.Errorf("parse nuextract entities: %w\nraw: %s", err, truncate(content, 500))
	}
	return entitiesFromRaw(raw.Entities)
}

func parseNuExtractRelations(content string) ([]schema.Relationship, error) {
	var raw struct {
		Relationships []nuextractRelation `json:"relationships"`
	}
	content = stripJSONFences(content)
	repaired := repairNuExtractJSON([]byte(content))
	if err := json.Unmarshal(repaired, &raw); err != nil {
		return nil, fmt.Errorf("parse nuextract relations: %w\nraw: %s", err, truncate(content, 500))
	}
	return relationshipsFromRaw(raw.Relationships), nil
}

func parseNuExtractCombined(content string) ([]schema.Entity, []schema.Relationship, string, error) {
	var raw struct {
		Entities      []nuextractEntity   `json:"entities"`
		Relationships []nuextractRelation `json:"relationships"`
	}
	content = stripJSONFences(content)
	repaired := repairNuExtractJSON([]byte(content))
	if err := json.Unmarshal(repaired, &raw); err != nil {
		return nil, nil, "", fmt.Errorf("parse nuextract combined: %w\nraw: %s", err, truncate(content, 500))
	}
	entities, entType, err := entitiesFromRaw(raw.Entities)
	if err != nil {
		return nil, nil, "", err
	}
	return entities, relationshipsFromRaw(raw.Relationships), entType, nil
}

func entitiesFromRaw(raw []nuextractEntity) ([]schema.Entity, string, error) {
	entities := make([]schema.Entity, 0, len(raw))
	typeCounts := map[string]int{}
	for _, e := range raw {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		role := string(e.Type)
		entities = append(entities, schema.Entity{Name: name, Role: role})
		if role != "" {
			typeCounts[role]++
		}
	}
	bestType := ""
	bestCount := 0
	for t, c := range typeCounts {
		if c > bestCount || (c == bestCount && bestType == "") {
			bestType = t
			bestCount = c
		}
	}
	return entities, strings.ToLower(bestType), nil
}

func relationshipsFromRaw(raw []nuextractRelation) []schema.Relationship {
	rels := make([]schema.Relationship, 0, len(raw))
	for _, r := range raw {
		target := strings.TrimSpace(r.Object)
		if target == "" || strings.TrimSpace(r.Predicate) == "" {
			continue
		}
		rels = append(rels, schema.Relationship{
			Target:   target,
			Type:     r.Predicate,
			Strength: nuExtractStrength,
		})
	}
	return rels
}

func dedupEntities(in []schema.Entity) []schema.Entity {
	seen := map[string]bool{}
	out := make([]schema.Entity, 0, len(in))
	for _, e := range in {
		k := strings.ToLower(e.Name) + "|" + strings.ToLower(e.Role)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}

func dedupRelationships(in []schema.Relationship) []schema.Relationship {
	seen := map[string]bool{}
	out := make([]schema.Relationship, 0, len(in))
	for _, r := range in {
		k := strings.ToLower(r.Target) + "|" + strings.ToLower(r.Type)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}
