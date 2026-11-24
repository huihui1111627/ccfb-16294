package zupu

import "fmt"

type GraphNode struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Subtitle string `json:"subtitle,omitempty"`
	Kind     string `json:"kind"` // rule, base
	Layer    int    `json:"layer"`
	RuleID   string `json:"ruleId,omitempty"`
}

type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind"` // positive, negated, aggregate
}

type Graph struct {
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
	Layers [][]string  `json:"layers"`
}

func (e *Engine) RuleGraph() *Graph {
	g := &Graph{Layers: e.Layers}
	seenNodes := map[string]bool{}
	addNode := func(n GraphNode) {
		if !seenNodes[n.ID] {
			seenNodes[n.ID] = true
			g.Nodes = append(g.Nodes, n)
		}
	}
	edgeSeen := map[string]bool{}
	addEdge := func(edge GraphEdge) {
		key := edge.From + "|" + edge.To + "|" + edge.Kind + "|" + edge.Label
		if edgeSeen[key] {
			return
		}
		edgeSeen[key] = true
		g.Edges = append(g.Edges, edge)
	}

	for _, cr := range e.Rules {
		addNode(GraphNode{
			ID: cr.ID, Kind: "rule", Layer: cr.Stats.Layer, RuleID: cr.ID,
			Label:    fmt.Sprintf("%s · %s/%d", cr.ID, cr.HeadPred, len(cr.Rule.Head.Args)),
			Subtitle: cr.Text(),
		})
	}
	for _, cr := range e.Rules {
		for _, dep := range ruleDependencies(cr.Rule, cr.ID) {
			producers := e.ByHead[dep.From]
			kind := dep.Kind
			label := dep.From
			if len(producers) == 0 {
				id := "base:" + dep.From
				addNode(GraphNode{
					ID: id, Kind: "base", Layer: 0,
					Label: dep.From, Subtitle: "基础事实 / EDB",
				})
				addEdge(GraphEdge{From: id, To: cr.ID, Label: label, Kind: kind})
				continue
			}
			for _, p := range producers {
				addEdge(GraphEdge{From: p.ID, To: cr.ID, Label: label, Kind: kind})
			}
		}
	}
	return g
}
