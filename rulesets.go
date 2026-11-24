package zupu

import (
	"fmt"
	"math/rand"
	"strings"
)

type RuleSet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Seed        int64  `json:"seed,omitempty"`
	Text        string `json:"text"`
}

func (rs *RuleSet) ParsedRules() ([]Rule, error) {
	p, err := ParseProgram(rs.Text)
	if err != nil {
		return nil, fmt.Errorf("ruleset %s: %w", rs.ID, err)
	}
	return p.Rules, nil
}

func (rs *RuleSet) Program(facts []Fact) (*Program, error) {
	rules, err := rs.ParsedRules()
	if err != nil {
		return nil, err
	}
	return &Program{Facts: facts, Rules: rules}, nil
}

const coreRuleText = `
%% 基础性别与父子关系
male(?p) :- person(?p, _, "M", _, _, _).
female(?p) :- person(?p, _, "F", _, _, _).
son(?c, ?f) :- father(?c, ?f), male(?c).
has_son(?f) :- son(?c, ?f).

%% 线性递归：完整祖先链
ancestor(?x, ?y) :- father(?x, ?y).
ancestor(?x, ?z) :- father(?x, ?y), ancestor(?y, ?z).

%% 分层有界距离，包含算术和比较
dist(?x, ?y, 1) :- father(?x, ?y).
dist(?x, ?z, ?d) :- father(?x, ?y), dist(?y, ?z, ?d0), let ?d = ?d0 + 1, ?d <= 8.

short_dist(?x, ?y, 1) :- father(?x, ?y).
short_dist(?x, ?z, ?d) :- father(?x, ?y), short_dist(?y, ?z, ?d0), let ?d = ?d0 + 1, ?d <= 3.

near_tongzong(?x, ?y, ?total) :-
  short_dist(?x, ?a, ?dx),
  short_dist(?y, ?a, ?dy),
  ?x != ?y,
  let ?total = ?dx + ?dy,
  ?total <= 4.

%% 内置字符串/算术函数
generation_char(?p, ?w) :- person(?p, ?n, _, _, ?g, _), let ?w = substr(?n, 1, 1).
name_length(?p, ?n) :- person(?p, ?full, _, _, _, _), let ?n = length(?full).
generation_gap(?x, ?y, ?gap) :-
  ancestor(?x, ?y),
  person(?x, _, _, _, ?gx, _),
  person(?y, _, _, _, ?gy, _),
  let ?gap = ?gy - ?gx.
clan_label(?p, ?label) :-
  person(?p, ?n, _, ?b, ?g, ?clan),
  let ?label = concat(?clan, "|", ?n, "|G", ?g, "|", ?b).

%% 否定：必须等 has_son / migration 等关系完全闭合
no_son(?f) :- male(?f), not has_son(?f).
root_man(?p) :- male(?p), not father(?p, _).
never_migrated(?p) :- person(?p, _, _, _, _, _), not migration(?p, _, _, _).

%% 男性支系多重/传递递归，然后用否定找断嗣
male_desc(?d, ?a) :- son(?d, ?a).
male_desc(?d, ?a) :- son(?d, ?p), male_desc(?p, ?a).
has_male_desc(?a) :- male_desc(?d, ?a).
extinct_branch(?a) :- male(?a), not has_male_desc(?a).

%% 聚合：源关系必须先完成，聚合头位于更高分层
child_count(?f, ?n) :- agg(count(?c), father(?c, ?f), group(?f), ?n).
son_count(?f, ?n) :- agg(count(?c), son(?c, ?f), group(?f), ?n).
male_desc_count(?a, ?n) :- agg(count(?d), male_desc(?d, ?a), group(?a), ?n).
migration_to_count(?place, ?n) :- agg(count(?p), migration(?p, _, ?place, _), group(?place), ?n).
child_birth(?c, ?f, ?b) :- father(?c, ?f), person(?c, _, _, ?b, _, _).
earliest_child_birth(?f, ?m) :- agg(min(?b), child_birth(?c, ?f, ?b), group(?f), ?m).
`

func CoreRuleSet() *RuleSet {
	return &RuleSet{
		ID:          "genealogy-core",
		Name:        "族谱核心规则",
		Kind:        "core",
		Description: "祖先/距离/同宗递归、断嗣否定、计数与最小值聚合、字辈截取和辈分差。",
		Text:        coreRuleText,
	}
}

func AllRuleSets(seed int64) []*RuleSet {
	out := []*RuleSet{CoreRuleSet()}
	out = append(out, RandomRuleSets(seed, 10)...)
	return out
}

// RandomRuleSets builds syntactically varied but semantically safe programs.
// Randomness selects bounded templates and constants; every generated program
// remains stratifiable and range-restricted.
func RandomRuleSets(seed int64, count int) []*RuleSet {
	r := rand.New(rand.NewSource(seed + 7717))
	out := make([]*RuleSet, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("random-%02d", i+1)
		out = append(out, randomOne(r, id, fmt.Sprintf("随机规则集 %02d", i+1), seed))
	}
	return out
}

