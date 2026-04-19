// Package enrich — LLM fallback (Phase 1).
//
// When the primary Tier 2 extractor (NuExtract/Triplex) returns a parse-error
// (raw model output that couldn't be unmarshalled even after the repair
// pre-pass — issues #107, #131, #133, #135), the file is enqueued for a
// batched fallback pass that asks a more capable chat-completion LLM to
// produce the same NuExtract-shape JSON. The recovered result is fed into the
// existing MergeModelResult + MergeSummary path so frontmatter looks
// identical to a successful primary run.
//
// Empty-result responses (the model ran cleanly but found no entities) are
// NOT enqueued — see issue #134's design discussion.
//
// Phase 1 = sequential dispatcher only. Parallel execution (#141) and
// coding-agent handoff (#142) ship in later PRs.
package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/Clarit-AI/markedup/llm"
	"github.com/Clarit-AI/markedup/schema"
)

// ErrParse is the sentinel error returned (wrapped) by Tier 2 extractors when
// the model's raw output cannot be parsed as the expected JSON shape, even
// after the repair pre-pass. The fallback dispatcher uses errors.Is(err,
// ErrParse) to distinguish "model produced unparseable output" (recoverable
// via LLM fallback) from "transport / endpoint / network / config error"
// (not recoverable by retrying with a different prompt).
var ErrParse = errors.New("enrich: parse error")

// IsParseError reports whether err is or wraps ErrParse.
func IsParseError(err error) bool {
	return err != nil && errors.Is(err, ErrParse)
}

// FallbackExtractor recovers Tier 2 extraction failures by asking a
// chat-completion LLM to produce the same NuExtract-shape JSON the primary
// extractor would have. Implementations MUST return output that
// MergeModelResult can consume directly.
type FallbackExtractor interface {
	// Extract returns a NuExtract-shape ModelResult for body. entityTypes /
	// predicates may be nil; the implementation is responsible for picking
	// sensible defaults. The returned result MUST be safe to feed into
	// MergeModelResult.
	Extract(ctx context.Context, body string, entityTypes, predicates []string) (*ModelResult, error)
}

// FallbackJob is one queued failure awaiting LLM-fallback retry.
//
// Body is the raw markdown body sent to the primary extractor (no
// frontmatter). PrimaryErr is the original parse error — kept for log
// surfacing only; the dispatcher does not branch on its content.
type FallbackJob struct {
	Path        string
	RelPath     string
	Body        string
	EntityTypes []string
	Predicates  []string
	PrimaryErr  error
}

// FallbackOutcome is the per-file result of a fallback attempt.
type FallbackOutcome struct {
	Job          FallbackJob
	Result       *ModelResult // nil when Err != nil
	Err          error
	InputTokens  int // approximate
	OutputTokens int // approximate
}

// Recovered reports whether the fallback produced a usable result.
func (o FallbackOutcome) Recovered() bool {
	return o.Err == nil && o.Result != nil
}

// LLMFallbackExtractor is the default FallbackExtractor. It calls the
// configured chat-completion endpoint with a NuExtract-shape JSON schema
// (json_schema response_format) when SchemaMode is true, otherwise falls back
// to a prompt-only "respond with JSON" instruction.
type LLMFallbackExtractor struct {
	client     *llm.Client
	model      string
	schemaMode bool
}

// LLMFallbackConfig configures LLMFallbackExtractor.
type LLMFallbackConfig struct {
	Endpoint   string
	Model      string
	APIKey     string
	HTTPClient interface{} // *http.Client; kept opaque to avoid an extra import here
	// SchemaMode toggles OpenAI-style response_format=json_schema. Disable for
	// endpoints that don't support it (the prompt-only path still asks for JSON
	// and parses it with the same shape tolerance as parseNuExtractCombined).
	SchemaMode bool
}

// NewLLMFallbackExtractor constructs a fallback extractor wired to the given
// chat-completion endpoint. The underlying llm.Client uses http.DefaultClient
// when HTTPClient is nil.
func NewLLMFallbackExtractor(cfg LLMFallbackConfig) *LLMFallbackExtractor {
	lc := llm.Config{
		Endpoint: cfg.Endpoint,
		Model:    cfg.Model,
		APIKey:   cfg.APIKey,
	}
	return &LLMFallbackExtractor{
		client:     llm.NewClient(lc),
		model:      cfg.Model,
		schemaMode: cfg.SchemaMode,
	}
}

