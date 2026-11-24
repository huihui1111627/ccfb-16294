package zupu

import (
	"math"
	"strings"
	"unicode/utf8"
)

// Env is a trailable variable substitution for one rule invocation.
type Env struct {
	vals  []Value
	bound []bool
	trail []int
}

func newEnv(slots int) *Env {
	return &Env{vals: make([]Value, slots), bound: make([]bool, slots)}
}

func (e *Env) checkpoint() int { return len(e.trail) }

func (e *Env) undo(to int) {
	for len(e.trail) > to {
		i := e.trail[len(e.trail)-1]
		e.trail = e.trail[:len(e.trail)-1]
		e.bound[i] = false
		e.vals[i] = Value{}
	}
}

func (e *Env) get(v *Var) (Value, bool) {
	if v == nil || v.Index < 0 || v.Index >= len(e.bound) || !e.bound[v.Index] {
		return Value{}, false
	}
	return e.vals[v.Index], true
}

func (e *Env) unifyVar(slot int, v Value) bool {
	if slot < 0 {
		return true
	}
	if e.bound[slot] {
		return valuesEqual(e.vals[slot], v)
	}
	e.bound[slot] = true
	e.vals[slot] = v
	e.trail = append(e.trail, slot)
	return true
}

func valuesEqual(a, b Value) bool { return a == b }

func evalTerm(t Term, env *Env) (Value, bool) {
	switch x := t.(type) {
	case *Var:
		return env.get(x)
	case *Lit:
		return x.Value, true
	case *Expr:
		vals := make([]Value, len(x.Args))
		for i, a := range x.Args {
			v, ok := evalTerm(a, env)
			if !ok {
				return Value{}, false
			}
			vals[i] = v
		}
		return callExpr(x.Op, vals)
	default:
		return Value{}, false
	}
}

func callExpr(op string, args []Value) (Value, bool) {
	switch op {
	case "neg":
		if len(args) != 1 || args[0].Kind != KindInt {
			return Value{}, false
		}
		return Int(-args[0].I), true
	case "+":
		if len(args) != 2 || args[0].Kind != KindInt || args[1].Kind != KindInt {
			return Value{}, false
		}
		return Int(args[0].I + args[1].I), true
	case "-":
		if len(args) != 2 || args[0].Kind != KindInt || args[1].Kind != KindInt {
			return Value{}, false
		}
		return Int(args[0].I - args[1].I), true
	case "*":
		if len(args) != 2 || args[0].Kind != KindInt || args[1].Kind != KindInt {
			return Value{}, false
		}
		return Int(args[0].I * args[1].I), true
	case "/":
		if len(args) != 2 || args[0].Kind != KindInt || args[1].Kind != KindInt || args[1].I == 0 {
			return Value{}, false
		}
		return Int(args[0].I / args[1].I), true
	case "mod":
		if len(args) != 2 || args[0].Kind != KindInt || args[1].Kind != KindInt || args[1].I == 0 {
			return Value{}, false
		}
		return Int(args[0].I % args[1].I), true
	case "abs":
		if len(args) != 1 || args[0].Kind != KindInt {
			return Value{}, false
		}
		return Int(int64(math.Abs(float64(args[0].I)))), true
	case "min2":
		if len(args) == 2 && args[0].Kind == KindInt && args[1].Kind == KindInt {
			if args[0].I < args[1].I {
				return args[0], true
			}
			return args[1], true
		}
	case "max2":
		if len(args) == 2 && args[0].Kind == KindInt && args[1].Kind == KindInt {
			if args[0].I > args[1].I {
				return args[0], true
			}
			return args[1], true
		}
	case "length", "len":
		if len(args) != 1 || args[0].Kind != KindString {
			return Value{}, false
		}
		return Int(int64(utf8.RuneCountInString(args[0].S))), true
	case "substr", "substring":
		if len(args) < 2 || len(args) > 3 || args[0].Kind != KindString || args[1].Kind != KindInt {
			return Value{}, false
		}
		r := []rune(args[0].S)
		start := int(args[1].I)
		if start < 0 {
			start = 0
		}
		if start > len(r) {
			return String(""), true
		}
		if len(args) == 2 {
			return String(string(r[start:])), true
		}
		if args[2].Kind != KindInt {
			return Value{}, false
		}
		n := int(args[2].I)
		if n < 0 {
			n = 0
		}
		end := start + n
		if end > len(r) {
			end = len(r)
		}
		return String(string(r[start:end])), true
	case "char_at":
		if len(args) != 2 || args[0].Kind != KindString || args[1].Kind != KindInt {
			return Value{}, false
		}
		r := []rune(args[0].S)
		i := int(args[1].I)
		if i < 0 || i >= len(r) {
			return Value{}, false
		}
		return String(string(r[i])), true
	case "concat":
		var b strings.Builder
		for _, a := range args {
			b.WriteString(valueDisplay(a))
		}
		return String(b.String()), true
	case "upper":
		if len(args) == 1 && args[0].Kind == KindString {
			return String(strings.ToUpper(args[0].S)), true
		}
	case "lower":
		if len(args) == 1 && args[0].Kind == KindString {
			return String(strings.ToLower(args[0].S)), true
		}
	case "trim":
		if len(args) == 1 && args[0].Kind == KindString {
			return String(strings.TrimSpace(args[0].S)), true
		}
	case "contains":
		if len(args) == 2 && args[0].Kind == KindString && args[1].Kind == KindString {
			return Bool(strings.Contains(args[0].S, args[1].S)), true
		}
	case "has_prefix":
		if len(args) == 2 && args[0].Kind == KindString && args[1].Kind == KindString {
			return Bool(strings.HasPrefix(args[0].S, args[1].S)), true
		}
	}
	return Value{}, false
}

func valueDisplay(v Value) string {
	switch v.Kind {
	case KindString:
		return v.S
	case KindInt:
		return Int(v.I).String()
	case KindBool:
		if v.B {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func compareValues(op string, a, b Value) bool {
	switch op {
	case "=", "==":
		return valuesEqual(a, b)
	case "!=":
		return !valuesEqual(a, b)
	}
	if a.Kind != b.Kind {
		return false
	}
	var c int
	switch a.Kind {
	case KindInt:
		switch {
		case a.I < b.I:
			c = -1
		case a.I > b.I:
			c = 1
		}
	case KindString:
		c = strings.Compare(a.S, b.S)
	case KindBool:
		if a.B == b.B {
			c = 0
		} else if !a.B {
			c = -1
		} else {
			c = 1
		}
	default:
		return false
	}
	switch op {
	case ">":
		return c > 0
	case "<":
		return c < 0
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	}
	return false
}
