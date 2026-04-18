package index

import (
	"testing"

	"github.com/Clarit-AI/markedup/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traverseTestPages returns a small graph:
//
//	alice --related-to--> bob
//	alice --describes-->  concept-graph
//	bob   --related-to--> concept-graph
//	bob   --depends-on--> alice  (creates a cycle)
func traverseTestPages() []*schema.Page {
	return []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "alice",
				Title:      "Alice",
				EntityType: "person",
				Confidence: 0.9,
				Tags:       []string{"people"},
				Relationships: []schema.Relationship{
					{Target: "bob", Type: "related-to", Strength: 0.8},
					{Target: "concept-graph", Type: "describes", Strength: 0.7},
				},
			},
			Body: "Alice body",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "bob",
				Title:      "Bob",
				EntityType: "person",
				Confidence: 0.85,
				Tags:       []string{"people"},
				Relationships: []schema.Relationship{
					{Target: "concept-graph", Type: "related-to", Strength: 0.6},
					{Target: "alice", Type: "depends-on", Strength: 0.5},
				},
			},
			Body: "Bob body",
		},
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:         "concept-graph",
				Title:      "Concept Graph",
				EntityType: "concept",
				Confidence: 0.95,
				Tags:       []string{"concept"},
			},
			Body: "Concept graph body",
		},
	}
}

func buildTraverseIndex(pages []*schema.Page) *KnowledgeIndex {
	return buildIndex(pages)
}

func nodeIDs(nodes []TraversalNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.Page.Frontmatter.ID
	}
	return ids
}

func TestTraverse(t *testing.T) {
	tests := []struct {
		name          string
		pages         []*schema.Page
		from          string
		opts          []TraverseOption
		wantErr       string
		wantNodeIDs   []string // expected node IDs (order may vary for some)
		wantRootDepth int      // expected max depth in result
		checkExact    bool     // if true, node order must match exactly
	}{
		{
			name:  "BFS from alice depth 1",
			pages: traverseTestPages(),
			from:  "alice",
			opts:  []TraverseOption{WithDepth(1)},
			wantNodeIDs: []string{
				"alice", "bob", "concept-graph",
			},
			wantRootDepth: 1,
			checkExact:    true, // BFS: alice (0), then bob, concept-graph (1)
		},
		{
			name:  "DFS from alice depth 1",
			pages: traverseTestPages(),
			from:  "alice",
			opts:  []TraverseOption{WithDepth(1), WithStrategy(DFS)},
			// Nodes are added at discovery time (during neighbor scan), so
			// order matches relationship declaration order: bob, concept-graph.
			// DFS vs BFS difference manifests at deeper levels.
			wantNodeIDs:   []string{"alice", "bob", "concept-graph"},
			wantRootDepth: 1,
		},
		{
			name:  "BFS from alice default depth 2",
			pages: traverseTestPages(),
			from:  "alice",
			// default depth=2, strategy=BFS, direction=Forward
			wantNodeIDs:   []string{"alice", "bob", "concept-graph"},
			wantRootDepth: 1, // all reachable at depth 1
		},
		{
			name:          "depth 0 returns only root",
			pages:         traverseTestPages(),
			from:          "alice",
			opts:          []TraverseOption{WithDepth(0)},
			wantNodeIDs:   []string{"alice"},
			wantRootDepth: 0,
			checkExact:    true,
		},
		{
			name:  "cycle detection A→B→A",
			pages: traverseTestPages(),
			from:  "alice",
			opts:  []TraverseOption{WithDepth(10)}, // deep enough to loop if no cycle detection
			// alice→bob, alice→concept-graph, bob→concept-graph, bob→alice(visited)
			wantNodeIDs:   []string{"alice", "bob", "concept-graph"},
			wantRootDepth: 1,
		},
		{
			name:  "reverse traversal from concept-graph",
			pages: traverseTestPages(),
			from:  "concept-graph",
			opts:  []TraverseOption{WithDirection(Reverse), WithDepth(1)},
			// concept-graph is targeted by alice and bob
			wantNodeIDs:   []string{"concept-graph", "alice", "bob"},
			wantRootDepth: 1,
		},
		{
			name:  "both direction from bob depth 1",
			pages: traverseTestPages(),
			from:  "bob",
			opts:  []TraverseOption{WithDirection(Both), WithDepth(1)},
			// Forward: bob→concept-graph, bob→alice
			// Reverse: bob is targeted by alice (already visited via forward)
			wantNodeIDs:   []string{"bob", "concept-graph", "alice"},
			wantRootDepth: 1,
		},
		{
			name:  "WithRelationshipTypes filtering",
			pages: traverseTestPages(),
			from:  "alice",
			opts:  []TraverseOption{WithRelationshipTypes([]string{"describes"}), WithDepth(2)},
			// alice --describes--> concept-graph only
			wantNodeIDs:   []string{"alice", "concept-graph"},
			wantRootDepth: 1,
			checkExact:    true,
		},
		{
			name:    "non-existent from ID",
			pages:   traverseTestPages(),
			from:    "nonexistent",
			wantErr: `traverse: node "nonexistent" not found in index`,
		},
		{
			name:    "empty graph",
			pages:   nil,
			from:    "anything",
			wantErr: `traverse: node "anything" not found in index`,
		},
		{
			name:  "reverse traversal filters by relationship type",
			pages: traverseTestPages(),
			from:  "alice",
			opts: []TraverseOption{
				WithDirection(Reverse),
				WithRelationshipTypes([]string{"depends-on"}),
				WithDepth(1),
			},
			// alice is targeted by bob with "depends-on"
			wantNodeIDs:   []string{"alice", "bob"},
			wantRootDepth: 1,
			checkExact:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := buildTraverseIndex(tt.pages)
			result, err := Traverse(idx, tt.from, tt.opts...)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.from, result.Root)
			assert.Equal(t, tt.wantRootDepth, result.Depth)

			gotIDs := nodeIDs(result.Nodes)
			if tt.checkExact {
				assert.Equal(t, tt.wantNodeIDs, gotIDs)
			} else {
				assert.ElementsMatch(t, tt.wantNodeIDs, gotIDs)
			}

			// Root is always first and at depth 0.
			require.NotEmpty(t, result.Nodes)
			assert.Equal(t, tt.from, result.Nodes[0].Page.Frontmatter.ID)
			assert.Equal(t, 0, result.Nodes[0].Depth)
		})
	}
}