// fallbackJSONSchema mirrors the NuExtract output shape that
// MergeModelResult / MergeSummary already understand. Kept inline rather than
// generated so the prompt and the schema can drift together.
const fallbackJSONSchema = `{
  "type": "object",
  "properties": {
    "entities": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string"},
          "type": {"type": "string"}
        },
        "required": ["name", "type"]
      }
    },
    "relationships": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "subject": {"type": "string"},
          "predicate": {"type": "string"},
          "object": {"type": "string"}
        },
        "required": ["subject", "predicate", "object"]
      }
    },
    "summary": {"type": "string"},
    "entity_type": {"type": "string"}
  },
  "required": ["entities", "relationships", "summary", "entity_type"]
}`

// fallbackSystemPrompt is intentionally NuExtract-flavored: the merge path
// downstream expects entity.type → schema.Entity.Role and the SPO triple
// shape that parseNuExtractCombined would have produced.
const fallbackSystemPrompt = `You are a knowledge-graph extraction fallback. The primary extractor (a small NER model) failed to produce parseable JSON for this document. Re-extract the same shape.

Return ONLY a JSON object — no markdown fences, no commentary — matching:

{
  "entities":      [{"name": "<string>", "type": "<UPPERCASE_TYPE>"}],
  "relationships": [{"subject": "<entity name>", "predicate": "<lower_snake>", "object": "<entity name>"}],
  "summary":       "<one-sentence description of what this document represents>",
  "entity_type":   "<one of: person, organization, concept, project, technology, event, location, document>"
}

Rules:
- entity types are UPPERCASE single words (PERSON, ORGANIZATION, CONCEPT, PROJECT, TECHNOLOGY, EVENT, LOCATION, OTHER).
- relationships reference entities by their "name" field — do NOT invent names that aren't in entities.
- summary is exactly one sentence about the entity/concept the document represents.
- entity_type is a lowercase classification of the document itself.
- omit fields you cannot fill (use [] for arrays, "" for strings) — do NOT hallucinate.`

// fallbackPayload is the wire shape the fallback LLM is asked to produce.
// Kept separate from ModelResult because the field names differ
// (relationships use subject/predicate/object instead of target/type) — we
// translate in toModelResult below to land cleanly in the existing merge
// path.
type fallbackPayload struct {
	Entities []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entities"`
	Relationships []struct {
		Subject   string `json:"subject"`
		Predicate string `json:"predicate"`
		Object    string `json:"object"`
	} `json:"relationships"`
	Summary    string `json:"summary"`
	EntityType string `json:"entity_type"`
}

// Extract implements FallbackExtractor.
func (e *LLMFallbackExtractor) Extract(ctx context.Context, body string, entityTypes, predicates []string) (*ModelResult, error) {
	user := body
	if len(entityTypes) > 0 || len(predicates) > 0 {
		// Surface the primary's enums to bias the fallback, but don't make
		// them load-bearing — the prompt's UPPERCASE rule wins on conflict.
		user = fmt.Sprintf("Allowed entity types: %s\nAllowed predicates: %s\n\nDocument:\n%s",
			strings.Join(entityTypes, ", "),
			strings.Join(predicates, ", "),
			body)
	}

	req := llm.Request{
		Model: e.model,
		Messages: []llm.Message{
			{Role: "system", Content: fallbackSystemPrompt},
			{Role: "user", Content: user},
		},
	}
	if e.schemaMode {
		req.Extra = map[string]any{
			"response_format": map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   "markedup_fallback_extraction",
					"strict": true,
					"schema": json.RawMessage(fallbackJSONSchema),
				},
			},
		}
	}

	content, err := e.client.ChatCompletionWith(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("enrich fallback: %w", err)
	}

	return parseFallbackPayload(content)
}

