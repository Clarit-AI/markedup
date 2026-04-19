package enrich

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepairNuExtractJSON_ValidUnchanged is the core regression guarantee:
// a well-formed NuExtract payload must pass through the repair pre-pass
// byte-for-byte. If this breaks, the repairs are too aggressive.
func TestRepairNuExtractJSON_ValidUnchanged(t *testing.T) {
	inputs := []string{
		`{"entities":[{"name":"Alice","type":"PERSON"}]}`,
		`{"entities":[{"name":"Alice","type":"PERSON"}],"relationships":[{"subject":"Alice","predicate":"works_for","object":"Acme"}]}`,
		`{"entities":[]}`,
		`{"relationships":[]}`,
		`{"entities":[{"name":"LM Studio","type":["TECHNOLOGY"]}]}`,
		`{"entities":[{"name":"Dr. }]{ Chen","type":"PERSON"}]}`, // braces inside a string value — must not fool the repairer.
	}
	for _, in := range inputs {
		got := repairNuExtractJSON([]byte(in))
		assert.Equal(t, in, string(got), "input must be unchanged: %s", in)
		// And it must still parse as valid JSON.
		var v any
		require.NoError(t, json.Unmarshal(got, &v), "output must remain valid JSON: %s", string(got))
	}
}

// Pattern A — two top-level NuExtract objects joined by ", ".
func TestRepairNuExtractJSON_PatternA_ConcatenatedObjects(t *testing.T) {
	bad := `{"entities":[{"name":"LM Studio","type":["TECHNOLOGY"]}]}, {"relationships":[{"object":null,"predicate":null,"subject":"lmstudio"}]}`
	got := repairNuExtractJSON([]byte(bad))

	var parsed struct {
		Entities      []nuextractEntity   `json:"entities"`
		Relationships []nuextractRelation `json:"relationships"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed),
		"repaired payload must be valid JSON: %s", string(got))
	require.Len(t, parsed.Entities, 1)
	assert.Equal(t, "LM Studio", parsed.Entities[0].Name)
	assert.Equal(t, "TECHNOLOGY", string(parsed.Entities[0].Type))
	require.Len(t, parsed.Relationships, 1)
	assert.Equal(t, "lmstudio", parsed.Relationships[0].Subject)
}

// Pattern B — missing colon after key; closing quote present.
func TestRepairNuExtractJSON_PatternB_MissingColon(t *testing.T) {
	bad := `{"entities" [{"name":"Markedup", "type":null}, {"name":"PageIndex", "type": null}]}`
	got := repairNuExtractJSON([]byte(bad))

	var parsed struct {
		Entities []nuextractEntity `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed),
		"repaired payload must be valid JSON: %s", string(got))
	require.Len(t, parsed.Entities, 2)
	assert.Equal(t, "Markedup", parsed.Entities[0].Name)
	assert.Equal(t, "PageIndex", parsed.Entities[1].Name)
}

// Pattern C — key lost its closing quote AND its colon.
func TestRepairNuExtractJSON_PatternC_MissingClosingQuote(t *testing.T) {
	bad := `{"entities [{"name":"Foo","type":"CONCEPT"}]}`
	got := repairNuExtractJSON([]byte(bad))

	var parsed struct {
		Entities []nuextractEntity `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed),
		"repaired payload must be valid JSON: %s", string(got))
	require.Len(t, parsed.Entities, 1)
	assert.Equal(t, "Foo", parsed.Entities[0].Name)
}

// Pattern D — bracket/brace mismatch inside array (IRL payload #1).
func TestRepairNuExtractJSON_PatternD_BracketBraceMismatch(t *testing.T) {
	bad := `{"entities":[{"name":"markedup", "type":["TECHNOLOGY"]}, {"name":"Go Library Usage Guide", "type":["CONCEPT"}]}`
	got := repairNuExtractJSON([]byte(bad))

	var parsed struct {
		Entities []nuextractEntity `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed),
		"repaired payload must be valid JSON: %s", string(got))
	require.Len(t, parsed.Entities, 2)
	assert.Equal(t, "markedup", parsed.Entities[0].Name)
	assert.Equal(t, "TECHNOLOGY", string(parsed.Entities[0].Type))
	assert.Equal(t, "Go Library Usage Guide", parsed.Entities[1].Name)
	assert.Equal(t, "CONCEPT", string(parsed.Entities[1].Type))
}

// Pattern E — truncated trailing close (IRL payload #2).
func TestRepairNuExtractJSON_PatternE_TruncatedTrailingBrace(t *testing.T) {
	bad := `{"entities":[{"name":"Alice Chen","type":"person","confidence":0.95,"alias":["alice", "Dr. Chen"]}, {"name":"bob","type":"person","confidence":1.0,"alias":["colleague"]}, {"name":"concept-graph","type":"project","confidence":1.0,"alias":["studies"]}, {"name":"system", "type":"person", "confidence":1.0}]`
	got := repairNuExtractJSON([]byte(bad))

	var parsed struct {
		Entities []nuextractEntity `json:"entities"`
	}
	require.NoError(t, json.Unmarshal(got, &parsed),
		"repaired payload must be valid JSON: %s", string(got))
	require.Len(t, parsed.Entities, 4)
	assert.Equal(t, "Alice Chen", parsed.Entities[0].Name)
	assert.Equal(t, "system", parsed.Entities[3].Name)
}

