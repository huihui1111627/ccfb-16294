package zupu

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type RuleStat struct {
	RuleID         string        `json:"ruleId"`
	HeadPredicate  string        `json:"headPredicate"`
	RuleText       string        `json:"ruleText"`
	Layer          int           `json:"layer"`
	Executions     int64         `json:"executions"`
	AtomEntries    int64         `json:"atomEntries"`
	CandidateJoins int64         `json:"candidateJoins"`
	MatchedJoins   int64         `json:"matchedJoins"`
	IndexLookups   int64         `json:"indexLookups"`
	FullScans      int64         `json:"fullScans"`
	AggregateRows  int64         `json:"aggregateRows"`
	Derivations    int64         `json:"derivations"`
	NewFacts       int64         `json:"newFacts"`
	Duration       time.Duration `json:"-"`
	DurationMS     float64       `json:"durationMs"`
}

type CompiledRule struct {
	ID           string
	Index        int
	Rule         Rule
	HeadPred     string
	Slots        int
	VarNames     []string
	Order        []int
	Positive     []int
	DeltaCapable []int
	AggIndex     int
	Stats        *RuleStat
}

func (r *CompiledRule) Text() string { return r.Rule.String() }

func compileProgram(prog *Program) (map[string]int, []*CompiledRule, map[string]int, [][]string, error) {
	schema := prog.Predicates()
	for name, arity := range schema {
		if arity < 0 {
			return nil, nil, nil, nil, fmt.Errorf("predicate %q is used with inconsistent arities", name)
		}
	}
	predLayer, layers, err := stratify(prog, schema)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	compiled := make([]*CompiledRule, 0, len(prog.Rules))
	for i := range prog.Rules {
		if err := validateRuleSchema(&prog.Rules[i], schema); err != nil {
			return nil, nil, nil, nil, err
		}
		cr, err := compileRule(&prog.Rules[i], i, predLayer)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		compiled = append(compiled, cr)
	}
	return schema, compiled, predLayer, layers, nil
}

