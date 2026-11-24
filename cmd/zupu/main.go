package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	zupu "zupu"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		verifyMain(os.Args[2:])
		return
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	serveMain(args)
}

func serveMain(argv []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8090", "HTTP listen address")
	people := fs.Int("people", 240000, "target number of generated people")
	seed := fs.Int64("seed", 20261125, "fixed generation seed")
	setID := fs.String("set", "genealogy-core", "initial ruleset")
	_ = fs.Parse(argv)

	fmt.Fprintf(os.Stderr, "generating deterministic family data: target=%d seed=%d\n", *people, *seed)
	t0 := time.Now()
	fd := zupu.GenerateFamily(zupu.FamilyConfig{People: *people, Seed: *seed})
	fmt.Fprintf(os.Stderr, "generated %d people and %d base facts in %s\n", fd.Config.People, len(fd.Facts), time.Since(t0).Round(time.Millisecond))

	sets := zupu.AllRuleSets(*seed)
	fmt.Fprintf(os.Stderr, "evaluating initial ruleset %s ...\n", *setID)
	evalStart := time.Now()
	srv, err := zupu.NewServer(fd, sets, *setID)
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "initial evaluation finished in %s\n", time.Since(evalStart).Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "zupu reasoning console: %s\n", consoleURL(*addr))
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		fatal(err)
	}
}

func verifyMain(argv []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	people := fs.Int("people", 240000, "target number of generated people")
	seed := fs.Int64("seed", 20261125, "fixed generation seed")
	only := fs.String("set", "all", "ruleset id, or 'all'")
	out := fs.String("json", "", "optional JSON report output path")
	_ = fs.Parse(argv)

	fd := zupu.GenerateFamily(zupu.FamilyConfig{People: *people, Seed: *seed})
	fmt.Fprintf(os.Stderr, "generated %d people, %d base facts\n", fd.Config.People, len(fd.Facts))
	all := zupu.AllRuleSets(*seed)
	results := []*zupu.VerifyResult{}
	failed := 0
	for _, rs := range all {
		if *only != "all" && rs.ID != *only {
			continue
		}
		start := time.Now()
		fmt.Fprintf(os.Stderr, "verifying %s ...\n", rs.ID)
		res, err := zupu.VerifyRuleSet(fd.Facts, rs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR: %v\n", err)
			failed++
			continue
		}
		results = append(results, res)
		status := "OK"
		if !res.OK {
			status = "MISMATCH"
			failed++
		}
		fmt.Fprintf(os.Stderr, "  %s facts=%d semiJoins=%d naiveJoins=%d speedup=%.2fx semi=%s naive=%s wall=%s\n",
			status, res.ComparedFactCount, res.SemiCandidateJoins, res.NaiveCandidateJoins, res.JoinSpeedup,
			roundMS(res.Semi.DurationMS), roundMS(res.Naive.DurationMS), time.Since(start).Round(time.Millisecond))
		if !res.OK {
			for _, d := range res.Diffs {
				fmt.Fprintf(os.Stderr, "    diff %s semi=%d naive=%d missing=%d extra=%d\n", d.Predicate, d.SemiCount, d.NaiveCount, len(d.Missing), len(d.Extra))
			}
		}
	}
	if *out != "" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fatal(err)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func roundMS(v float64) string {
	return (time.Duration(v*1e6) * time.Nanosecond).Round(time.Millisecond).String()
}

func consoleURL(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
