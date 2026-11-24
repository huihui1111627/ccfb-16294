package zupu

import (
	"fmt"
	"sort"
	"sync"
)

type Row struct {
	Values []Value
}

type Index struct {
	Cols    []int
	entries map[string][]*Row
	lookups int64
}

type Relation struct {
	Name    string
	Arity   int
	Facts   map[string]*Row
	indexes []*Index
}

type Store struct {
	mu        sync.RWMutex
	Relations map[string]*Relation
}

func NewStore(schema map[string]int) *Store {
	s := &Store{Relations: map[string]*Relation{}}
	for name, arity := range schema {
		s.Relations[name] = &Relation{
			Name:  name,
			Arity: arity,
			Facts: map[string]*Row{},
		}
	}
	return s
}

func (s *Store) relation(name string) (*Relation, error) {
	r, ok := s.Relations[name]
	if !ok {
		return nil, fmt.Errorf("unknown predicate %q", name)
	}
	return r, nil
}

// Insert adds a fact and reports whether it was new. The encoded key is the
// single source of set uniqueness for a relation.
func (s *Store) Insert(name string, values []Value) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.Relations[name]
	if !ok {
		return false, fmt.Errorf("unknown predicate %q", name)
	}
	if len(values) != r.Arity {
		return false, fmt.Errorf("%s expects %d arguments, got %d", name, r.Arity, len(values))
	}
	key := EncodeKey(values)
	if _, exists := r.Facts[key]; exists {
		return false, nil
	}
	f := &Row{Values: values}
	r.Facts[key] = f
	for _, idx := range r.indexes {
		k := indexKey(idx.Cols, values)
		idx.entries[k] = append(idx.entries[k], f)
	}
	return true, nil
}

func (s *Store) MustInsert(name string, values ...Value) {
	if _, err := s.Insert(name, values); err != nil {
		panic(err)
	}
}

func indexKey(cols []int, values []Value) string {
	part := make([]Value, 0, len(cols))
	for _, c := range cols {
		part = append(part, values[c])
	}
	return EncodeKey(part)
}

// EnsureIndex creates an index on an arbitrary non-empty parameter set.
// Column order is canonicalized ascending.
func (s *Store) EnsureIndex(name string, cols []int) (*Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.relation(name)
	if err != nil {
		return nil, err
	}
	clean := append([]int(nil), cols...)
	if len(clean) == 0 {
		return nil, fmt.Errorf("index requires at least one parameter")
	}
	sort.Ints(clean)
	for i := 1; i < len(clean); i++ {
		if clean[i] == clean[i-1] {
			return nil, fmt.Errorf("duplicate index column %d", clean[i])
		}
	}
	for _, c := range clean {
		if c < 0 || c >= r.Arity {
			return nil, fmt.Errorf("%s has no parameter %d", name, c)
		}
	}
	for _, idx := range r.indexes {
		if equalInts(idx.Cols, clean) {
			return idx, nil
		}
	}
	idx := &Index{Cols: clean, entries: map[string][]*Row{}}
	for _, f := range r.Facts {
		k := indexKey(clean, f.Values)
		idx.entries[k] = append(idx.entries[k], f)
	}
	r.indexes = append(r.indexes, idx)
	return idx, nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *Relation) findIndex(cols []int) *Index {
	for _, idx := range r.indexes {
		if equalInts(idx.Cols, cols) {
			return idx
		}
	}
	return nil
}

// BestIndex returns an existing index whose columns are all bound. Prefer the
// longest composite key because it narrows candidates most.
func (s *Store) BestIndex(name string, bound map[int]Value) (*Index, []Value) {
	r, ok := s.Relations[name]
	if !ok {
		return nil, nil
	}
	var best *Index
	for _, idx := range r.indexes {
		ok := true
		for _, c := range idx.Cols {
			if _, bound := bound[c]; !bound {
				ok = false
				break
			}
		}
		if ok && (best == nil || len(idx.Cols) > len(best.Cols)) {
			best = idx
		}
	}
	if best == nil {
		return nil, nil
	}
	vals := make([]Value, len(best.Cols))
	for i, c := range best.Cols {
		vals[i] = bound[c]
	}
	return best, vals
}

func (s *Store) Lookup(name string, cols []int, vals []Value) ([][]*Row, error) {
	idx, err := s.EnsureIndex(name, cols)
	if err != nil {
		return nil, err
	}
	if len(vals) != len(cols) {
		return nil, fmt.Errorf("got %d index values for %d columns", len(vals), len(cols))
	}
	key := EncodeKey(vals)
	return [][]*Row{idx.entries[key]}, nil
}

// Query performs an indexed exact query when args contains non-nil Values.
// A nil entry means that parameter is unbound. If several parameters are
// bound, a composite index is created on exactly those parameters.
func (s *Store) Query(pred string, args []*Value) ([]*Row, *QueryPlan, error) {
	r, ok := s.Relations[pred]
	if !ok {
		return nil, nil, fmt.Errorf("unknown predicate %q", pred)
	}
	if len(args) != r.Arity {
		return nil, nil, fmt.Errorf("%s expects %d arguments, got %d", pred, r.Arity, len(args))
	}
	cols := []int{}
	vals := []Value{}
	for i, a := range args {
		if a != nil {
			cols = append(cols, i)
			vals = append(vals, *a)
		}
	}
	plan := &QueryPlan{Predicate: pred, BoundColumns: cols}
	if len(cols) == 0 {
		s.mu.RLock()
		out := make([]*Row, 0, len(r.Facts))
		for _, f := range r.Facts {
			out = append(out, f)
		}
		s.mu.RUnlock()
		plan.Access = "full_scan"
		plan.Examined = int64(len(out))
		return out, plan, nil
	}
	idx, err := s.EnsureIndex(pred, cols)
	if err != nil {
		return nil, nil, err
	}
	s.mu.RLock()
	key := EncodeKey(vals)
	out := append([]*Row(nil), idx.entries[key]...)
	idx.lookups++
	s.mu.RUnlock()
	plan.Access = "index"
	plan.IndexColumns = idx.Cols
	plan.Examined = int64(len(out))
	return out, plan, nil
}

type QueryPlan struct {
	Predicate    string `json:"predicate"`
	Access       string `json:"access"`
	BoundColumns []int  `json:"boundColumns"`
	IndexColumns []int  `json:"indexColumns,omitempty"`
	Examined     int64  `json:"examined"`
}

func (s *Store) RowMap(pred string) map[string]*Row {
	if r, ok := s.Relations[pred]; ok {
		return r.Facts
	}
	return nil
}

func (s *Store) FactCount(pred string) int {
	if r, ok := s.Relations[pred]; ok {
		return len(r.Facts)
	}
	return 0
}

func (s *Store) TotalFacts() int {
	n := 0
	for _, r := range s.Relations {
		n += len(r.Facts)
	}
	return n
}

func (s *Store) IndexInfo() map[string][][]int {
	out := map[string][][]int{}
	for name, r := range s.Relations {
		for _, idx := range r.indexes {
			out[name] = append(out[name], append([]int(nil), idx.Cols...))
		}
	}
	return out
}