func parseFallbackPayload(content string) (*ModelResult, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		// Strip leading/trailing fence lines, tolerating ```json.
		if i := strings.Index(content, "\n"); i >= 0 {
			content = content[i+1:]
		}
		if j := strings.LastIndex(content, "```"); j >= 0 {
			content = content[:j]
		}
		content = strings.TrimSpace(content)
	}

	var p fallbackPayload
	if err := json.Unmarshal([]byte(content), &p); err != nil {
		return nil, fmt.Errorf("enrich fallback: parse payload: %w\nraw: %s", err, truncate(content, 500))
	}

	res := &ModelResult{
		EntityType: strings.ToLower(strings.TrimSpace(p.EntityType)),
		Summary:    strings.TrimSpace(p.Summary),
	}
	for _, ent := range p.Entities {
		name := strings.TrimSpace(ent.Name)
		if name == "" {
			continue
		}
		res.Entities = append(res.Entities, schema.Entity{
			Name: name,
			Role: strings.TrimSpace(ent.Type),
		})
	}
	// Index entities by lowercased name so we can resolve the SPO triples
	// against canonical casing rather than whatever the model echoed back.
	known := make(map[string]string, len(res.Entities))
	for _, e := range res.Entities {
		known[strings.ToLower(e.Name)] = e.Name
	}
	for _, r := range p.Relationships {
		obj := strings.TrimSpace(r.Object)
		pred := strings.TrimSpace(r.Predicate)
		if obj == "" || pred == "" {
			continue
		}
		if canonical, ok := known[strings.ToLower(obj)]; ok {
			obj = canonical
		}
		res.Relationships = append(res.Relationships, schema.Relationship{
			Target:   obj,
			Type:     pred,
			Strength: nuExtractStrength,
		})
	}
	return res, nil
}

// IsLocalEndpoint reports whether endpoint refers to a local LLM (localhost,
// 127.0.0.1, or no scheme/host). Used by config defaults: local endpoints
// default-on with no MaxFiles cap; cloud endpoints default-off with a 50-file
// cap to limit accidental token spend.
func IsLocalEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return true
	}
	// Strip scheme if present so net/url doesn't choke on bare host:port.
	parsed, err := url.Parse(endpoint)
	host := ""
	switch {
	case err == nil && parsed.Host != "":
		host = parsed.Host
	case !strings.Contains(endpoint, "://"):
		// "localhost:11434" or "127.0.0.1" — treat the whole thing as host.
		host = endpoint
	default:
		return false
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// FallbackBatchOptions controls RunFallbackBatch.
type FallbackBatchOptions struct {
	// MaxFiles caps the number of jobs processed. <=0 means unbounded.
	MaxFiles int
	// Logger receives one line per file (token counts, recovered/failed).
	// Defaults to a no-op when nil.
	Logger func(format string, args ...any)
}

// RunFallbackBatch processes queued FallbackJobs sequentially and returns one
// FallbackOutcome per attempted job. Jobs beyond MaxFiles are skipped (no
// outcome appended). Caller is responsible for re-merging recovered results
// into frontmatter via MergeModelResult / MergeSummary.
func RunFallbackBatch(ctx context.Context, extractor FallbackExtractor, jobs []FallbackJob, opts FallbackBatchOptions) []FallbackOutcome {
	if extractor == nil || len(jobs) == 0 {
		return nil
	}
	log := opts.Logger
	if log == nil {
		log = func(string, ...any) {}
	}
	limit := len(jobs)
	if opts.MaxFiles > 0 && opts.MaxFiles < limit {
		log("LLM fallback: capping at MaxFiles=%d (%d failures queued)", opts.MaxFiles, len(jobs))
		limit = opts.MaxFiles
	}
	out := make([]FallbackOutcome, 0, limit)
	for i := 0; i < limit; i++ {
		job := jobs[i]
		inTok := approxTokens(job.Body)
		log("LLM fallback %d/%d: %s (~%d input tokens)", i+1, limit, job.RelPath, inTok)
		res, err := extractor.Extract(ctx, job.Body, job.EntityTypes, job.Predicates)
		outTok := 0
		if res != nil {
			outTok = approxTokensModelResult(res)
		}
		oc := FallbackOutcome{
			Job:          job,
			Result:       res,
			Err:          err,
			InputTokens:  inTok,
			OutputTokens: outTok,
		}
		if oc.Recovered() {
			log("LLM fallback %d/%d: %s recovered (~%d output tokens)", i+1, limit, job.RelPath, outTok)
		} else {
			log("LLM fallback %d/%d: %s failed: %v", i+1, limit, job.RelPath, err)
		}
		out = append(out, oc)
	}
	return out
}

// approxTokens is a deliberately cheap word→token approximation. Real
// tokenizers vary by model; for log telemetry the order-of-magnitude is what
// matters. Uses len(words) * 1.3 as a rough English-text heuristic.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	words := strings.Fields(s)
	return (len(words) * 13) / 10
}

func approxTokensModelResult(r *ModelResult) int {
	if r == nil {
		return 0
	}
	n := approxTokens(r.Summary) + approxTokens(r.EntityType)
	for _, e := range r.Entities {
		n += approxTokens(e.Name) + approxTokens(e.Role) + 2
	}
	for _, rel := range r.Relationships {
		n += approxTokens(rel.Target) + approxTokens(rel.Type) + 2
	}
	return n
}
