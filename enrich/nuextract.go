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
	Name string `json:"name"`
	Type string `json:"type"`
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
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, "", fmt.Errorf("parse nuextract entities: %w\nraw: %s", err, truncate(content, 500))
	}
	return entitiesFromRaw(raw.Entities)
}

func parseNuExtractRelations(content string) ([]schema.Relationship, error) {
	var raw struct {
		Relationships []nuextractRelation `json:"relationships"`
	}
	content = stripJSONFences(content)
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
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
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
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
		entities = append(entities, schema.Entity{Name: name, Role: e.Type})
		if e.Type != "" {
			typeCounts[e.Type]++
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
