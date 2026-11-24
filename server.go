package zupu

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

//go:embed web/index.html
var staticFS embed.FS

type Server struct {
	mu       sync.Mutex
	fd       *FamilyData
	sets     []*RuleSet
	byID     map[string]*RuleSet
	activeID string
	engine   *Engine
	report   *Report
	verify   map[string]*VerifyResult
}

func NewServer(fd *FamilyData, sets []*RuleSet, activeID string) (*Server, error) {
	s := &Server{
		fd:     fd,
		sets:   sets,
		byID:   map[string]*RuleSet{},
		verify: map[string]*VerifyResult{},
	}
	for _, rs := range sets {
		s.byID[rs.ID] = rs
	}
	if activeID == "" {
		activeID = "genealogy-core"
	}
	if err := s.activate(activeID); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) activate(id string) error {
	rs, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("unknown ruleset %q", id)
	}
	prog, err := rs.Program(s.fd.Facts)
	if err != nil {
		return err
	}
	eng, err := NewEngine(prog)
	if err != nil {
		return err
	}
	rep, err := eng.EvaluateSemi(rs.ID, rs.Name)
	if err != nil {
		return err
	}
	s.engine = eng
	s.report = rep
	s.activeID = id
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/rule-sets", s.handleRuleSets)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/activate", s.handleActivate)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/facts", s.handleFacts)
	mux.HandleFunc("/api/indexes", s.handleIndexes)
	mux.HandleFunc("/api/query", s.handleQuery)
	mux.HandleFunc("/api/proof", s.handleProof)
	mux.HandleFunc("/api/verify", s.handleVerify)
	return withCORS(mux)
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := staticFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

type ruleSetMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
}

func (s *Server) handleRuleSets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ruleSetMeta, 0, len(s.sets))
	for _, rs := range s.sets {
		out = append(out, ruleSetMeta{ID: rs.ID, Name: rs.Name, Kind: rs.Kind, Description: rs.Description, Active: rs.ID == s.activeID})
	}
	writeJSON(w, out)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{
		"family":       s.fd.Config,
		"clans":        s.fd.Clans,
		"activeId":     s.activeID,
		"report":       s.report,
		"indexes":      s.engine.Store.IndexInfo(),
		"predicates":   s.engine.Schema,
		"verifyCached": s.verify,
	})
}

type activateReq struct {
	RuleSetID string `json:"ruleSetId"`
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	var req activateReq
	if r.Method != http.MethodPost {
		writeError(w, fmt.Errorf("POST required"), http.StatusMethodNotAllowed)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.activate(req.RuleSetID); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, s.report)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.engine.RuleGraph())
}

func (s *Server) handleFacts(w http.ResponseWriter, r *http.Request) {
	pred := r.URL.Query().Get("pred")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rel, ok := s.engine.Store.Relations[pred]
	if !ok {
		writeError(w, fmt.Errorf("unknown predicate %q", pred), http.StatusBadRequest)
		return
	}
	out := [][]Value{}
	for _, f := range rel.Facts {
		out = append(out, f.Values)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, map[string]any{"predicate": pred, "arity": rel.Arity, "total": len(rel.Facts), "rows": out})
}

type indexReq struct {
	Predicate string `json:"predicate"`
	Columns   []int  `json:"columns"`
}

func (s *Server) handleIndexes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodPost {
		var req indexReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, err, http.StatusBadRequest)
			return
		}
		if _, err := s.engine.Store.EnsureIndex(req.Predicate, req.Columns); err != nil {
			writeError(w, err, http.StatusBadRequest)
			return
		}
	}
	writeJSON(w, s.engine.Store.IndexInfo())
}

type queryRequest struct {
	Predicate string `json:"predicate"`
	Args      []any  `json:"args"`
	Columns   []int  `json:"columns,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if r.Method != http.MethodPost {
		writeError(w, fmt.Errorf("POST required"), http.StatusMethodNotAllowed)
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(req.Columns) > 0 {
		if _, err := s.engine.Store.EnsureIndex(req.Predicate, req.Columns); err != nil {
			writeError(w, err, http.StatusBadRequest)
			return
		}
	}
	args := make([]*Value, len(req.Args))
	for i, a := range req.Args {
		if a == nil {
			continue
		}
		v, err := ValueFromAny(a)
		if err != nil {
			writeError(w, err, http.StatusBadRequest)
			return
		}
		args[i] = &v
	}
	rows, plan, err := s.engine.Store.Query(req.Predicate, args)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	limit := req.Limit
	if limit == 0 {
		limit = 200
	}
	if limit < 0 || limit > 1000 {
		limit = 1000
	}
	totalMatched := len(rows)
	showN := totalMatched
	truncated := false
	if showN > limit {
		showN = limit
		truncated = true
	}
	vals := make([][]Value, 0, showN)
	for _, row := range rows[:showN] {
		vals = append(vals, row.Values)
	}
	writeJSON(w, map[string]any{
		"plan":         plan,
		"count":        len(vals),
		"totalMatched": totalMatched,
		"limit":        limit,
		"truncated":    truncated,
		"rows":         vals,
	})
}

func (s *Server) handleProof(w http.ResponseWriter, r *http.Request) {
	pred := r.URL.Query().Get("pred")
	argsRaw := r.URL.Query().Get("args")
	limit := 100
	if v := r.URL.Query().Get("aggLimit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	args, err := ParseJSONArgs(argsRaw)
	if err != nil {
		writeError(w, fmt.Errorf("args must be a JSON array: %w", err), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proof, err := s.engine.Prove(pred, args, limit)
	if err != nil {
		writeError(w, err, http.StatusNotFound)
		return
	}
	writeJSON(w, proof)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("set")
	if id == "" {
		s.mu.Lock()
		id = s.activeID
		s.mu.Unlock()
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	s.mu.Lock()
	rs := s.byID[id]
	cached := s.verify[id]
	s.mu.Unlock()
	if rs == nil {
		writeError(w, fmt.Errorf("unknown ruleset %q", id), http.StatusBadRequest)
		return
	}
	if cached != nil && !refresh {
		writeJSON(w, cached)
		return
	}
	res, err := VerifyRuleSet(s.fd.Facts, rs)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.verify[id] = res
	s.mu.Unlock()
	writeJSON(w, res)
}
