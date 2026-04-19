package schema

import "testing"

func TestIsValidEntityType(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Whitelisted values
		{"document", true},
		{"person", true},
		{"project", true},
		{"concept", true},
		{"organization", true},
		{"software", true},
		{"event", true},
		{"place", true},
		{"tool", true},
		{"paper", true},

		// Case-insensitivity
		{"Concept", true},
		{"SOFTWARE", true},

		// Rejections
		{"", false},
		{"date", false},
		{"location", false}, // observed NuExtract hallucination
		{"random", false},
		{"documents", false}, // no fuzzy matching
	}
	for _, tc := range tests {
		if got := IsValidEntityType(tc.in); got != tc.want {
			t.Errorf("IsValidEntityType(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidEntityTypes_NonEmpty(t *testing.T) {
	if len(ValidEntityTypes) == 0 {
		t.Fatal("ValidEntityTypes must not be empty")
	}
	// "document" is required as the default fallback used in MergeModelResult
	// default-upgrade logic.
	found := false
	for _, v := range ValidEntityTypes {
		if v == "document" {
			found = true
			break
		}
	}
	if !found {
		t.Error(`ValidEntityTypes must contain "document" (the default fallback)`)
	}
}
