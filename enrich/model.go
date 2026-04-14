package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/KHAEntertainment/markedup/schema"
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
}

// ModelExtractor calls a chat-completion API to extract structured knowledge.
type ModelExtractor struct {
	cfg    ModelConfig
	client *http.Client
}

// NewModelExtractor creates a new model extractor with the given config.
func NewModelExtractor(cfg ModelConfig) *ModelExtractor {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &ModelExtractor{cfg: cfg, client: client}
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

	systemPrompt := buildSystemPrompt(entityTypes, predicates)

	reqBody := chatRequest{
		Model: m.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: body},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("enrich: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(m.cfg.Endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("enrich: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if m.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enrich: model request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("enrich: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrich: model returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("enrich: unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("enrich: model returned no choices")
	}

	content := chatResp.Choices[0].Message.Content
	return parseModelOutput(content)
}

// chatRequest is the OpenAI-compatible chat completion request.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
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

	reqBody := chatRequest{
		Model: m.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a concise knowledge graph summarizer. Generate one-sentence entity descriptions."},
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("enrich: marshal summary request: %w", err)
	}

	endpoint := strings.TrimRight(m.cfg.Endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("enrich: create summary request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if m.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("enrich: summary request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("enrich: read summary response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enrich: summary model returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("enrich: unmarshal summary response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("enrich: summary model returned no choices")
	}

	summary := strings.TrimSpace(chatResp.Choices[0].Message.Content)
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
