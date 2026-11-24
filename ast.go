package zupu

import "fmt"

// Term is implemented by Var, Lit and Expr.
type Term interface{ termNode() }

type Var struct {
	Name  string
	Index int // assigned by the rule compiler; -1 until then
}

func (v *Var) termNode() {}

type Lit struct {
	Value Value
}

func (l *Lit) termNode() {}

// Expr is both function call and operator node. Operators use names such as
// "+", "-", ">" and "and".
type Expr struct {
	Op   string
	Args []Term
}

func (e *Expr) termNode() {}

type Atom struct {
	Pred string
	Args []Term
	Line int
}

type BodyItem interface{ bodyItem() }

type PositiveAtom struct {
	Atom Atom
}

func (*PositiveAtom) bodyItem() {}

type NegatedAtom struct {
	Atom Atom
}

func (*NegatedAtom) bodyItem() {}

type Comparison struct {
	Op    string
	Left  Term
	Right Term
	Line  int
}

func (*Comparison) bodyItem() {}

type Let struct {
	Out  *Var
	Expr Term
	Line int
}

func (*Let) bodyItem() {}

// AggregateSpec is the restricted, explicitly stratified aggregate form:
//
//	agg(count(?c), source(?c, ?g), group(?g), ?n)
type AggregateSpec struct {
	Func     string // count, min, max, sum
	Input    *Var   // nil for count(*)
	Distinct bool
	Source   Atom
	GroupBy  []*Var
	Output   *Var
	Line     int
}

func (*AggregateSpec) bodyItem() {}

type Rule struct {
	Head Atom
	Body []BodyItem
	Line int
}

type Fact struct {
	Pred string
	Args []Value
	Line int
}

type Program struct {
	Facts []Fact
	Rules []Rule
}

func (p *Program) Predicates() map[string]int {
	out := map[string]int{}
	setArity := func(pred string, arity int) {
		if a, ok := out[pred]; ok && a != arity {
			out[pred] = -1
		} else if !ok {
			out[pred] = arity
		}
	}
	for _, f := range p.Facts {
		setArity(f.Pred, len(f.Args))
	}
	for _, r := range p.Rules {
		setArity(r.Head.Pred, len(r.Head.Args))
		for _, item := range r.Body {
			switch x := item.(type) {
			case *PositiveAtom:
				setArity(x.Atom.Pred, len(x.Atom.Args))
			case *NegatedAtom:
				setArity(x.Atom.Pred, len(x.Atom.Args))
			case *AggregateSpec:
				setArity(x.Source.Pred, len(x.Source.Args))
			}
		}
	}
	return out
}

func termString(t Term) string {
	switch x := t.(type) {
	case *Var:
		return x.Name
	case *Lit:
		return x.Value.String()
	case *Expr:
		switch x.Op {
		case "+", "-", "*", "/", ">", "<", ">=", "<=", "==", "!=":
			if len(x.Args) == 2 {
				return "(" + termString(x.Args[0]) + " " + x.Op + " " + termString(x.Args[1]) + ")"
			}
		}
		s := x.Op + "("
		for i, a := range x.Args {
			if i > 0 {
				s += ", "
			}
			s += termString(a)
		}
		return s + ")"
	}
	return "?"
}

func atomString(a Atom) string {
	s := a.Pred + "("
	for i, t := range a.Args {
		if i > 0 {
			s += ", "
		}
		s += termString(t)
	}
	return s + ")"
}

func (r Rule) String() string {
	s := atomString(r.Head) + " :- "
	for i, b := range r.Body {
		if i > 0 {
			s += ", "
		}
		switch x := b.(type) {
		case *PositiveAtom:
			s += atomString(x.Atom)
		case *NegatedAtom:
			s += "not " + atomString(x.Atom)
		case *Comparison:
			s += termString(x.Left) + " " + x.Op + " " + termString(x.Right)
		case *Let:
			s += "let " + x.Out.Name + " = " + termString(x.Expr)
		case *AggregateSpec:
			input := "*"
			if x.Input != nil {
				input = x.Input.Name
			}
			s += fmt.Sprintf("agg(%s(%s), %s, group(", x.Func, input, atomString(x.Source))
			for j, g := range x.GroupBy {
				if j > 0 {
					s += ", "
				}
				s += g.Name
			}
			s += "), " + x.Output.Name + ")"
		}
	}
	return s + "."
}
