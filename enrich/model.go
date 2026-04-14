package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/KHAEntertainment/markedup/llm"
	"github.com/KHAEntertainment/markedup/schema"
)

// ModelFormat selects the prompt/parse strategy for Extract().
type ModelFormat int

const (
	FormatGeneric ModelFormat = iota // default: generic chat completion
	FormatTriplex                    // Triplex NER fine-tune format
)

// DefaultEntityTypes are the entity types used when none are specified.
var DefaultEntityTypes = []string{
	"PERSON", "ORGANIZATION", "CONCEPT", "TOOL", "EVENT", "LOCATION", "DOCUMENT",
}

// DefaultPredicates are the relationship predicates used when none are specified.
var DefaultPredicates = []string{
	"related-to", "derived-from", "implements", "depends-on", "created-by", "part-of", "used-by",
}

// ModelConfig configures the chat completion client for Tier 2 extraction.
type ModelConfig struct {
	Endpoint   string       // Base URL (e.g. http://localhost:11434)
	Model      string       // Model name (e.g. "triplex")
	APIKey     string       // Optional API key for authentication
	HTTPClient *http.Client // Optional; defaults to http.DefaultClient
	Format     ModelFormat  // zero value = FormatGeneric, no breaking change
}

// ModelExtractor calls a chat-completion API to extract structured knowledge.
type ModelExtractor struct {
	cfg       ModelConfig
	llmClient *llm.Client
}

// NewModelExtractor creates a new model extractor with the given config.
func NewModelExtractor(cfg ModelConfig) *ModelExtractor {
	llmClient := llm.NewClient(llm.Config{
		Endpoint:   cfg.Endpoint,
		Model:      cfg.Model,
		APIKey:     cfg.APIKey,
		HTTPClient: cfg.HTTPClient,
	})
	return &ModelExtractor{cfg: cfg, llmClient: llmClient}
}

// ModelResult holds Tier 2 extraction output.
type ModelResult struct {
	Entities          []schema.Entity       `json:"entities"`
	Relationships     []schema.Relationship `json:"relationships"`
	EntityType        string                `json:"entity_type"`
	Summary           string                `json:"summary"`
	SemanticHints     []string              `json:"semantic_hints"`
	PossibleQuestions []string              `json:"possible_questions"`
}

// Extract sends the document body to the model and returns enriched fields.
// entityTypes and predicates constrain the extraction. If nil, defaults are used.
func (m *ModelExtractor) Extract(ctx context.Context, body string, entityTypes, predicates []string) (*ModelResult, error) {
	if len(entityTypes) == 0 {
		entityTypes = DefaultEntityTypes
	}
	if len(predicates) == 0 {
		predicates = DefaultPredicates
	}

	if m.cfg.Format == FormatTriplex {
		userMsg, err := buildTriplexMessage(entityTypes, predicates, body)
		if err != nil {
			return nil, err
		}
		messages := []llm.Message{
			{Role: "user", Content: userMsg},
		}
		content, err := m.llmClient.ChatCompletion(ctx, messages)
		if err != nil {
			return nil, err
		}
		return parseTriplexOutput(content)
	}

	systemPrompt := buildSystemPrompt(entityTypes, predicates)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: body},
	}

	content, err := m.llmClient.ChatCompletion(ctx, messages)
	if err != nil {
		return nil, err
	}
	return parseModelOutput(content)
}

func buildSystemPrompt(entityTypes, predicates []string) string {
	return fmt.Sprintf(`You are a knowledge graph extraction assistant. Extract structured knowledge from the provided text.

Available entity types: %s
Available relationship predicates: %s

Respond with ONLY a JSON object in the following format (no markdown fences, no explanation):
{
  "entities": [{"name": "...", "role": "...", "aliases": ["..."]}],
  "relationships": [{"target": "...", "type": "...", "strength": 0.0}],
  "entity_type": "...",
  "semantic_hints": ["..."],
  "possible_questions": ["..."]
}

Rules:
- entity_type: classify the document as one of the available entity types (lowercase)
- relationships: use target as a slug (lowercase, hyphens), type from available predicates, strength 0.0-1.0
- entities: extract key named entities mentioned in the text
- semantic_hints: 2-5 short phrases describing what this document is about
- possible_questions: 2-5 questions this document could answer`,
		strings.Join(entityTypes, ", "),
		strings.Join(predicates, ", "))
}