// End-to-end: parseNuExtractEntities must recover entities from each
// malformed payload rather than erroring out. Covers the real caller path
// including stripJSONFences + repair + Unmarshal.
func TestParseNuExtractEntities_ToleratesAllPatterns(t *testing.T) {
	cases := map[string]struct {
		raw       string
		wantNames []string
	}{
		"patternA": {
			raw:       `{"entities":[{"name":"LM Studio","type":["TECHNOLOGY"]}]}, {"relationships":[{"object":null,"predicate":null,"subject":"lmstudio"}]}`,
			wantNames: []string{"LM Studio"},
		},
		"patternB": {
			raw:       `{"entities" [{"name":"Markedup", "type":null}, {"name":"PageIndex", "type": null}]}`,
			wantNames: []string{"Markedup", "PageIndex"},
		},
		"patternC": {
			raw:       `{"entities [{"name":"Foo","type":"CONCEPT"}]}`,
			wantNames: []string{"Foo"},
		},
		"patternD_IRL1": {
			raw:       `{"entities":[{"name":"markedup", "type":["TECHNOLOGY"]}, {"name":"Go Library Usage Guide", "type":["CONCEPT"}]}`,
			wantNames: []string{"markedup", "Go Library Usage Guide"},
		},
		"patternE_IRL2": {
			raw:       `{"entities":[{"name":"Alice Chen","type":"person","confidence":0.95,"alias":["alice", "Dr. Chen"]}, {"name":"bob","type":"person","confidence":1.0,"alias":["colleague"]}, {"name":"concept-graph","type":"project","confidence":1.0,"alias":["studies"]}, {"name":"system", "type":"person", "confidence":1.0}]`,
			wantNames: []string{"Alice Chen", "bob", "concept-graph", "system"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entities, _, err := parseNuExtractEntities(tc.raw)
			require.NoError(t, err, "raw=%s", tc.raw)
			gotNames := make([]string, 0, len(entities))
			for _, e := range entities {
				gotNames = append(gotNames, e.Name)
			}
			assert.Equal(t, tc.wantNames, gotNames)
		})
	}
}

// parseNuExtractCombined must also tolerate Pattern A end-to-end (the
// combined single-mode template is where Pattern A was first observed).
func TestParseNuExtractCombined_ToleratesPatternA(t *testing.T) {
	raw := `{"entities":[{"name":"LM Studio","type":["TECHNOLOGY"]}]}, {"relationships":[{"object":"x","predicate":"uses","subject":"lmstudio"}]}`
	entities, rels, _, err := parseNuExtractCombined(raw)
	require.NoError(t, err)
	require.Len(t, entities, 1)
	assert.Equal(t, "LM Studio", entities[0].Name)
	require.Len(t, rels, 1)
	assert.Equal(t, "x", rels[0].Target)
}

// Narrow unit tests for the sub-repairs: ensure they no-op on
// non-matching input.
func TestRepairSubpasses_NoOpOnUnrelatedInput(t *testing.T) {
	clean := []byte(`{"entities":[{"name":"A","type":"PERSON"}]}`)
	assert.Equal(t, string(clean), string(repairConcatenatedObjects(clean)))
	assert.Equal(t, string(clean), string(repairMissingColonBeforeArray(clean)))
	assert.Equal(t, string(clean), string(repairBracketBraceMismatch(clean)))
	assert.Equal(t, string(clean), string(repairTruncatedTrailingBrace(clean)))
}

// Pattern A guard: must NOT rewrite a payload where "}, {" appears inside
// a JSON string value (cannot happen at top level, but prove the depth
// tracker doesn't false-positive on string content).
func TestRepairConcatenatedObjects_IgnoresInnerStringContent(t *testing.T) {
	in := []byte(`{"entities":[{"name":"weird }, { name","type":"PERSON"}]}`)
	got := repairConcatenatedObjects(in)
	assert.Equal(t, string(in), string(got))
}

// Pattern E guard: do not append '}' when both balances are already 0.
func TestRepairTruncatedTrailingBrace_SkipsBalancedInput(t *testing.T) {
	in := []byte(`{"entities":[]}`)
	got := repairTruncatedTrailingBrace(in)
	assert.Equal(t, string(in), string(got))
}

// nuextractType must accept both string and array-of-string shapes, and
// collapse the array to its first non-empty element.
func TestNuextractType_AcceptsStringAndArray(t *testing.T) {
	var e1 nuextractEntity
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","type":"PERSON"}`), &e1))
	assert.Equal(t, "PERSON", string(e1.Type))

	var e2 nuextractEntity
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","type":["CONCEPT","OTHER"]}`), &e2))
	assert.Equal(t, "CONCEPT", string(e2.Type))

	var e3 nuextractEntity
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","type":null}`), &e3))
	assert.Equal(t, "", string(e3.Type))

	var e4 nuextractEntity
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x","type":[]}`), &e4))
	assert.Equal(t, "", string(e4.Type))
}
