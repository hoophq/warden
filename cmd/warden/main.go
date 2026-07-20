// Warden: author, test and enforce-check Hoop guardrail rules.
//
//	warden test <file>...       validate rule files against their own examples (CI)
//	warden check <file> [q...]  probe ad-hoc queries against a rule file
//	warden generate             draft a rule with an LLM, proven before it lands
//	warden export <file>        curl that ships the rule to Hoop as a guardrail
//	warden scan [file]          detect PII in sample query output via Alcatraz
//
// The builder UI lives at https://hoop.dev/labs/warden. The binary is CLI-only.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hoophq/alcatraz/analyzer"
	"github.com/hoophq/warden/pkg/export"
	"github.com/hoophq/warden/pkg/generate"
	"github.com/hoophq/warden/pkg/match"
	"github.com/hoophq/warden/pkg/rules"
	"github.com/hoophq/warden/pkg/scan"
)

// version is stamped by the release build (homebrew/build.mjs) via
// -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

const usage = `Warden: author and test Hoop guardrail rules.

Usage:
  warden test [-json] <rule.yaml> [...]
                                     Run each rule file against its own
                                     must_block / must_allow examples.
                                     Exits 1 on any false positive/negative.
                                     -json emits structured results, made for
                                     agent loops that generate and refine rules.
  warden scan [-custom pack.yaml] [file]
                                     Detect PII in sample query output with
                                     Alcatraz (stdin when no file is given).
                                     -custom adds your own recognizers.
  warden check <rule.yaml> [query ...]
                                     Probe ad-hoc queries against a rule file:
                                     one verdict per query (stdin, one query
                                     per line, when none are given). Exits 3
                                     when any query would be blocked.
  warden generate -block "intent" [-query q] [-allow-query q] [-o rule.yaml]
                                     Draft a rule with an LLM (claude -p by
                                     default; override with -llm-cmd) and
                                     validate it against its own examples
                                     until every one behaves.
  warden export <rule.yaml>          Print a ready-to-run curl that creates
                                     the rule as a Hoop guardrail, reading
                                     api_url and token from a logged-in hoop
                                     CLI. Pipe to sh to ship it:
                                     warden export rule.yaml | sh

The builder UI lives at https://hoop.dev/labs/warden; it is not served by
this binary. test, check and scan run locally; nothing is sent anywhere.
generate sends the intent and the sample queries to the LLM command you
configure. export only prints the curl; nothing is sent until you run it.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "test":
		cmdTest(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "scan":
		cmdScan(os.Args[2:])
	case "generate":
		cmdGenerate(os.Args[2:])
	case "export":
		cmdExport(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println(version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// testResult is one example's verdict, emitted by `warden test -json`.
type testResult struct {
	File     string `json:"file"`
	Rule     string `json:"rule"`
	Example  string `json:"example"`
	Expected string `json:"expected"` // "block" or "allow"
	Got      string `json:"got"`      // "block" or "allow"
	Pass     bool   `json:"pass"`
}

type testReport struct {
	Pass     bool         `json:"pass"`
	Total    int          `json:"total"`
	Failures int          `json:"failures"`
	Results  []testResult `json:"results"`
	Error    string       `json:"error,omitempty"`
}

func cmdTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit structured results as JSON")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: warden test [-json] <rule.yaml> [...]")
		os.Exit(2)
	}

	verdict := func(blocked bool) string {
		if blocked {
			return "block"
		}
		return "allow"
	}
	report := testReport{Results: []testResult{}}
	fail := func(err error) {
		if *asJSON {
			report.Pass = false
			report.Error = err.Error()
			out, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(out))
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}

	for _, path := range fs.Args() {
		rf, err := rules.Load(path)
		if err != nil {
			fail(err)
		}
		if !*asJSON {
			fmt.Printf("==> %s (%s)\n", path, rf.Name)
		}
		run := func(queries []string, expectBlocked bool, label string) {
			if len(queries) == 0 {
				return
			}
			if !*asJSON {
				fmt.Printf("  %s:\n", label)
			}
			for _, q := range queries {
				report.Total++
				blocked, err := match.Blocked(rf, q)
				if err != nil {
					fail(err)
				}
				pass := blocked == expectBlocked
				if !pass {
					report.Failures++
				}
				report.Results = append(report.Results, testResult{
					File:     path,
					Rule:     rf.Name,
					Example:  q,
					Expected: verdict(expectBlocked),
					Got:      verdict(blocked),
					Pass:     pass,
				})
				if *asJSON {
					continue
				}
				if pass {
					fmt.Printf("    ok    %s\n", q)
				} else {
					msg := "SLIPS THROUGH (false negative)"
					if !expectBlocked {
						msg = "OVER-BLOCKED (false positive)"
					}
					fmt.Printf("    FAIL  %s: %s\n", q, msg)
				}
			}
		}
		run(rf.Examples.MustBlock, true, "must block")
		run(rf.Examples.MustAllow, false, "must allow")
	}

	report.Pass = report.Failures == 0
	if *asJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
	} else if report.Pass {
		fmt.Printf("\nPASS: %d examples\n", report.Total)
	} else {
		fmt.Printf("\nFAIL: %d of %d examples misbehaved\n", report.Failures, report.Total)
	}
	if !report.Pass {
		os.Exit(1)
	}
}

// cmdCheck probes ad-hoc queries against a rule file: the queries you did not
// think to commit as examples. Verdicts print one per query; exit code 3 means
// at least one query would be blocked, mirroring scan's "found something" code.
func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: warden check <rule.yaml> [query ...]  (stdin, one query per line, when no queries are given)")
		os.Exit(2)
	}

	rf, err := rules.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	blockedCount := 0
	totalCount := 0
	checkQuery := func(q string) {
		blocked, err := match.Blocked(rf, q)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		totalCount++
		if blocked {
			blockedCount++
			fmt.Printf("BLOCK  %s\n", q)
		} else {
			fmt.Printf("allow  %s\n", q)
		}
	}

	queries := fs.Args()[1:]
	if len(queries) == 0 {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				if q := strings.TrimSpace(line); q != "" {
					checkQuery(q)
				}
			}
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				break
			}
		}
	} else {
		for _, q := range queries {
			checkQuery(q)
		}
	}
	if totalCount == 0 {
		fmt.Fprintln(os.Stderr, "no queries to check")
		os.Exit(2)
	}

	fmt.Printf("\n%d of %d queries blocked by %s\n", blockedCount, totalCount, rf.Name)
	if blockedCount > 0 {
		if rf.Message != "" {
			fmt.Printf("message shown on block: %s\n", rf.Message)
		}
		os.Exit(3)
	}
}

// cmdExport prints the curl that creates the rule as a Hoop guardrail. It
// never sends anything itself; pipe to sh when you mean it.
func cmdExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: warden export <rule.yaml>")
		os.Exit(2)
	}

	rf, err := rules.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	cmd, err := export.Curl(rf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(cmd)
	fmt.Fprintln(os.Stderr, "\nRequires a logged-in hoop CLI (hoop login). Run it with:")
	fmt.Fprintf(os.Stderr, "  warden export %s | sh\n", fs.Arg(0))
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func cmdGenerate(args []string) {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	intent := fs.String("block", "", "what to block, in plain language (required)")
	out := fs.String("o", "", "write the passing rule file here (default: stdout)")
	llmCmd := fs.String("llm-cmd", "claude -p", "command that reads the prompt on stdin and prints the rule; run with sh -c")
	maxAttempts := fs.Int("max-attempts", 5, "generate-validate iterations before giving up")
	var blockQueries, allowQueries multiFlag
	fs.Var(&blockQueries, "query", "sample query that must be blocked (repeatable)")
	fs.Var(&allowQueries, "allow-query", "sample query that must stay allowed (repeatable)")
	fs.Parse(args)

	if *intent == "" {
		fmt.Fprintln(os.Stderr, `usage: warden generate -block "intent" [-query q] [-allow-query q] [-o rule.yaml]`)
		os.Exit(2)
	}

	res, err := generate.Run(generate.Options{
		Intent:       *intent,
		BlockQueries: blockQueries,
		AllowQueries: allowQueries,
		LLMCmd:       *llmCmd,
		MaxAttempts:  *maxAttempts,
		Log:          os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	nBlock := len(res.RuleFile.Examples.MustBlock)
	nAllow := len(res.RuleFile.Examples.MustAllow)
	if *out == "" {
		fmt.Print(string(res.YAML))
	} else {
		if err := os.WriteFile(*out, res.YAML, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
	}
	fmt.Fprintf(os.Stderr, "proven in %d attempt(s): %d must_block and %d must_allow examples behaved\n",
		res.Attempts, nBlock, nAllow)
}

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	custom := fs.String("custom", "", "YAML pack of custom recognizers to add")
	fs.Parse(args)

	var input []byte
	var err error
	if fs.NArg() > 0 {
		input, err = os.ReadFile(fs.Arg(0))
	} else {
		input, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	eng, err := scan.NewEngine(*custom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	results := eng.Analyze(string(input), analyzer.Options{})
	findings := scan.Aggregate(results)
	if len(findings) == 0 {
		fmt.Println("no PII detected")
		return
	}
	fmt.Printf("%-24s %-6s %-6s %s\n", "ENTITY", "COUNT", "SCORE", "SAMPLES (masked)")
	for _, f := range findings {
		samples := ""
		for i, s := range f.Samples {
			if i > 0 {
				samples += ", "
			}
			samples += s
		}
		fmt.Printf("%-24s %-6d %-6.2f %s\n", f.Entity, f.Count, f.MaxScore, samples)
	}
	fmt.Println("\nThese entities appear in your sample output. To redact or block them at the")
	fmt.Println("gateway, add output rules to your guardrail; the builder at")
	fmt.Println("https://hoop.dev/labs/warden exports them in Hoop format.")
	os.Exit(3) // distinct exit code: scan succeeded, PII found
}
