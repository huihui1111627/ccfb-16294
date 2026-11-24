package zupu

import (
	"fmt"
	"sort"
	"strings"
)

type ProofNode struct {
	Kind       string       `json:"kind"` // fact, rule, negation, test, let, aggregate, note
	Label      string       `json:"label"`
	Detail     string       `json:"detail,omitempty"`
	Predicate  string       `json:"predicate,omitempty"`
	RuleID     string       `json:"ruleId,omitempty"`
	RuleText   string       `json:"ruleText,omitempty"`
	Conclusion []Value      `json:"conclusion,omitempty"`
	Base       bool         `json:"base,omitempty"`
	Truncated  bool         `json:"truncated,omitempty"`
	Children   []*ProofNode `json:"children,omitempty"`
}

type proofBuilder struct {
	e         *Engine
	stack     map[string]bool
	aggLimit  int
	proofStat *RuleStat
}

// Prove reconstructs one concrete derivation tree on demand. Evaluation does
// not retain a proof per row, which keeps hundred-thousand-person runs lean.
func (e *Engine) Prove(pred string, args []Value, aggregateChildLimit int) (*ProofNode, error) {
	r, ok := e.Store.Relations[pred]
	if !ok {
		return nil, fmt.Errorf("unknown predicate %q", pred)
	}
	if len(args) != r.Arity {
		return nil, fmt.Errorf("%s expects %d arguments, got %d", pred, r.Arity, len(args))
	}
	key := EncodeKey(args)
	if _, ok := r.Facts[key]; !ok {
		return nil, fmt.Errorf("fact %s(%s) was not derived", pred, formatValues(args))
	}
	b := &proofBuilder{e: e, stack: map[string]bool{}, aggLimit: aggregateChildLimit, proofStat: &RuleStat{}}
	if b.aggLimit == 0 {
		b.aggLimit = 100
	}
	return b.proveFact(pred, args)
}

func (b *proofBuilder) factKey(pred string, vals []Value) string {
	return pred + "\x00" + EncodeKey(vals)
}

func (b *proofBuilder) proveFact(pred string, vals []Value) (*ProofNode, error) {
	key := EncodeKey(vals)
	stackKey := b.factKey(pred, vals)
	if b.stack[stackKey] {
		return nil, nil
	}
	if bases := b.e.BaseKeys[pred]; bases != nil {
		if _, ok := bases[key]; ok {
			return &ProofNode{
				Kind: "fact", Predicate: pred, Base: true, Conclusion: vals,
				Label:  atomString(Atom{Pred: pred, Args: valuesToTerms(vals)}),
				Detail: "EDB 原始事实",
			}, nil
		}
	}
	rules := b.e.ByHead[pred]
	if len(rules) == 0 {
		return &ProofNode{
			Kind: "fact", Predicate: pred, Base: true, Conclusion: vals,
			Label:  atomString(Atom{Pred: pred, Args: valuesToTerms(vals)}),
			Detail: "存储事实",
		}, nil
	}
	b.stack[stackKey] = true
	defer delete(b.stack, stackKey)
	for _, cr := range rules {
		children, ok := b.proveRule(cr, vals)
		if !ok {
			continue
		}
		return &ProofNode{
			Kind: "rule", Predicate: pred, RuleID: cr.ID, RuleText: cr.Text(),
			Conclusion: vals, Children: children,
			Label: fmt.Sprintf("%s 推导 %s", cr.ID, atomString(Atom{Pred: pred, Args: valuesToTerms(vals)})),
		}, nil
	}
	return &ProofNode{
		Kind: "note", Predicate: pred, Conclusion: vals,
		Label: "未找到可回放的推导", Detail: "该事实存在，但没有规则分支能在证明搜索中复现；可能是证明深度或循环保护导致。",
	}, nil
}

func valuesToTerms(vs []Value) []Term {
	out := make([]Term, len(vs))
	for i, v := range vs {
		out[i] = &Lit{Value: v}
	}
	return out
}

