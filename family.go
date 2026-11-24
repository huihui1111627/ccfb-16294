package zupu

import (
	"fmt"
	"math/rand"
)

type FamilyConfig struct {
	People        int   `json:"people"`
	Seed          int64 `json:"seed"`
	MaxGeneration int   `json:"maxGeneration"`
}

type FamilyData struct {
	Config       FamilyConfig `json:"config"`
	Facts        []Fact       `json:"-"`
	Clans        []string     `json:"clans"`
	OriginPlaces []string     `json:"originPlaces"`
	DestPlaces   []string     `json:"destPlaces"`
}

type clanInfo struct {
	Surname string
	Hall    string
	Poem    string
	Origin  string
}

var clanTable = []clanInfo{
	{"李", "陇西堂", "德宗光祖业世代永荣昌忠孝传家远诗书绍泽长", "甘肃陇西"},
	{"王", "太原堂", "文章开泰运孝友振家声立德承先志存仁启后英", "山西太原"},
	{"张", "清河堂", "清河衍泽长祖德永流芳孝友传家宝诗书裕后良", "河北清河"},
	{"刘", "彭城堂", "彭城世泽长礼乐振家邦忠义承先志仁厚启永昌", "江苏彭城"},
	{"陈", "颍川堂", "颍川绵世泽敦厚振家声祖德千秋远宗功万代荣", "河南颍川"},
	{"杨", "弘农堂", "弘农传清白世德作求长积善家余庆诗书国之光", "陕西弘农"},
	{"赵", "天水堂", "天水家声远琴鹤世泽长孝思惟则友于兄弟良", "甘肃天水"},
	{"黄", "江夏堂", "江夏源流远衣冠礼乐长忠贞昭日月孝友焕文章", "湖北江夏"},
	{"周", "汝南堂", "汝南绵世泽濂洛溯源长道学承先绪功德裕后昌", "河南汝南"},
	{"吴", "延陵堂", "延陵传礼让至德炳千秋世家推让德国史表贤侯", "江苏延陵"},
	{"徐", "东海堂", "东海家声远南州世泽长诗文承祖道孝友焕宗枋", "山东东海"},
	{"孙", "乐安堂", "乐安绵世泽兵法著奇勋忠孝传家远诗书启后昆", "山东乐安"},
}

var givenChars = []rune("仁义礼智信温良恭俭让明哲文武英才俊杰宏达安康泰祥瑞福寿康宁荣华富贵金玉满堂诗书礼乐")
var femaleChars = []rune("婉娴淑惠兰桂芳秀贞静怡萱瑶琴雪梅莲蓉薇岚凤莺燕玉珍珠珊琪琳")

var destPlaces = []string{
	"福建上杭", "广东梅州", "江西赣州", "四川成都", "重庆璧山", "湖南湘潭",
	"浙江金华", "江苏苏州", "台湾新竹", "广西玉林", "云南腾冲", "贵州遵义",
	"陕西西安", "北京宛平", "上海松江", "海南琼山", "香港新界", "南洋槟城",
}

type genPerson struct {
	ID    string
	Name  string
	Sex   string
	Birth int
	Gen   int
	Clan  int
}

