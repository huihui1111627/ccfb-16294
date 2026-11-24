package zupu

import "fmt"

type PredicateDiff struct {
	Predicate  string    `json:"predicate"`
	SemiCount  int       `json:"semiCount"`
	NaiveCount int       `json:"naiveCount"`
	Missing    [][]Value `json:"missingInSemi,omitempty"`
	Extra      [][]Value `json:"extraInSemi,omitempty"`
	Checked    bool      `json:"checked"`
}

type VerifyResult struct {
	OK                  bool            `json:"ok"`
	RuleSetID           string          `json:"ruleSetId"`
	RuleSetName         string          `json:"ruleSetName"`
	PredicateCount      int             `json:"predicateCount"`
	ComparedFactCount   int             `json:"comparedFactCount"`
	Diffs               []PredicateDiff `json:"diffs,omitempty"`
	Semi                *Report         `json:"semi"`
	Naive               *Report         `json:"naive"`
	SemiCandidateJoins  int64           `json:"semiCandidateJoins"`
	NaiveCandidateJoins int64           `json:"naiveCandidateJoins"`
	JoinSpeedup         float64         `json:"joinSpeedup"`
}

func VerifyRuleSet(facts []Fact, rs *RuleSet) (*VerifyResult, error) {
	prog, err := rs.Program(facts)
	if err != nil {
		return nil, err
	}
	semi, err := NewEngine(prog)
	if err != nil {
		return nil, fmt.Errorf("semi engine compile: %w", err)
	}
	semiReport, err := semi.EvaluateSemi(rs.ID, rs.Name)
	if err != nil {
		return nil, fmt.Errorf("semi evaluation: %w", err)
	}

	naive, err := NewEngine(prog)
	if err != nil {
		return nil, fmt.Errorf("naive engine compile: %w", err)
	}
	naiveReport, err := naive.EvaluateNaive(rs.ID, rs.Name)
	if err != nil {
		return nil, fmt.Errorf("naive evaluation: %w", err)
	}

	res := &VerifyResult{
		OK:                  true,
		RuleSetID:           rs.ID,
		RuleSetName:         rs.Name,
		PredicateCount:      len(prog.Predicates()),
		Semi:                semiReport,
		Naive:               naiveReport,
		SemiCandidateJoins:  semiReport.Totals.CandidateJoins,
		NaiveCandidateJoins: naiveReport.Totals.CandidateJoins,
	}
	if res.SemiCandidateJoins > 0 {
		res.JoinSpeedup = float64(res.NaiveCandidateJoins) / float64(res.SemiCandidateJoins)
	}
	for pred := range prog.Predicates() {
		sr := semi.Store.Relations[pred]
		nr := naive.Store.Relations[pred]
		diff := PredicateDiff{Predicate: pred, Checked: true}
		if sr != nil {
			diff.SemiCount = len(sr.Facts)
		}
		if nr != nil {
			diff.NaiveCount = len(nr.Facts)
		}
		if sr == nil || nr == nil {
			res.OK = false
			diff.Missing = [][]Value{}
			res.Diffs = append(res.Diffs, diff)
			continue
		}
		res.ComparedFactCount += len(nr.Facts)
		for key, f := range nr.Facts {
			if _, ok := sr.Facts[key]; !ok {
				res.OK = false
				if len(diff.Missing) < 10 {
					diff.Missing = append(diff.Missing, f.Values)
				}
			}
		}
		for key, f := range sr.Facts {
			if _, ok := nr.Facts[key]; !ok {
				res.OK = false
				if len(diff.Extra) < 10 {
					diff.Extra = append(diff.Extra, f.Values)
				}
			}
		}
		if diff.SemiCount != diff.NaiveCount || len(diff.Missing) > 0 || len(diff.Extra) > 0 {
			res.Diffs = append(res.Diffs, diff)
		}
	}
	return res, nil
}
