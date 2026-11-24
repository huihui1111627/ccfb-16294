package zupu

import (
	"fmt"
	"sort"
	"time"
)

type DeltaSpec struct {
	BodyIndex int
	Pred      string
	Facts     []*Row
}

type Iteration struct {
	Layer        int            `json:"layer"`
	Round        int            `json:"round"`
	Label        string         `json:"label"`
	NewTotal     int            `json:"newTotal"`
	NewByPred    map[string]int `json:"newByPredicate"`
	DurationMS   float64        `json:"durationMs"`
	DeltaPresent int            `json:"deltaPresent"`
}

type JoinTotals struct {
	Executions     int64   `json:"executions"`
	AtomEntries    int64   `json:"atomEntries"`
	CandidateJoins int64   `json:"candidateJoins"`
	MatchedJoins   int64   `json:"matchedJoins"`
	IndexLookups   int64   `json:"indexLookups"`
	FullScans      int64   `json:"fullScans"`
	AggregateRows  int64   `json:"aggregateRows"`
	Derivations    int64   `json:"derivations"`
	NewFacts       int64   `json:"newFacts"`
	DurationMS     float64 `json:"durationMs"`
}

type Report struct {
	Mode            string         `json:"mode"`
	RuleSetID       string         `json:"ruleSetId,omitempty"`
	RuleSetName     string         `json:"ruleSetName,omitempty"`
	DurationMS      float64        `json:"durationMs"`
	TotalFacts      int            `json:"totalFacts"`
	BaseFacts       int            `json:"baseFacts"`
	DerivedFacts    int            `json:"derivedFacts"`
	PredicateCounts map[string]int `json:"predicateCounts"`
	RuleStats       []*RuleStat    `json:"ruleStats"`
	Iterations      []Iteration    `json:"iterations"`
	Totals          JoinTotals     `json:"totals"`
	Layers          [][]string     `json:"layers"`
}

type Engine struct {
	Program    *Program
	Schema     map[string]int
	Store      *Store
	Rules      []*CompiledRule
	ByHead     map[string][]*CompiledRule
	ByLayer    map[int][]*CompiledRule
	PredLayer  map[string]int
	Layers     [][]string
	Iterations []Iteration
	BaseFacts  int
	BaseKeys   map[string]map[string]struct{}
}

func NewEngine(prog *Program) (*Engine, error) {
	schema, rules, predLayer, layers, err := compileProgram(prog)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		Program:   prog,
		Schema:    schema,
		Store:     NewStore(schema),
		Rules:     rules,
		ByHead:    map[string][]*CompiledRule{},
		ByLayer:   map[int][]*CompiledRule{},
		PredLayer: predLayer,
		Layers:    layers,
		BaseKeys:  map[string]map[string]struct{}{},
	}
	for _, f := range prog.Facts {
		if _, err := e.Store.Insert(f.Pred, f.Args); err != nil {
			return nil, err
		}
		e.BaseFacts++
		if e.BaseKeys[f.Pred] == nil {
			e.BaseKeys[f.Pred] = map[string]struct{}{}
		}
		e.BaseKeys[f.Pred][EncodeKey(f.Args)] = struct{}{}
	}
	for _, cr := range rules {
		e.ByHead[cr.HeadPred] = append(e.ByHead[cr.HeadPred], cr)
		layer := cr.Stats.Layer
		e.ByLayer[layer] = append(e.ByLayer[layer], cr)
	}
	if err := e.prepareJoinIndexes(); err != nil {
		return nil, err
	}
	return e, nil
}

