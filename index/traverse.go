package index

import (
	"fmt"

	"github.com/Clarit-AI/markedup/schema"
)

// TraversalStrategy selects between breadth-first and depth-first traversal.
type TraversalStrategy int

const (
	// BFS performs a breadth-first traversal (default).
	BFS TraversalStrategy = iota
	// DFS performs a depth-first traversal.
	DFS
)

// TraversalDirection controls which adjacency edges are followed.
type TraversalDirection int

const (
	// Forward follows outgoing relationships (adjacency map).
	Forward TraversalDirection = iota
	// Reverse follows incoming relationships (reverse adjacency map).
	Reverse
	// Both merges forward and reverse neighbors.
	Both
)

// TraversalNode pairs a page with the depth at which it was discovered.
type TraversalNode struct {
	Page  *schema.Page
	Depth int
}

// TraversalEdge represents a directed edge discovered during traversal.
type TraversalEdge struct {
	From         string
	To           string
	Relationship schema.Relationship
}

// TraversalResult holds the output of a graph traversal.
type TraversalResult struct {
	Root  string
	Nodes []TraversalNode
	Edges []TraversalEdge
	Depth int
}

// TraverseOption configures a Traverse call.
type TraverseOption func(*traverseConfig)

type traverseConfig struct {
	maxDepth          int
	strategy          TraversalStrategy
	direction         TraversalDirection
	relationshipTypes []string // nil = all types
}

// WithDepth sets the maximum traversal depth. Default is 2.
func WithDepth(n int) TraverseOption {
	return func(c *traverseConfig) {
		c.maxDepth = n
	}
}

// WithStrategy selects BFS or DFS. Default is BFS.
func WithStrategy(s TraversalStrategy) TraverseOption {
	return func(c *traverseConfig) {
		c.strategy = s
	}
}

// WithDirection sets the traversal direction. Default is Forward.
func WithDirection(d TraversalDirection) TraverseOption {
	return func(c *traverseConfig) {
		c.direction = d
	}
}

// WithRelationshipTypes restricts traversal to edges whose Type is in the
// provided list. Passing nil or an empty slice follows all relationship types.
func WithRelationshipTypes(types []string) TraverseOption {
	return func(c *traverseConfig) {
		c.relationshipTypes = types
	}
}

// queueEntry is used internally for BFS/DFS traversal.
type queueEntry struct {
	id    string
	depth int
}

// Traverse performs an iterative graph traversal starting from the node with
// the given ID. It is cycle-safe: a visited set prevents revisiting nodes.
// Returns an error if the starting node is not found in the index.
func Traverse(idx *KnowledgeIndex, from string, opts ...TraverseOption) (*TraversalResult, error) {
	cfg := traverseConfig{
		maxDepth:  2,
		strategy:  BFS,
		direction: Forward,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Verify the starting node exists.
	rootPage, ok := idx.Get(from)
	if !ok {
		return nil, fmt.Errorf("traverse: node %q not found in index", from)
	}

	// Build the relationship type filter set.
	var typeFilter map[string]struct{}
	if len(cfg.relationshipTypes) > 0 {
		typeFilter = make(map[string]struct{}, len(cfg.relationshipTypes))
		for _, t := range cfg.relationshipTypes {
			typeFilter[t] = struct{}{}
		}
	}

	visited := map[string]bool{from: true}
	result := &TraversalResult{
		Root:  from,
		Nodes: []TraversalNode{{Page: rootPage, Depth: 0}},
	}

	// stack/queue — append to end; BFS pops from front, DFS pops from back.
	work := []queueEntry{{id: from, depth: 0}}

	for len(work) > 0 {
		var current queueEntry
		if cfg.strategy == BFS {
			current = work[0]
			work = work[1:]
		} else {
			current = work[len(work)-1]
			work = work[:len(work)-1]
		}

		if current.depth >= cfg.maxDepth {
			continue
		}

		// Collect neighbors.
		neighbors := neighborsFor(idx, current.id, cfg.direction, typeFilter)

		for _, n := range neighbors {
			// Record the edge regardless of visited status.
			result.Edges = append(result.Edges, TraversalEdge{
				From:         n.edgeFrom,
				To:           n.edgeTo,
				Relationship: n.rel,
			})

			targetID := n.visitID
			if visited[targetID] {
				continue
			}
			visited[targetID] = true

			targetPage, exists := idx.Get(targetID)
			if !exists {
				// Dangling reference — skip but edge is still recorded.
				continue
			}

			nextDepth := current.depth + 1
			result.Nodes = append(result.Nodes, TraversalNode{
				Page:  targetPage,
				Depth: nextDepth,
			})
			if nextDepth > result.Depth {
				result.Depth = nextDepth
			}

			work = append(work, queueEntry{id: targetID, depth: nextDepth})
		}
	}

	return result, nil
}

// neighborInfo bundles an edge discovered during traversal.
// visitID is the node to add to the work queue (the "next hop").
type neighborInfo struct {
	edgeFrom string              // edge source in the original graph
	edgeTo   string              // edge target in the original graph
	visitID  string              // the node we are moving to
	rel      schema.Relationship // the relationship on this edge
}

// neighborsFor returns the neighbors reachable from nodeID in the given
// direction, filtered by relationship type if typeFilter is non-nil.
func neighborsFor(idx *KnowledgeIndex, nodeID string, dir TraversalDirection, typeFilter map[string]struct{}) []neighborInfo {
	var neighbors []neighborInfo

	if dir == Forward || dir == Both {
		for _, rel := range idx.ForwardRels(nodeID) {
			if typeFilter != nil {
				if _, ok := typeFilter[rel.Type]; !ok {
					continue
				}
			}
			neighbors = append(neighbors, neighborInfo{
				edgeFrom: nodeID,
				edgeTo:   rel.Target,
				visitID:  rel.Target,
				rel:      rel,
			})
		}
	}

	if dir == Reverse || dir == Both {
		for _, srcID := range idx.ReverseRefs(nodeID) {
			// Find the original relationship from srcID → nodeID.
			syntheticRel := schema.Relationship{
				Target:   nodeID,
				Type:     "reverse-ref",
				Strength: 0.0,
			}
			for _, rel := range idx.ForwardRels(srcID) {
				if rel.Target == nodeID {
					syntheticRel = rel
					break
				}
			}
			if typeFilter != nil {
				if _, ok := typeFilter[syntheticRel.Type]; !ok {
					continue
				}
			}
			neighbors = append(neighbors, neighborInfo{
				edgeFrom: srcID,
				edgeTo:   nodeID,
				visitID:  srcID, // traverse into the source node
				rel:      syntheticRel,
			})
		}
	}

	return neighbors
}