func (b *proofBuilder) proveRule(cr *CompiledRule, goal []Value) ([]*ProofNode, bool) {
	env := newEnv(cr.Slots)
	for i, t := range cr.Rule.Head.Args {
		switch x := t.(type) {
		case *Var:
			if i >= len(goal) || !env.unifyVar(x.Index, goal[i]) {
				return nil, false
			}
		case *Lit:
			if i >= len(goal) || !valuesEqual(x.Value, goal[i]) {
				return nil, false
			}
		default:
			v, ok := evalTerm(x, env)
			if !ok || i >= len(goal) || !valuesEqual(v, goal[i]) {
				return nil, false
			}
		}
	}

	var solve func(pos int, children []*ProofNode) ([]*ProofNode, bool)
	solve = func(pos int, children []*ProofNode) ([]*ProofNode, bool) {
		if pos == len(cr.Order) {
			return children, true
		}
		bi := cr.Order[pos]
		item := cr.Rule.Body[bi]
		switch x := item.(type) {
		case *PositiveAtom:
			var result []*ProofNode
			ok := false
			b.e.scanAtom(b.proofStat, bi, x.Atom, env, nil, func(row *Row) bool {
				sub, err := b.proveFact(x.Atom.Pred, row.Values)
				if err != nil || sub == nil {
					return false
				}
				path := append(append([]*ProofNode{}, children...), sub)
				out, good := solve(pos+1, path)
				if good {
					result = out
					ok = true
					return true
				}
				return false
			})
			return result, ok

		case *NegatedAtom:
			matched := false
			b.e.scanAtom(b.proofStat, bi, x.Atom, env, nil, func(*Row) bool {
				matched = true
				return true
			})
			if matched {
				return nil, false
			}
			n := &ProofNode{
				Kind:   "negation",
				Label:  "not " + atomString(x.Atom),
				Detail: fmt.Sprintf("失败即否定：%s 已完成分层求值，按当前绑定未找到匹配事实", x.Atom.Pred),
			}
			return solve(pos+1, append(children, n))

		case *Comparison:
			lv, lok := evalTerm(x.Left, env)
			rv, rok := evalTerm(x.Right, env)
			if !lok || !rok || !compareValues(x.Op, lv, rv) {
				return nil, false
			}
			n := &ProofNode{
				Kind:   "test",
				Label:  fmt.Sprintf("%s %s %s", termString(x.Left), x.Op, termString(x.Right)),
				Detail: fmt.Sprintf("代入为 %s %s %s", lv.String(), x.Op, rv.String()),
			}
			return solve(pos+1, append(children, n))

		case *Let:
			v, ok := evalTerm(x.Expr, env)
			if !ok {
				return nil, false
			}
			cp := env.checkpoint()
			if !env.unifyVar(x.Out.Index, v) {
				env.undo(cp)
				return nil, false
			}
			n := &ProofNode{
				Kind:   "let",
				Label:  fmt.Sprintf("let %s = %s", x.Out.Name, termString(x.Expr)),
				Detail: fmt.Sprintf("%s = %s", x.Out.Name, v.String()),
			}
			out, good := solve(pos+1, append(children, n))
			env.undo(cp)
			return out, good

		case *AggregateSpec:
			groups, err := b.e.computeAggregate(b.proofStat, x, env)
			if err != nil {
				return nil, false
			}
			for _, g := range groups {
				cp := env.checkpoint()
				goodBind := true
				for i, slot := range g.groupSlots {
					if !env.unifyVar(slot, g.key[i]) {
						goodBind = false
						break
					}
				}
				if goodBind && env.unifyVar(x.Output.Index, g.value) {
					aggNode := b.aggregateNode(x, g)
					out, good := solve(pos+1, append(children, aggNode))
					env.undo(cp)
					if good {
						return out, true
					}
				} else {
					env.undo(cp)
				}
			}
			return nil, false
		}
		return nil, false
	}
	return solve(0, []*ProofNode{})
}

func (b *proofBuilder) aggregateNode(spec *AggregateSpec, g aggGroup) *ProofNode {
	groupText := []string{}
	for i, gv := range spec.GroupBy {
		groupText = append(groupText, gv.Name+"="+g.key[i].String())
	}
	n := &ProofNode{
		Kind:      "aggregate",
		Predicate: spec.Source.Pred,
		Label:     fmt.Sprintf("%s 聚合 = %s", strings.ToUpper(spec.Func), g.value.String()),
		Detail:    strings.Join(groupText, ", "),
	}
	r := b.e.Store.Relations[spec.Source.Pred]
	groupCols := make([]int, len(spec.GroupBy))
	for i, gv := range spec.GroupBy {
		for c, t := range spec.Source.Args {
			if v, ok := t.(*Var); ok && v.Index == gv.Index {
				groupCols[i] = c
				break
			}
		}
	}
	type matched struct{ row []Value }
	var rows []matched
	patternEnv := newEnv(aggregateSlotCount(spec))
	for _, f := range r.Facts {
		cp := patternEnv.checkpoint()
		patternOK := unifyAtom(spec.Source, f.Values, patternEnv)
		ok := patternOK
		if ok {
			for i, c := range groupCols {
				if !valuesEqual(f.Values[c], g.key[i]) {
					ok = false
					break
				}
			}
		}
		patternEnv.undo(cp)
		if ok {
			rows = append(rows, matched{f.Values})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return EncodeKey(rows[i].row) < EncodeKey(rows[j].row) })
	limit := len(rows)
	if b.aggLimit > 0 && limit > b.aggLimit {
		limit = b.aggLimit
		n.Truncated = true
	}
	for i := 0; i < limit; i++ {
		sub, _ := b.proveFact(spec.Source.Pred, rows[i].row)
		if sub != nil {
			n.Children = append(n.Children, sub)
		}
	}
	if n.Truncated {
		n.Children = append(n.Children, &ProofNode{
			Kind:   "note",
			Label:  fmt.Sprintf("仅展示前 %d 条，共 %d 条聚合来源", b.aggLimit, len(rows)),
			Detail: "调用证明 API 时设置 aggLimit=-1 可返回全部来源。",
		})
	}
	return n
}

func aggregateSlotCount(spec *AggregateSpec) int {
	maxSlot := -1
	walk := func(t Term) {
		for _, slot := range termVars(t) {
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}
	for _, a := range spec.Source.Args {
		walk(a)
	}
	if spec.Input != nil {
		walk(spec.Input)
	}
	if spec.Output != nil {
		walk(spec.Output)
	}
	for _, g := range spec.GroupBy {
		walk(g)
	}
	return maxSlot + 1
}

func formatValues(vs []Value) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.String()
	}
	return strings.Join(parts, ", ")
}