// prepareJoinIndexes builds single-column indexes for constants and shared
// variables used in joins. The web API can additionally build arbitrary
// composite indexes.
func (e *Engine) prepareJoinIndexes() error {
	occurrences := map[string]map[int]int{}
	for _, cr := range e.Rules {
		counts := map[int]int{}
		for _, bi := range cr.Positive {
			a := cr.Rule.Body[bi].(*PositiveAtom).Atom
			for _, t := range a.Args {
				for _, slot := range termVars(t) {
					counts[slot]++
				}
			}
		}
		_ = occurrences
		for _, bi := range cr.Positive {
			a := cr.Rule.Body[bi].(*PositiveAtom).Atom
			for col, t := range a.Args {
				need := false
				switch x := t.(type) {
				case *Lit:
					need = true
				case *Var:
					if counts[x.Index] > 1 {
						need = true
					}
				}
				if need {
					if _, err := e.Store.EnsureIndex(a.Pred, []int{col}); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

type pendingMap map[string]map[string]*Row

func newPending() pendingMap { return pendingMap{} }

func (p pendingMap) add(e *Engine, pred string, vals []Value) (bool, error) {
	r, ok := e.Store.Relations[pred]
	if !ok {
		return false, fmt.Errorf("unknown predicate %q", pred)
	}
	if len(vals) != r.Arity {
		return false, fmt.Errorf("%s head is arity %d but rule produced %d", pred, r.Arity, len(vals))
	}
	key := EncodeKey(vals)
	if _, exists := r.Facts[key]; exists {
		return false, nil
	}
	m := p[pred]
	if m == nil {
		m = map[string]*Row{}
		p[pred] = m
	}
	if _, exists := m[key]; exists {
		return false, nil
	}
	m[key] = &Row{Values: vals}
	return true, nil
}

func (e *Engine) merge(pending pendingMap) (map[string][]*Row, map[string]int, int, error) {
	delta := map[string][]*Row{}
	byPred := map[string]int{}
	total := 0
	e.Store.mu.Lock()
	defer e.Store.mu.Unlock()
	for pred, m := range pending {
		r := e.Store.Relations[pred]
		for key, f := range m {
			if _, exists := r.Facts[key]; exists {
				continue
			}
			r.Facts[key] = f
			for _, idx := range r.indexes {
				k := indexKey(idx.Cols, f.Values)
				idx.entries[k] = append(idx.entries[k], f)
			}
			delta[pred] = append(delta[pred], f)
			byPred[pred]++
			total++
		}
	}
	return delta, byPred, total, nil
}

func (e *Engine) runRule(cr *CompiledRule, delta *DeltaSpec, pending pendingMap) error {
	start := time.Now()
	stats := cr.Stats
	stats.Executions++
	env := newEnv(cr.Slots)
	order := cr.Order
	if delta != nil {
		// The delta literal drives a semi-naive variant. Every delta row is
		// visited once; all later atoms use bound-argument indexes.
		order = make([]int, 0, len(cr.Order))
		order = append(order, delta.BodyIndex)
		for _, bi := range cr.Order {
			if bi != delta.BodyIndex {
				order = append(order, bi)
			}
		}
	}

	var emit = func() error {
		vals := make([]Value, len(cr.Rule.Head.Args))
		for i, t := range cr.Rule.Head.Args {
			v, ok := evalTerm(t, env)
			if !ok {
				if hv, isVar := t.(*Var); isVar && isWildcardName(hv.Name) {
					v = Null()
				} else {
					return nil // head remains unsafe for this answer
				}
			}
			vals[i] = v
		}
		stats.Derivations++
		isNew, err := pending.add(e, cr.HeadPred, vals)
		if err != nil {
			return err
		}
		if isNew {
			stats.NewFacts++
		}
		return nil
	}

	var solveAt func(pos int) error
	solveAt = func(pos int) error {
		if pos == len(order) {
			return emit()
		}
		bi := order[pos]
		item := cr.Rule.Body[bi]
		switch x := item.(type) {
		case *PositiveAtom:
			e.scanAtom(cr.Stats, bi, x.Atom, env, delta, func(f *Row) bool {
				if err := solveAt(pos + 1); err != nil {
					// Fatal engine errors are captured by runRule via the
					// named error mechanism below.
					panic(evalError{err})
				}
				return false
			})
			return nil
		case *NegatedAtom:
			matched := false
			e.scanAtom(cr.Stats, bi, x.Atom, env, nil, func(f *Row) bool {
				matched = true
				return true
			})
			if !matched {
				return solveAt(pos + 1)
			}
			return nil
		case *Comparison:
			lv, lok := evalTerm(x.Left, env)
			rv, rok := evalTerm(x.Right, env)
			if lok && rok && compareValues(x.Op, lv, rv) {
				return solveAt(pos + 1)
			}
			return nil
		case *Let:
			v, ok := evalTerm(x.Expr, env)
			if !ok {
				return nil
			}
			cp := env.checkpoint()
			if env.unifyVar(x.Out.Index, v) {
				if err := solveAt(pos + 1); err != nil {
					env.undo(cp)
					return err
				}
			}
			env.undo(cp)
			return nil
		case *AggregateSpec:
			groups, err := e.computeAggregate(stats, x, env)
			if err != nil {
				return err
			}
			for _, g := range groups {
				cp := env.checkpoint()
				ok := true
				for i, slot := range g.groupSlots {
					if !env.unifyVar(slot, g.key[i]) {
						ok = false
						break
					}
				}
				if ok && env.unifyVar(x.Output.Index, g.value) {
					if err := solveAt(pos + 1); err != nil {
						env.undo(cp)
						return err
					}
				}
				env.undo(cp)
			}
			return nil
		default:
			return fmt.Errorf("unknown body item %T", item)
		}
	}

	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if ee, ok := rec.(evalError); ok {
					err = ee.err
					return
				}
				panic(rec)
			}
		}()
		err = solveAt(0)
	}()
	stats.Duration += time.Since(start)
	stats.DurationMS = float64(stats.Duration.Microseconds()) / 1000.0
	return err
}

type evalError struct{ err error }

// scanAtom performs index selection, candidate counting and unification. The
// callback is invoked with the substitution active; scanAtom undoes it after
// callback returns. Returning true stops scanning (used by negation).
func (e *Engine) scanAtom(stats *RuleStat, bodyIndex int, a Atom, env *Env, delta *DeltaSpec, yield func(*Row) bool) bool {
	stats.AtomEntries++
	if delta != nil && delta.BodyIndex == bodyIndex {
		for _, f := range delta.Facts {
			stats.CandidateJoins++
			cp := env.checkpoint()
			if unifyAtom(a, f.Values, env) {
				stats.MatchedJoins++
				if yield(f) {
					env.undo(cp)
					return true
				}
			}
			env.undo(cp)
		}
		return false
	}

	bound := map[int]Value{}
	for i, t := range a.Args {
		switch x := t.(type) {
		case *Lit:
			bound[i] = x.Value
		case *Var:
			if v, ok := env.get(x); ok {
				bound[i] = v
			}
		default:
			if v, ok := evalTerm(t, env); ok {
				bound[i] = v
			}
		}
	}
	idx, vals := e.Store.BestIndex(a.Pred, bound)
	if idx == nil && len(bound) > 0 {
		cols := make([]int, 0, len(bound))
		for c := range bound {
			cols = append(cols, c)
		}
		sort.Ints(cols)
		// A single-column index is enough for nested-loop evaluation; residual
		// bindings are checked by unifyAtom.
		idx, _ = e.Store.EnsureIndex(a.Pred, []int{cols[0]})
		if idx != nil {
			vals = []Value{bound[cols[0]]}
		}
	}
	if idx != nil {
		stats.IndexLookups++
		key := EncodeKey(vals)
		rows := idx.entries[key]
		for _, f := range rows {
			stats.CandidateJoins++
			cp := env.checkpoint()
			if unifyAtom(a, f.Values, env) {
				stats.MatchedJoins++
				if yield(f) {
					env.undo(cp)
					return true
				}
			}
			env.undo(cp)
		}
		return false
	}

	r := e.Store.Relations[a.Pred]
	stats.FullScans++
	for _, f := range r.Facts {
		stats.CandidateJoins++
		cp := env.checkpoint()
		if unifyAtom(a, f.Values, env) {
			stats.MatchedJoins++
			if yield(f) {
				env.undo(cp)
				return true
			}
		}
		env.undo(cp)
	}
	return false
}

func unifyAtom(a Atom, row []Value, env *Env) bool {
	if len(a.Args) != len(row) {
		return false
	}
	for i, t := range a.Args {
		switch x := t.(type) {
		case *Lit:
			if !valuesEqual(x.Value, row[i]) {
				return false
			}
		case *Var:
			if !env.unifyVar(x.Index, row[i]) {
				return false
			}
		default:
			v, ok := evalTerm(x, env)
			if !ok || !valuesEqual(v, row[i]) {
				return false
			}
		}
	}
	return true
}

type aggGroup struct {
	key        []Value
	groupSlots []int
	value      Value
}

func (e *Engine) computeAggregate(stats *RuleStat, spec *AggregateSpec, env *Env) ([]aggGroup, error) {
	r := e.Store.Relations[spec.Source.Pred]
	if r == nil {
		return nil, fmt.Errorf("unknown aggregate source %s", spec.Source.Pred)
	}
	groupCols := make([]int, len(spec.GroupBy))
	groupSlots := make([]int, len(spec.GroupBy))
	for i, gv := range spec.GroupBy {
		col := -1
		for c, t := range spec.Source.Args {
			if v, ok := t.(*Var); ok && v.Index == gv.Index {
				col = c
				break
			}
		}
		if col < 0 {
			return nil, fmt.Errorf("aggregate group variable %s must occur in source %s", gv.Name, spec.Source.Pred)
		}
		groupCols[i] = col
		groupSlots[i] = gv.Index
	}
	inputCol := -1
	if spec.Input != nil {
		for c, t := range spec.Source.Args {
			if v, ok := t.(*Var); ok && v.Index == spec.Input.Index {
				inputCol = c
				break
			}
		}
		if inputCol < 0 {
			return nil, fmt.Errorf("aggregate input %s must occur in source", spec.Input.Name)
		}
	}
	type state struct {
		key         []Value
		count       int64
		distinct    map[string]struct{}
		distinctSum int64
		min         Value
		max         Value
		sum         int64
		has         bool
	}
	states := map[string]*state{}
	order := []string{}
	for _, f := range r.Facts {
		stats.CandidateJoins++
		stats.AggregateRows++
		rowCP := env.checkpoint()
		// Aggregate sources are atoms too: constants and repeated variables
		// in the source pattern must filter the aggregated rows.
		if !unifyAtom(spec.Source, f.Values, env) {
			env.undo(rowCP)
			continue
		}
		keyVals := make([]Value, len(groupCols))
		for i, c := range groupCols {
			keyVals[i] = f.Values[c]
		}
		k := EncodeKey(keyVals)
		st := states[k]
		if st == nil {
			st = &state{key: keyVals, distinct: map[string]struct{}{}}
			states[k] = st
			order = append(order, k)
		}
		st.count++
		if spec.Distinct && spec.Input != nil {
			iv := f.Values[inputCol]
			dkey := EncodeKey([]Value{iv})
			if _, seen := st.distinct[dkey]; !seen {
				st.distinct[dkey] = struct{}{}
				if iv.Kind == KindInt {
					st.distinctSum += iv.I
				}
			}
		}
		if spec.Input != nil {
			iv := f.Values[inputCol]
			if !st.has {
				st.min, st.max, st.has = iv, iv, true
			} else {
				if compareValues("<", iv, st.min) {
					st.min = iv
				}
				if compareValues(">", iv, st.max) {
					st.max = iv
				}
			}
			if iv.Kind == KindInt {
				st.sum += iv.I
			}
		}
		env.undo(rowCP)
	}
	out := make([]aggGroup, 0, len(order))
	for _, k := range order {
		st := states[k]
		var val Value
		switch spec.Func {
		case "count":
			n := st.count
			if spec.Distinct {
				n = int64(len(st.distinct))
			}
			val = Int(n)
		case "min":
			val = st.min
		case "max":
			val = st.max
		case "sum":
			if spec.Distinct {
				val = Int(st.distinctSum)
			} else {
				val = Int(st.sum)
			}
		default:
			return nil, fmt.Errorf("unsupported aggregate %q", spec.Func)
		}
		out = append(out, aggGroup{key: st.key, groupSlots: groupSlots, value: val})
	}
	return out, nil
}

func (e *Engine) makeReport(mode, setID, setName string, start time.Time) *Report {
	counts := map[string]int{}
	total := 0
	for name, r := range e.Store.Relations {
		n := len(r.Facts)
		counts[name] = n
		total += n
	}
	stats := append([]*RuleStat(nil), func() []*RuleStat {
		out := []*RuleStat{}
		for _, rs := range e.Rules {
			out = append(out, rs.Stats)
		}
		return out
	}()...)
	sort.Slice(stats, func(i, j int) bool { return stats[i].RuleID < stats[j].RuleID })
	t := JoinTotals{}
	for _, rs := range stats {
		t.Executions += rs.Executions
		t.AtomEntries += rs.AtomEntries
		t.CandidateJoins += rs.CandidateJoins
		t.MatchedJoins += rs.MatchedJoins
		t.IndexLookups += rs.IndexLookups
		t.FullScans += rs.FullScans
		t.AggregateRows += rs.AggregateRows
		t.Derivations += rs.Derivations
		t.NewFacts += rs.NewFacts
		t.DurationMS += rs.DurationMS
	}
	return &Report{
		Mode:            mode,
		RuleSetID:       setID,
		RuleSetName:     setName,
		DurationMS:      float64(time.Since(start).Microseconds()) / 1000.0,
		TotalFacts:      total,
		BaseFacts:       e.BaseFacts,
		DerivedFacts:    total - e.BaseFacts,
		PredicateCounts: counts,
		RuleStats:       stats,
		Iterations:      append([]Iteration(nil), e.Iterations...),
		Totals:          t,
		Layers:          e.Layers,
	}
}

// EvaluateSemi runs delta-only semi-naive evaluation layer by layer.
func (e *Engine) EvaluateSemi(setID, setName string) (*Report, error) {
	start := time.Now()
	for layer := 0; layer < len(e.Layers); layer++ {
		rules := e.ByLayer[layer]
		round := 1
		{
			t0 := time.Now()
			pending := newPending()
			for _, cr := range rules {
				if err := e.runRule(cr, nil, pending); err != nil {
					return nil, err
				}
			}
			delta, byPred, n, err := e.merge(pending)
			if err != nil {
				return nil, err
			}
			e.Iterations = append(e.Iterations, Iteration{
				Layer: layer, Round: round, Label: fmt.Sprintf("L%d/R%d initial", layer, round),
				NewTotal: n, NewByPred: byPred, DurationMS: float64(time.Since(t0).Microseconds()) / 1000.0,
				DeltaPresent: len(delta),
			})
			round++
			current := delta
			for len(current) > 0 {
				t1 := time.Now()
				pending := newPending()
				deltaPresent := 0
				for _, cr := range rules {
					for _, bi := range cr.Positive {
						a := cr.Rule.Body[bi].(*PositiveAtom).Atom
						if e.PredLayer[a.Pred] != layer {
							continue
						}
						facts := current[a.Pred]
						if len(facts) == 0 {
							continue
						}
						deltaPresent++
						spec := &DeltaSpec{BodyIndex: bi, Pred: a.Pred, Facts: facts}
						if err := e.runRule(cr, spec, pending); err != nil {
							return nil, err
						}
					}
				}
				next, byPred2, n2, err := e.merge(pending)
				if err != nil {
					return nil, err
				}
				e.Iterations = append(e.Iterations, Iteration{
					Layer: layer, Round: round, Label: fmt.Sprintf("L%d/R%d delta", layer, round),
					NewTotal: n2, NewByPred: byPred2, DurationMS: float64(time.Since(t1).Microseconds()) / 1000.0,
					DeltaPresent: deltaPresent,
				})
				current = next
				round++
			}
		}
	}
	return e.makeReport("semi-naive", setID, setName, start), nil
}

// EvaluateNaive recomputes every rule against every fact in each round. It is
// the simple reference evaluator used for differential testing.
func (e *Engine) EvaluateNaive(setID, setName string) (*Report, error) {
	start := time.Now()
	for layer := 0; layer < len(e.Layers); layer++ {
		rules := e.ByLayer[layer]
		for round := 1; ; round++ {
			t0 := time.Now()
			pending := newPending()
			for _, cr := range rules {
				if err := e.runRule(cr, nil, pending); err != nil {
					return nil, err
				}
			}
			_, byPred, n, err := e.merge(pending)
			if err != nil {
				return nil, err
			}
			e.Iterations = append(e.Iterations, Iteration{
				Layer: layer, Round: round, Label: fmt.Sprintf("L%d/R%d full-recompute", layer, round),
				NewTotal: n, NewByPred: byPred, DurationMS: float64(time.Since(t0).Microseconds()) / 1000.0,
			})
			if n == 0 {
				break
			}
		}
	}
	return e.makeReport("naive-full-recompute", setID, setName, start), nil
}