func validateRuleSchema(r *Rule, schema map[string]int) error {
	checkAtom := func(a Atom) error {
		want, ok := schema[a.Pred]
		if !ok {
			return fmt.Errorf("line %d: unknown predicate %q", a.Line, a.Pred)
		}
		if want != len(a.Args) {
			return fmt.Errorf("line %d: predicate %q has arity %d but this atom uses %d", a.Line, a.Pred, want, len(a.Args))
		}
		return nil
	}
	if err := checkAtom(r.Head); err != nil {
		return err
	}
	for _, item := range r.Body {
		switch x := item.(type) {
		case *PositiveAtom:
			if err := checkAtom(x.Atom); err != nil {
				return err
			}
		case *NegatedAtom:
			if err := checkAtom(x.Atom); err != nil {
				return err
			}
		case *AggregateSpec:
			if err := checkAtom(x.Source); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileRule(r *Rule, index int, predLayer map[string]int) (*CompiledRule, error) {
	cr := &CompiledRule{
		ID:       fmt.Sprintf("R%d", index+1),
		Index:    index,
		Rule:     *r,
		HeadPred: r.Head.Pred,
		AggIndex: -1,
		Stats: &RuleStat{
			RuleID:        fmt.Sprintf("R%d", index+1),
			HeadPredicate: r.Head.Pred,
			RuleText:      r.String(),
			Layer:         predLayer[r.Head.Pred],
		},
	}
	names := map[string]int{}
	var walk func(t Term)
	walk = func(t Term) {
		switch x := t.(type) {
		case *Var:
			if idx, ok := names[x.Name]; ok {
				x.Index = idx
			} else {
				idx := len(cr.VarNames)
				names[x.Name] = idx
				x.Index = idx
				cr.VarNames = append(cr.VarNames, x.Name)
			}
		case *Expr:
			for _, a := range x.Args {
				walk(a)
			}
		}
	}
	for _, a := range r.Head.Args {
		walk(a)
	}
	for bi, item := range r.Body {
		switch x := item.(type) {
		case *PositiveAtom:
			for _, a := range x.Atom.Args {
				if _, ok := a.(*Expr); ok {
					return nil, fmt.Errorf("line %d: compute expressions with let before using them", x.Atom.Line)
				}
				walk(a)
			}
			cr.Positive = append(cr.Positive, bi)
		case *NegatedAtom:
			for _, a := range x.Atom.Args {
				if _, ok := a.(*Expr); ok {
					return nil, fmt.Errorf("line %d: compute expressions with let before using them", x.Atom.Line)
				}
				walk(a)
			}
		case *Comparison:
			walk(x.Left)
			walk(x.Right)
		case *Let:
			walk(x.Out)
			walk(x.Expr)
		case *AggregateSpec:
			if x.Input != nil {
				walk(x.Input)
			}
			for _, g := range x.GroupBy {
				walk(g)
			}
			walk(x.Output)
			for _, a := range x.Source.Args {
				walk(a)
			}
			cr.AggIndex = bi
		}
	}
	cr.Slots = len(cr.VarNames)
	if err := cr.plan(); err != nil {
		return nil, err
	}
	return cr, nil
}

func isWildcardName(n string) bool { return strings.HasPrefix(n, "__wild_") }

func termVars(t Term) []int {
	var out []int
	var walk func(Term)
	walk = func(t Term) {
		switch x := t.(type) {
		case *Var:
			out = append(out, x.Index)
		case *Expr:
			for _, a := range x.Args {
				walk(a)
			}
		}
	}
	walk(t)
	return out
}

func atomVars(a Atom) []int {
	var out []int
	for _, t := range a.Args {
		out = append(out, termVars(t)...)
	}
	return out
}

func (cr *CompiledRule) plan() error {
	r := &cr.Rule
	bound := map[int]bool{}
	placed := make([]bool, len(r.Body))
	order := []int{}
	for len(order) < len(r.Body) {
		progress := false
		// Place filters and computations as soon as their inputs are bound.
		for i, item := range r.Body {
			if placed[i] {
				continue
			}
			ready := false
			switch x := item.(type) {
			case *PositiveAtom:
				continue
			case *NegatedAtom:
				ready = true
				for _, slot := range atomVars(x.Atom) {
					name := cr.VarNames[slot]
					if !isWildcardName(name) && !bound[slot] {
						ready = false
						break
					}
				}
			case *Comparison:
				ready = true
				for _, slot := range append(termVars(x.Left), termVars(x.Right)...) {
					if !bound[slot] {
						ready = false
					}
				}
			case *Let:
				ready = true
				for _, slot := range termVars(x.Expr) {
					if !bound[slot] {
						ready = false
					}
				}
			case *AggregateSpec:
				ready = true
			}
			if ready {
				placed[i] = true
				order = append(order, i)
				progress = true
				if let, ok := item.(*Let); ok {
					bound[let.Out.Index] = true
				}
				if agg, ok := item.(*AggregateSpec); ok {
					for _, g := range agg.GroupBy {
						bound[g.Index] = true
					}
					bound[agg.Output.Index] = true
				}
				break
			}
		}
		if progress {
			continue
		}

		// Choose the positive atom with the most already-bound arguments.
		best := -1
		bestScore := -1
		bestTie := 0
		for i, item := range r.Body {
			pa, ok := item.(*PositiveAtom)
			if !ok || placed[i] {
				continue
			}
			score := 0
			unbound := 0
			for _, t := range pa.Atom.Args {
				switch x := t.(type) {
				case *Lit:
					// Constants are genuine access-path restrictions.
					score += 10
				case *Var:
					if isWildcardName(x.Name) {
						continue
					}
					if bound[x.Index] {
						// A shared join variable usually turns a scan into an
						// index lookup.
						score += 10
					} else {
						unbound++
					}
				}
			}
			score -= unbound
			// Ties deliberately choose the earlier body atom. Generated and
			// hand-written programs put the selective driving relation first;
			// counting wildcard columns as selectivity caused Cartesian plans.
			if best == -1 || score > bestScore {
				best = i
				bestScore = score
				bestTie = i
			} else if score == bestScore && i < bestTie {
				best = i
				bestTie = i
			}
		}
		if best == -1 {
			var unready []string
			for i, item := range r.Body {
				if !placed[i] {
					unready = append(unready, fmt.Sprintf("#%d %T", i, item))
				}
			}
			return fmt.Errorf("rule %s has unsafe/unorderable body conditions: %s", cr.ID, strings.Join(unready, "; "))
		}
		pa := r.Body[best].(*PositiveAtom)
		placed[best] = true
		order = append(order, best)
		for _, slot := range atomVars(pa.Atom) {
			bound[slot] = true
		}
	}
	for _, t := range r.Head.Args {
		for _, slot := range termVars(t) {
			name := cr.VarNames[slot]
			if isWildcardName(name) {
				continue
			}
			if !bound[slot] {
				return fmt.Errorf("rule %s at line %d: head variable %s is unsafe; it must occur in a positive body atom/aggregate or be bound by let", cr.ID, r.Line, name)
			}
		}
	}
	cr.Order = order
	return nil
}

// Dependency describes one edge used by stratification and the web graph.
type Dependency struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Strict bool   `json:"strict"`
	Kind   string `json:"kind"` // positive, negated, aggregate
	RuleID string `json:"ruleId"`
}

func ruleDependencies(r Rule, ruleID string) []Dependency {
	var out []Dependency
	to := r.Head.Pred
	for _, item := range r.Body {
		switch x := item.(type) {
		case *PositiveAtom:
			out = append(out, Dependency{From: x.Atom.Pred, To: to, Kind: "positive", RuleID: ruleID})
		case *NegatedAtom:
			out = append(out, Dependency{From: x.Atom.Pred, To: to, Strict: true, Kind: "negated", RuleID: ruleID})
		case *AggregateSpec:
			out = append(out, Dependency{From: x.Source.Pred, To: to, Strict: true, Kind: "aggregate", RuleID: ruleID})
		}
	}
	return out
}

// stratify computes SCCs and layers. A negation/aggregation edge inside an SCC
// is the classic unstratifiable cycle and is rejected.
func stratify(prog *Program, schema map[string]int) (map[string]int, [][]string, error) {
	nodes := make([]string, 0, len(schema))
	for n := range schema {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n] = i
	}
	adj := make([][]int, len(nodes))
	for ri, r := range prog.Rules {
		rid := fmt.Sprintf("R%d", ri+1)
		for _, d := range ruleDependencies(r, rid) {
			u, ok1 := idx[d.From]
			v, ok2 := idx[d.To]
			if !ok1 || !ok2 {
				continue
			}
			adj[u] = append(adj[u], v)
		}
	}
	// Tarjan SCC.
	index := 0
	indices := make([]int, len(nodes))
	low := make([]int, len(nodes))
	onStack := make([]bool, len(nodes))
	stack := []int{}
	for i := range indices {
		indices[i] = -1
	}
	sccOf := make([]int, len(nodes))
	sccNodes := [][]string{}
	var strongConnect func(v int)
	strongConnect = func(v int) {
		indices[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if indices[w] == -1 {
				strongConnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && indices[w] < low[v] {
				low[v] = indices[w]
			}
		}
		if low[v] == indices[v] {
			id := len(sccNodes)
			var members []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				sccOf[w] = id
				members = append(members, nodes[w])
				if w == v {
					break
				}
			}
			sort.Strings(members)
			sccNodes = append(sccNodes, members)
		}
	}
	for v := range nodes {
		if indices[v] == -1 {
			strongConnect(v)
		}
	}

	// Re-check strict edges now that SCCs exist.
	for ri, r := range prog.Rules {
		rid := fmt.Sprintf("R%d", ri+1)
		for _, d := range ruleDependencies(r, rid) {
			u := idx[d.From]
			v := idx[d.To]
			if d.Strict && sccOf[u] == sccOf[v] {
				return nil, nil, fmt.Errorf(
					"unstratifiable rule set: %s uses %s dependency %q in a recursive cycle containing %s (rule %s)",
					rid, d.Kind, d.From, strings.Join(sccNodes[sccOf[u]], ", "), rid)
			}
		}
	}

	// Condensation DAG longest path. Every edge raises the layer; strict edges
	// therefore necessarily put the dependency in an earlier layer.
	succ := make([]map[int]bool, len(sccNodes))
	for i := range succ {
		succ[i] = map[int]bool{}
	}
	indeg := make([]int, len(sccNodes))
	for u, vs := range adj {
		for _, v := range vs {
			cu, cv := sccOf[u], sccOf[v]
			if cu != cv && !succ[cu][cv] {
				succ[cu][cv] = true
				indeg[cv]++
			}
		}
	}
	layer := make([]int, len(sccNodes))
	queue := []int{}
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := range succ[u] {
			if layer[v] < layer[u]+1 {
				layer[v] = layer[u] + 1
			}
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	maxLayer := 0
	for _, l := range layer {
		if l > maxLayer {
			maxLayer = l
		}
	}
	predLayer := map[string]int{}
	layers := make([][]string, maxLayer+1)
	for i, members := range sccNodes {
		l := layer[i]
		layers[l] = append(layers[l], members...)
		for _, m := range members {
			predLayer[m] = l
		}
	}
	for l := range layers {
		sort.Strings(layers[l])
	}
	return predLayer, layers, nil
}