// parseModelOutput extracts a ModelResult from the model's text response.
func parseModelOutput(content string) (*ModelResult, error) {
	// Strip markdown code fences if present.
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		// Remove first and last lines (fences).
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	content = strings.TrimSpace(content)

	var result ModelResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("enrich: parse model output: %w\nraw output: %s", err, truncate(content, 500))
	}

	return &result, nil
}

// buildTriplexMessage constructs the user message for the Triplex NER fine-tune.
// The preamble text is a trigger for the fine-tuned weights and must match exactly.
func buildTriplexMessage(entityTypes, predicates []string, body string) (string, error) {
	entityTypesObj := map[string][]string{"entity_types": entityTypes}
	predicatesObj := map[string][]string{"predicates": predicates}

	etJSON, err := json.Marshal(entityTypesObj)
	if err != nil {
		return "", fmt.Errorf("enrich: build triplex message: marshal entity types: %w", err)
	}
	predJSON, err := json.Marshal(predicatesObj)
	if err != nil {
		return "", fmt.Errorf("enrich: build triplex message: marshal predicates: %w", err)
	}

	msg := fmt.Sprintf(`Perform Named Entity Recognition (NER) and extract knowledge graph triplets from the text. NER identifies named entities of given entity types, and triple extraction identifies relationships between entities using specified predicates.

**Entity Types:**
%s

**Predicates:**
%s

**Text:**
%s`, string(etJSON), string(predJSON), body)

	return msg, nil
}