// GenerateFamily creates a deterministic genealogical forest. Official father
// links reflect adoption; biofather preserves blood lineage.
func GenerateFamily(cfg FamilyConfig) *FamilyData {
	if cfg.People <= 0 {
		cfg.People = 240000
	}
	if cfg.Seed == 0 {
		cfg.Seed = 20261125
	}
	if cfg.MaxGeneration <= 0 {
		cfg.MaxGeneration = 18
	}
	r := rand.New(rand.NewSource(cfg.Seed))
	fd := &FamilyData{Config: cfg, DestPlaces: append([]string(nil), destPlaces...)}
	for _, c := range clanTable {
		fd.Clans = append(fd.Clans, c.Surname+c.Hall)
		fd.OriginPlaces = append(fd.OriginPlaces, c.Origin)
	}

	facts := []Fact{}
	add := func(pred string, args ...Value) {
		facts = append(facts, Fact{Pred: pred, Args: args})
	}
	people := map[string]*genPerson{}
	allIDs := []string{}
	nextSeq := 0
	newID := func() string {
		nextSeq++
		return fmt.Sprintf("p%07d", nextSeq)
	}
	makeName := func(clan int, gen int, sex string) string {
		info := clanTable[clan]
		poem := []rune(info.Poem)
		gi := gen
		if gi >= len(poem) {
			gi = len(poem) - 1
		}
		genChar := poem[gi]
		pool := givenChars
		if sex == "F" {
			pool = femaleChars
		}
		given := pool[r.Intn(len(pool))]
		// A small fraction gets a two-character given name; substr(name,1,1)
		// still extracts the generation character.
		if r.Float64() < 0.18 {
			given2 := pool[r.Intn(len(pool))]
			return info.Surname + string(genChar) + string(given) + string(given2)
		}
		return info.Surname + string(genChar) + string(given)
	}
	addPerson := func(clan, gen int, sex string, birth int) *genPerson {
		id := newID()
		p := &genPerson{ID: id, Name: makeName(clan, gen, sex), Sex: sex, Birth: birth, Gen: gen, Clan: clan}
		people[id] = p
		allIDs = append(allIDs, id)
		add("person", String(id), String(p.Name), String(sex), Int(int64(birth)), Int(int64(gen)), String(clanTable[clan].Surname+clanTable[clan].Hall))
		return p
	}

	// Scale roots with the requested size so small differential tests still
	// contain several generations instead of stopping at the root batch.
	rootsPerClan := cfg.People / (len(clanTable) * 28)
	if rootsPerClan < 2 {
		rootsPerClan = 2
	}
	rootMen := []*genPerson{}
	for c := range clanTable {
		fd.OriginPlaces[c] = clanTable[c].Origin
		for i := 0; i < rootsPerClan; i++ {
			birth := 1660 + r.Intn(70)
			p := addPerson(c, 0, "M", birth)
			rootMen = append(rootMen, p)
		}
	}
	menByGen := [][]*genPerson{}
	menByGen = append(menByGen, rootMen)

	officialFather := map[string]string{}
	bioFather := map[string]string{}
	for g := 0; g <= cfg.MaxGeneration; g++ {
		men := menByGen[g]
		if len(men) == 0 {
			break
		}
		nextMen := []*genPerson{}
		childrenThisGen := []*genPerson{}
		sonsByFather := map[string][]*genPerson{}
		// Clan-specific adoptable boys.
		adoptableByClan := map[int][]*genPerson{}

		reached := false
		for _, f := range men {
			if len(allIDs) >= cfg.People {
				reached = true
				break
			}
			// Most adult men have a recorded spouse.
			var wife *genPerson
			if r.Float64() < 0.94 {
				wifeBirth := f.Birth - 6 + r.Intn(13)
				wife = addPerson(f.Clan, f.Gen, "F", wifeBirth)
				marryYear := f.Birth + 20 + r.Intn(12)
				add("spouse", String(f.ID), String(wife.ID), Int(int64(marryYear)))
			}
			u := r.Float64()
			nChild := 0
			switch {
			case u < 0.15:
				nChild = 0
			case u < 0.39:
				nChild = 1
			case u < 0.69:
				nChild = 2
			case u < 0.88:
				nChild = 3
			case u < 0.97:
				nChild = 4
			default:
				nChild = 5 + r.Intn(3)
			}
			for i := 0; i < nChild; i++ {
				if len(allIDs) >= cfg.People {
					reached = true
					break
				}
				sex := "M"
				if r.Float64() < 0.49 {
					sex = "F"
				}
				birth := f.Birth + 22 + r.Intn(16)
				c := addPerson(f.Clan, g+1, sex, birth)
				bioFather[c.ID] = f.ID
				officialFather[c.ID] = f.ID
				add("biofather", String(c.ID), String(f.ID))
				if wife != nil {
					add("mother", String(c.ID), String(wife.ID))
				}
				childrenThisGen = append(childrenThisGen, c)
				if sex == "M" {
					nextMen = append(nextMen, c)
					sonsByFather[f.ID] = append(sonsByFather[f.ID], c)
				}
			}
			if reached {
				break
			}
		}

		// Adoption: childless men may adopt one surplus son from a same-clan
		// relative. Some lines remain broken because not every man adopts.
		for _, boy := range nextMen {
			bf := bioFather[boy.ID]
			if len(sonsByFather[bf]) >= 2 {
				adoptableByClan[boy.Clan] = append(adoptableByClan[boy.Clan], boy)
			}
		}
		adopted := map[string]bool{}
		for _, f := range men {
			if len(sonsByFather[f.ID]) > 0 {
				continue
			}
			if r.Float64() > 0.42 {
				continue
			}
			candidates := adoptableByClan[f.Clan]
			if len(candidates) == 0 {
				continue
			}
			boy := candidates[r.Intn(len(candidates))]
			if adopted[boy.ID] {
				continue
			}
			adopted[boy.ID] = true
			bf := bioFather[boy.ID]
			officialFather[boy.ID] = f.ID
			year := boy.Birth + r.Intn(9)
			add("adoption", String(boy.ID), String(bf), String(f.ID), Int(int64(year)))
		}

		for _, c := range childrenThisGen {
			add("father", String(c.ID), String(officialFather[c.ID]))
		}

		// Migration records.
		for _, p := range men {
			if r.Float64() < 0.11 {
				from := clanTable[p.Clan].Origin
				to := destPlaces[r.Intn(len(destPlaces))]
				year := p.Birth + 18 + r.Intn(35)
				add("migration", String(p.ID), String(from), String(to), Int(int64(year)))
			}
		}

		menByGen = append(menByGen, nextMen)
		if reached && g >= 2 {
			break
		}
		if len(allIDs) >= cfg.People && g >= 3 {
			break
		}
	}

	fd.Facts = facts
	fd.Config.People = len(allIDs)
	return fd
}
