package zupu

import (
	"strings"
	"testing"
)

func TestCompileRejectsWrongArityNegation(t *testing.T) {
	prog, err := ParseProgram(`
person("p1").
blocked("p2").
can(?p) :- person(?p), not blocked(?p, _).
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(prog); err == nil {
		t.Fatal("expected arity validation error for blocked/1 used as /2")
	}
}

func TestCompileRejectsUnsafeHeadVariable(t *testing.T) {
	prog, err := ParseProgram(`p("a"). q(?z) :- p(?x).`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(prog); err == nil {
		t.Fatal("expected unsafe head variable error")
	}
}

func TestUnknownBodyPredicateIsEmptyRelation(t *testing.T) {
	prog, err := ParseProgram(`p("a"). q(?x) :- p(?x), missing(?x).`)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.EvaluateSemi("t", "t"); err != nil {
		t.Fatal(err)
	}
	if eng.Store.FactCount("q") != 0 {
		t.Fatal("q must be empty because missing/1 is empty")
	}
}

func TestAggregateSourcePatternAndDistinctSum(t *testing.T) {
	prog, err := ParseProgram(`
entry("i1", "b", 2).
entry("i2", "b", 2).
entry("i3", "b", 3).
entry("i4", "c", 9).
cnt_b(?n) :- agg(count(?id), entry(?id, "b", ?v), group(), ?n).
sum_distinct_b(?n) :- agg(sum(distinct ?v), entry(?id, "b", ?v), group(), ?n).
repeat(?k, ?n) :- agg(count(?x), same(?k, ?x, ?x), group(?k), ?n).
same("k1", 7, 7).
same("k1", 8, 9).
`)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.EvaluateSemi("agg", "agg"); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		pred string
		val  Value
	}{
		{"cnt_b", Int(3)},
		{"sum_distinct_b", Int(5)},
	}
	for _, c := range checks {
		rows := eng.Store.RowMap(c.pred)
		if rows[EncodeKey([]Value{c.val})] == nil {
			for _, f := range rows {
				t.Logf("%s row %#v key=%q", c.pred, f.Values, EncodeKey(f.Values))
			}
			t.Fatalf("missing %s(%s), lookupKey=%q", c.pred, c.val, EncodeKey([]Value{c.val}))
		}
	}
	if eng.Store.RowMap("repeat")[EncodeKey([]Value{String("k1"), Int(1)})] == nil {
		t.Fatalf("repeated source variable should allow only (7,7); rows=%v", eng.Store.RowMap("repeat"))
	}
}

func TestProofAggregateTree(t *testing.T) {
	prog, err := ParseProgram(`
father("c1", "f1").
father("c2", "f1").
cnt(?f, ?n) :- agg(count(?c), father(?c, ?f), group(?f), ?n).
`)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.EvaluateSemi("proof", "proof"); err != nil {
		t.Fatal(err)
	}
	p, err := eng.Prove("cnt", []Value{String("f1"), Int(2)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var hasAgg bool
	var walk func(*ProofNode)
	walk = func(n *ProofNode) {
		if n.Kind == "aggregate" {
			hasAgg = true
		}
		if strings.Contains(n.Label, "COUNT") == false && n.Kind == "aggregate" {
			t.Fatalf("aggregate label missing COUNT: %q", n.Label)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(p)
	if !hasAgg {
		t.Fatal("proof tree does not contain aggregate node")
	}
}