// parseTriplexOutput parses the entities_and_triples output format from Triplex.
func parseTriplexOutput(content string) (*ModelResult, error) {
	// Strip markdown code fences if present.
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	content = strings.TrimSpace(content)

	var raw struct {
		EntitiesAndTriples []string `json:"entities_and_triples"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse triplex output: %w", err)
	}

	var result ModelResult
	entityIndex := make(map[string]string) // index → entity name
	entityTypeCounts := make(map[string]int)

	// First pass: collect entities (items starting with "[").
	for _, item := range raw.EntitiesAndTriples {
		if !strings.HasPrefix(item, "[") {
			continue
		}
		// Format: "[1], PERSON:Alice Chen"
		parts := strings.SplitN(item, "], ", 2)
		if len(parts) != 2 {
			continue
		}
		idx := strings.TrimPrefix(parts[0], "[")
		rest := parts[1]

		nameParts := strings.SplitN(rest, ":", 2)
		if len(nameParts) != 2 {
			continue
		}
		entityType := strings.TrimSpace(nameParts[0])
		name := strings.TrimSpace(nameParts[1])

		entityIndex[idx] = name
		entityTypeCounts[entityType]++
		result.Entities = append(result.Entities, schema.Entity{Name: name, Role: entityType})
	}

	// Second pass: collect relationships (items starting with "(").
	for _, item := range raw.EntitiesAndTriples {
		if !strings.HasPrefix(item, "(") {
			continue
		}
		// Format: "(1, WORKS_FOR, 2)"
		stripped := strings.TrimPrefix(item, "(")
		stripped = strings.TrimSuffix(stripped, ")")
		tripParts := strings.Split(stripped, ", ")
		if len(tripParts) != 3 {
			continue
		}
		subjectIdx := strings.TrimSpace(tripParts[0])
		predicate := strings.TrimSpace(tripParts[1])
		objectIdx := strings.TrimSpace(tripParts[2])

		_, subjectOK := entityIndex[subjectIdx]
		targetName, objectOK := entityIndex[objectIdx]
		if !subjectOK || !objectOK {
			continue
		}

		result.Relationships = append(result.Relationships, schema.Relationship{
			Target:   targetName,
			Type:     predicate,
			Strength: 0.8,
		})
	}

	// Set EntityType to the most-frequent entity type among parsed entities.
	bestType := ""
	bestCount := 0
	for et, count := range entityTypeCounts {
		if count > bestCount || (count == bestCount && bestType == "") {
			bestType = et
			bestCount = count
		}
	}
	result.EntityType = strings.ToLower(bestType)

	return &result, nil
}

// GenerateSummary calls the model to produce a one-sentence entity description.
// title, entityType, and tags provide context; bodyPreview is the first ~500 tokens of body.
func (m *ModelExtractor) GenerateSummary(ctx context.Context, title, entityType string, tags []string, bodyPreview string) (string, error) {
	tagsStr := "(none)"
	if len(tags) > 0 {
		tagsStr = strings.Join(tags, ", ")
	}

	prompt := fmt.Sprintf(`Generate a one-sentence summary describing what this page represents.
Focus on the entity/concept itself, not the document structure.

Title: %s
Entity type: %s
Tags: %s
Body (first 500 tokens): %s

Reply with ONLY the summary sentence, no quotes or formatting.`, title, entityType, tagsStr, bodyPreview)

	messages := []llm.Message{
		{Role: "system", Content: "You are a concise knowledge graph summarizer. Generate one-sentence entity descriptions."},
		{Role: "user", Content: prompt},
	}

	content, err := m.llmClient.ChatCompletion(ctx, messages)
	if err != nil {
		// Wrap with summary-specific context to preserve existing error message patterns.
		return "", fmt.Errorf("enrich: summary %w", err)
	}

	summary := strings.TrimSpace(content)
	// Strip surrounding quotes if the model wrapped it.
	summary = strings.Trim(summary, "\"'")
	return summary, nil
}

// BodyPreview returns the first maxTokens approximate tokens of a body string.
// Uses a simple word-based approximation (not exact tokenization).
func BodyPreview(body string, maxTokens int) string {
	words := strings.Fields(body)
	if len(words) <= maxTokens {
		return body
	}
	return strings.Join(words[:maxTokens], " ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// MergeModelResult merges model extraction results into existing frontmatter.
// Same merge semantics as MergeFrontmatter: default fills missing, force overwrites.
func MergeModelResult(existing schema.GraphFrontmatter, model *ModelResult, opts MergeOptions) schema.GraphFrontmatter {
	result := existing

	if opts.Force {
		if model.EntityType != "" {
			result.EntityType = strings.ToLower(model.EntityType)
		}
		if model.Summary != "" {
			result.Summary = model.Summary
		}
		if len(model.Entities) > 0 {
			result.Entities = model.Entities
		}
		if len(model.Relationships) > 0 {
			result.Relationships = model.Relationships
		}
		if len(model.SemanticHints) > 0 {
			result.SemanticHints = model.SemanticHints
		}
		if len(model.PossibleQuestions) > 0 {
			result.PossibleQuestions = model.PossibleQuestions
		}
		return result
	}

	// Default: fill missing, union arrays.
	if result.EntityType == "" && model.EntityType != "" {
		result.EntityType = strings.ToLower(model.EntityType)
	}
	if result.Summary == "" && model.Summary != "" {
		result.Summary = model.Summary
	}

	// Union entities by name.
	if len(model.Entities) > 0 {
		seen := make(map[string]bool)
		for _, e := range result.Entities {
			seen[strings.ToLower(e.Name)] = true
		}
		for _, e := range model.Entities {
			if !seen[strings.ToLower(e.Name)] {
				seen[strings.ToLower(e.Name)] = true
				result.Entities = append(result.Entities, e)
			}
		}
	}

	// Union relationships by target.
	result.Relationships = unionRelationships(result.Relationships, model.Relationships)

	// Union semantic hints.
	result.SemanticHints = unionStrings(result.SemanticHints, model.SemanticHints)

	// Union possible questions.
	result.PossibleQuestions = unionStrings(result.PossibleQuestions, model.PossibleQuestions)

	return result
}
