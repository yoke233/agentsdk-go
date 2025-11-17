package workflow

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Graph holds nodes and transitions for a workflow.
type Graph struct {
	mu     sync.RWMutex
	start  string
	nodes  map[string]Node
	edges  map[string][]Transition
	closed bool
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]Node),
		edges: make(map[string][]Transition),
	}
}

// AddNode registers a node. The first node added becomes the start node unless SetStart is called.
func (g *Graph) AddNode(n Node) error {
	if n == nil {
		return errors.New("node is nil")
	}
	name := strings.TrimSpace(n.Name())
	if name == "" {
		return errors.New("node name is empty")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("graph is closed")
	}
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("node %q already exists", name)
	}
	g.nodes[name] = n
	if g.start == "" {
		g.start = name
	}
	return nil
}

// SetStart selects the entry node.
func (g *Graph) SetStart(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("graph is closed")
	}
	if _, ok := g.nodes[name]; !ok {
		return fmt.Errorf("start node %q not found", name)
	}
	g.start = name
	return nil
}

// AddTransition connects two nodes with an optional condition.
func (g *Graph) AddTransition(from, to string, cond Condition) error {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return errors.New("transition endpoints cannot be empty")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("graph is closed")
	}
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("from node %q not registered", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("to node %q not registered", to)
	}
	g.edges[from] = append(g.edges[from], Transition{
		From:      from,
		To:        to,
		Condition: cond,
	})
	return nil
}

// Start returns the configured entry node.
func (g *Graph) Start() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.start
}

// Node retrieves a node by name.
func (g *Graph) Node(name string) (Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[name]
	return n, ok
}

// transitions returns outgoing transitions for a node.
func (g *Graph) transitions(from string) []Transition {
	g.mu.RLock()
	defer g.mu.RUnlock()
	list := g.edges[from]
	out := make([]Transition, len(list))
	copy(out, list)
	return out
}

// Validate ensures the graph has a start node and no missing edges.
func (g *Graph) Validate() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.start == "" {
		return errors.New("start node is not set")
	}
	for from, edges := range g.edges {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("edge references unknown from node %q", from)
		}
		for _, e := range edges {
			if _, ok := g.nodes[e.To]; !ok {
				return fmt.Errorf("edge from %q targets unknown node %q", from, e.To)
			}
		}
	}
	return nil
}

// Close prevents further mutation. Useful to freeze a graph after build.
func (g *Graph) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
}