func TestTraverse_EdgesRecorded(t *testing.T) {
	idx := buildTraverseIndex(traverseTestPages())
	result, err := Traverse(idx, "alice", WithDepth(1))
	require.NoError(t, err)

	// alice has 2 forward relationships: bob and concept-graph.
	require.Len(t, result.Edges, 2)

	edgeTargets := make([]string, len(result.Edges))
	for i, e := range result.Edges {
		assert.Equal(t, "alice", e.From)
		edgeTargets[i] = e.To
	}
	assert.ElementsMatch(t, []string{"bob", "concept-graph"}, edgeTargets)
}

func TestTraverse_CycleEdgesStillRecorded(t *testing.T) {
	idx := buildTraverseIndex(traverseTestPages())
	result, err := Traverse(idx, "alice", WithDepth(2))
	require.NoError(t, err)

	// Edges should include the back-edge bob→alice even though alice is visited.
	var foundBackEdge bool
	for _, e := range result.Edges {
		if e.From == "bob" && e.To == "alice" {
			foundBackEdge = true
		}
	}
	assert.True(t, foundBackEdge, "expected back-edge bob→alice to be recorded")
}

func TestTraverse_SingleNodeNoRelationships(t *testing.T) {
	pages := []*schema.Page{
		{
			Frontmatter: schema.GraphFrontmatter{
				ID:    "lonely",
				Title: "Lonely Node",
			},
		},
	}
	idx := buildTraverseIndex(pages)
	result, err := Traverse(idx, "lonely")
	require.NoError(t, err)
	assert.Len(t, result.Nodes, 1)
	assert.Equal(t, "lonely", result.Nodes[0].Page.Frontmatter.ID)
	assert.Empty(t, result.Edges)
	assert.Equal(t, 0, result.Depth)
}
