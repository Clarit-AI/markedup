package embed

import "testing"

func TestNewFromProvider_BuildsEquivalentEmbedder(t *testing.T) {
	e := NewFromProvider("http://localhost:11434", "nomic-embed-text", "sk-test", 768)

	if e == nil {
		t.Fatal("NewFromProvider returned nil")
	}
	if e.Model() != "nomic-embed-text" {
		t.Errorf("expected Model=nomic-embed-text, got %q", e.Model())
	}
	if e.Dimensions() != 768 {
		t.Errorf("expected Dimensions=768, got %d", e.Dimensions())
	}
}

func TestNewFromProvider_EmptyAPIKey(t *testing.T) {
	// Local endpoints often don't need a key.
	e := NewFromProvider("http://localhost:11434", "model", "", 384)
	if e == nil {
		t.Fatal("NewFromProvider returned nil with empty APIKey")
	}
	if e.Dimensions() != 384 {
		t.Errorf("expected Dimensions=384, got %d", e.Dimensions())
	}
}

func TestNewFromProvider_TrimsTrailingSlash(t *testing.T) {
	// Verifies the convenience constructor delegates to NewOpenAICompatible
	// which trims the trailing slash on Endpoint.
	e := NewFromProvider("http://localhost:11434/", "m", "", 0)
	if e.endpoint != "http://localhost:11434" {
		t.Errorf("expected endpoint trimmed, got %q", e.endpoint)
	}
}
