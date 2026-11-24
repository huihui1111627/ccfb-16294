package zupu

import (
	"testing"
)

func TestHandWrittenTransitiveClosure(t *testing.T) {
	prog, err := ParseProgram(`
edge("a","b").
edge("b","c").
edge("c","d").
anc(?x, ?y) :- edge(?x, ?y).
anc(?x, ?z) :- edge(?x, ?y), anc(?y, ?z).
`)
	if err != nil {
		t.Fatal(err)
	}
	semi, err := NewEngine(prog)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := semi.EvaluateSemi("test", "test")
	if err != nil {
		t.Fatal(err)
	}
	if got := semi.Store.FactCount("anc"); got != 6 {
		t.Fatalf("anc = %d facts, want 6; iterations=%+v", got, rep.Iterations)
	}
	for _, pair := range [][2]string{{"a", "d"}, {"b", "d"}, {"a", "c"}} {
		if semi.Store.RowMap("anc")[EncodeKey([]Value{String(pair[0]), String(pair[1])})] == nil {
			t.Fatalf("missing anc(%s,%s)", pair[0], pair[1])
		}
	}
}

func TestAggregateCountAndMin(t *testing.T) {
	prog, err := ParseProgram(`
father("c1","f1").
father("c2","f1").
father("c3","f2").
birth("c1",1990).
birth("c2",1988).
birth("c3",2001).
cb(?c,?f,?b) :- father(?c,?f), birth(?c,?b).
cnt(?f,?n) :- agg(count(?c), father(?c,?f), group(?f), ?n).
mn(?f,?m) :- agg(min(?b), cb(?c,?f,?b), group(?f), ?m).
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
		vals []Value
	}{
		{"cnt", []Value{String("f1"), Int(2)}},
		{"cnt", []Value{String("f2"), Int(1)}},
		{"mn", []Value{String("f1"), Int(1988)}},
		{"mn", []Value{String("f2"), Int(2001)}},
	}
	for _, c := range checks {
		if eng.Store.RowMap(c.pred)[EncodeKey(c.vals)] == nil {
			t.Fatalf("missing %s%v; all=%v", c.pred, c.vals, eng.Store.RowMap(c.pred))
		}
	}
}

func TestUnstratifiableNegationCycle(t *testing.T) {
	prog, err := ParseProgram(`
q("a").
p(?x) :- q(?x), not p(?x).
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(prog); err == nil {
		t.Fatal("expected stratification error for negation cycle")
	}
}

func TestBuiltinsAndWildcards(t *testing.T) {
	prog, err := ParseProgram(`
person("p1","李德明","M",1900,3,"陇西堂").
word(?p,?w) :- person(?p,?n,_,_,_,_), let ?w = substr(?n,1,1).
decade(?p,?d) :- person(?p,_,_,?b,_,_), let ?d = (?b / 10) * 10.
`)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.EvaluateSemi("b", "b"); err != nil {
		t.Fatal(err)
	}
	if eng.Store.RowMap("word")[EncodeKey([]Value{String("p1"), String("德")})] == nil {
		t.Fatal("generation character not derived")
	}
	if eng.Store.RowMap("decade")[EncodeKey([]Value{String("p1"), Int(1900)})] == nil {
		t.Fatal("decade not derived")
	}
}

func TestAllGeneratedRuleSetsDifferentialSmall(t *testing.T) {
	fd := GenerateFamily(FamilyConfig{People: 1600, Seed: 20261125})
	sets := AllRuleSets(20261125)
	for _, rs := range sets {
		t.Run(rs.ID, func(t *testing.T) {
			res, err := VerifyRuleSet(fd.Facts, rs)
			if err != nil {
				t.Fatal(err)
			}
			if !res.OK {
				for _, d := range res.Diffs {
					t.Errorf("diff %s semi=%d naive=%d missing=%v extra=%v", d.Predicate, d.SemiCount, d.NaiveCount, d.Missing, d.Extra)
				}
			}
			t.Logf("%s facts-compared=%d semi-joins=%d naive-joins=%d speedup=%.2fx",
				rs.ID, res.ComparedFactCount, res.SemiCandidateJoins, res.NaiveCandidateJoins, res.JoinSpeedup)
		})
	}
}