func randomOne(r *rand.Rand, id, name string, baseSeed int64) *RuleSet {
	var b strings.Builder
	emit := func(s string) { b.WriteString(strings.TrimSpace(s)); b.WriteString("\n") }

	clan := clanTable[r.Intn(len(clanTable))]
	place := destPlaces[r.Intn(len(destPlaces))]
	year := 1880 + r.Intn(100)
	bound := 2 + r.Intn(4)

	emit(`male(?p) :- person(?p, _, "M", _, _, _).`)
	emit(`son(?c, ?f) :- father(?c, ?f), male(?c).`)
	emit(`has_son(?f) :- son(?c, ?f).`)

	// Always include at least two recursion shapes in each random set.
	switch r.Intn(3) {
	case 0:
		emit(`r_anc(?x, ?y) :- father(?x, ?y).`)
		emit(`r_anc(?x, ?z) :- father(?x, ?y), r_anc(?y, ?z).`)
		emit(`b_anc(?x, ?y) :- biofather(?x, ?y).`)
		emit(`b_anc(?x, ?z) :- biofather(?x, ?y), b_anc(?y, ?z).`)
	case 1:
		emit(`up_a(?x, ?z) :- father(?x, ?z).`)
		emit(`up_a(?x, ?z) :- father(?x, ?y), up_b(?y, ?z).`)
		emit(`up_b(?x, ?z) :- father(?x, ?z).`)
		emit(`up_b(?x, ?z) :- father(?x, ?y), up_a(?y, ?z).`)
		// Keep the common r_anc name available to shared built-in templates.
		emit(`r_anc(?x, ?y) :- up_a(?x, ?y).`)
	default:
		emit(`rdist(?x, ?y, 1) :- father(?x, ?y).`)
		emit(fmt.Sprintf(`rdist(?x, ?z, ?d) :- father(?x, ?y), rdist(?y, ?z, ?d0),
			let ?d = ?d0 + 1, ?d <= %d.`, bound))
		emit(`r_anc(?x, ?y) :- rdist(?x, ?y, _).`)
		emit(`b_anc(?x, ?y) :- biofather(?x, ?y).`)
		emit(`b_anc(?x, ?z) :- biofather(?x, ?y), b_anc(?y, ?z).`)
	}

	// Comparison and built-in functions.
	builtinChoice := r.Intn(4)
	switch builtinChoice {
	case 0:
		emit(`gen_word(?p, ?w) :- person(?p, ?n, _, _, _, _), let ?w = substr(?n, 1, 1).`)
	case 1:
		emit(`name_len(?p, ?n) :- person(?p, ?full, _, _, _, _), let ?n = length(?full).`)
	case 2:
		emit(`birth_decade(?p, ?d) :- person(?p, _, _, ?b, _, _), let ?d = (?b / 10) * 10.`)
	default:
		emit(`gen_gap(?x, ?y, ?gap) :- r_anc(?x, ?y),
			person(?x, _, _, _, ?gx, _), person(?y, _, _, _, ?gy, _),
			let ?gap = ?gy - ?gx.`)
	}

	// Constant + comparison filter.
	switch r.Intn(3) {
	case 0:
		emit(fmt.Sprintf(`clan_members(?p) :- person(?p, _, _, _, _, %q).`, clan.Surname+clan.Hall))
	case 1:
		emit(fmt.Sprintf(`moved_to_place(?p) :- migration(?p, _, %q, _).`, place))
	default:
		emit(fmt.Sprintf(`born_after_year(?p) :- person(?p, _, _, ?b, _, _), ?b >= %d.`, year))
	}

	// Stratified negation.
	switch r.Intn(4) {
	case 0:
		emit(`random_no_son(?f) :- male(?f), not has_son(?f).`)
	case 1:
		emit(`random_root(?p) :- male(?p), not father(?p, _).`)
	case 2:
		emit(`random_stay(?p) :- person(?p, _, _, _, _, _), not migration(?p, _, _, _).`)
	default:
		emit(`adopted_boy(?c) :- adoption(?c, _, _, _).`)
		emit(`random_not_adopted(?p) :- person(?p, _, _, _, _, _), not adopted_boy(?p).`)
	}

	// Male-line recursion + terminal negation in half of sets.
	maleDesc := r.Intn(2) == 0
	if maleDesc {
		emit(`male_desc(?d, ?a) :- son(?d, ?a).`)
		emit(`male_desc(?d, ?a) :- son(?d, ?p), male_desc(?p, ?a).`)
		emit(`has_male_desc(?a) :- male_desc(?d, ?a).`)
		emit(`random_extinct(?a) :- male(?a), not has_male_desc(?a).`)
	}

	// At least one aggregate; often add a second.
	aggChoice := r.Intn(4)
	switch aggChoice {
	case 0:
		emit(`random_child_count(?f, ?n) :- agg(count(?c), father(?c, ?f), group(?f), ?n).`)
	case 1:
		emit(`random_son_count(?f, ?n) :- agg(count(?c), son(?c, ?f), group(?f), ?n).`)
	case 2:
		emit(`random_adopt_count(?f, ?n) :- agg(count(?c), adoption(?c, _, ?f, _), group(?f), ?n).`)
	default:
		emit(`random_move_count(?place, ?n) :- agg(count(?p), migration(?p, _, ?place, _), group(?place), ?n).`)
	}
	if r.Intn(2) == 0 {
		emit(`random_child_birth(?c, ?f, ?b) :- father(?c, ?f), person(?c, _, _, ?b, _, _).`)
		emit(`random_min_birth(?f, ?m) :- agg(min(?b), random_child_birth(?c, ?f, ?b), group(?f), ?m).`)
	}
	if maleDesc && r.Intn(3) == 0 {
		emit(`random_desc_count(?a, ?n) :- agg(count(?d), male_desc(?d, ?a), group(?a), ?n).`)
	}

	return &RuleSet{
		ID:          id,
		Name:        name,
		Kind:        "random",
		Seed:        baseSeed,
		Description: fmt.Sprintf("种子 %d 固定生成；递归上界 %d，常量 %q/%q/%d。", baseSeed, bound, clan.Surname+clan.Hall, place, year),
		Text:        b.String(),
	}
}
