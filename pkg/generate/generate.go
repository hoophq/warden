// Package generate drafts a Warden rule file with an LLM and proves it with
// the same matcher `warden test` runs. The LLM is any command that reads a
// prompt on stdin and writes text to stdout (Claude Code headless,
// `claude -p`, by default). Each attempt is parsed, checked against its own
// must_block / must_allow examples via pkg/match, and on failure the misses
// are fed back into the next attempt. A rule is only returned after every
// example behaves.
package generate

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/hoophq/warden/pkg/match"
	"github.com/hoophq/warden/pkg/rules"
)

// Options configure one generation run.
type Options struct {
	// Intent is the plain-language description of what to block.
	Intent string
	// BlockQueries must end up blocked; they are required verbatim in the
	// rule file's must_block examples.
	BlockQueries []string
	// AllowQueries must stay allowed; required verbatim in must_allow.
	AllowQueries []string
	// LLMCmd is run with `sh -c`, receives the prompt on stdin and must
	// print its answer to stdout.
	LLMCmd string
	// MaxAttempts bounds the generate-validate loop.
	MaxAttempts int
	// Log receives one progress line per attempt. Nil discards.
	Log io.Writer
}

// Result is a validated rule file.
type Result struct {
	YAML     []byte
	RuleFile *rules.RuleFile
	Attempts int
}

const promptHeader = `You write Hoop guardrail rules. Produce ONE Warden rule file: a YAML document, inside a single fenced code block, and nothing else. No prose before or after.

Schema:

` + "```yaml" + `
name: "short-kebab-name"
description: "one sentence"
message: "shown to the user when blocked"
rules:
  - type: "pattern_match"
    pattern_regex: "(?is)..."
examples:
  must_block:
    - "query that must be blocked"
  must_allow:
    - "query that must pass"
` + "```" + `

Hard constraints on pattern_regex:
- Go RE2 syntax only: NO lookahead (?= (?!, NO lookbehind, NO backreferences \1.
- Matching is unanchored and case-sensitive by default; start with (?is) for case-insensitive, dot-matches-newline SQL rules.
- Put \b word boundaries around keywords so "drop" does not match "backdrop".
- Inside YAML double quotes a backslash must be doubled: write \\b for \b.
- For exact substrings prefer type "deny_words_list" with a words: list (case-sensitive contains) instead of a regex.

Examples requirements:
- At least 2 must_block and 2 must_allow entries.
- must_allow needs near-misses: queries that look like the blocked ones but are fine.
- Include every user-supplied query below verbatim in the right list.`

func buildPrompt(opts Options, prevYAML string, feedback []string) string {
	var b strings.Builder
	b.WriteString(promptHeader)
	b.WriteString("\n\nTask: write a rule that blocks: ")
	b.WriteString(opts.Intent)
	b.WriteString("\n")
	for _, q := range opts.BlockQueries {
		fmt.Fprintf(&b, "\nUser query that MUST be blocked (copy verbatim into must_block):\n%s\n", q)
	}
	for _, q := range opts.AllowQueries {
		fmt.Fprintf(&b, "\nUser query that MUST stay allowed (copy verbatim into must_allow):\n%s\n", q)
	}
	if prevYAML != "" {
		b.WriteString("\nYour previous attempt failed validation. Previous attempt:\n\n```yaml\n")
		b.WriteString(prevYAML)
		b.WriteString("\n```\n\nValidation failures (fix the pattern, keep the examples):\n")
		for _, f := range feedback {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func callLLM(cmd, prompt string) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	c.Stdin = strings.NewReader(prompt)
	var out, errOut bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errOut
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg != "" {
			return "", fmt.Errorf("llm command failed: %v: %s", err, msg)
		}
		return "", fmt.Errorf("llm command failed: %v", err)
	}
	return out.String(), nil
}

var fenceRe = regexp.MustCompile("(?s)```(?:yaml|yml)?\\s*\\n(.*?)\\n```")

// extractYAML pulls the rule document out of the LLM's reply: the first
// fenced code block if present, otherwise the whole trimmed output.
func extractYAML(reply string) string {
	if m := fenceRe.FindStringSubmatch(reply); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(reply)
}

// validate parses the document and runs it against its own examples plus the
// user-supplied queries. It returns the parsed file and a list of failures,
// empty when the rule is proven.
func validate(doc string, opts Options) (*rules.RuleFile, []string) {
	rf, err := rules.Parse([]byte(doc), "attempt")
	if err != nil {
		return nil, []string{err.Error()}
	}
	var feedback []string

	contains := func(list []string, q string) bool {
		for _, e := range list {
			if e == q {
				return true
			}
		}
		return false
	}
	for _, q := range opts.BlockQueries {
		if !contains(rf.Examples.MustBlock, q) {
			feedback = append(feedback, fmt.Sprintf("must_block is missing the user query verbatim: %s", q))
		}
	}
	for _, q := range opts.AllowQueries {
		if !contains(rf.Examples.MustAllow, q) {
			feedback = append(feedback, fmt.Sprintf("must_allow is missing the user query verbatim: %s", q))
		}
	}
	if len(rf.Examples.MustBlock) < 2 {
		feedback = append(feedback, "examples.must_block needs at least 2 entries")
	}
	if len(rf.Examples.MustAllow) < 2 {
		feedback = append(feedback, "examples.must_allow needs at least 2 entries (include near-misses)")
	}

	check := func(q string, expectBlocked bool) {
		blocked, err := match.Blocked(rf, q)
		if err != nil {
			feedback = append(feedback, err.Error())
			return
		}
		if blocked == expectBlocked {
			return
		}
		if expectBlocked {
			feedback = append(feedback, fmt.Sprintf("SLIPS THROUGH (expected block, got allow): %s", q))
		} else {
			feedback = append(feedback, fmt.Sprintf("OVER-BLOCKED (expected allow, got block): %s", q))
		}
	}
	for _, q := range rf.Examples.MustBlock {
		check(q, true)
	}
	for _, q := range rf.Examples.MustAllow {
		check(q, false)
	}
	return rf, feedback
}

// Run executes the generate-validate loop and returns the first attempt that
// passes every example, or an error carrying the last feedback.
func Run(opts Options) (*Result, error) {
	if opts.Intent == "" {
		return nil, fmt.Errorf("intent is required")
	}
	if opts.LLMCmd == "" {
		opts.LLMCmd = "claude -p"
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}

	prevYAML := ""
	var feedback []string
	for attempt := 1; attempt <= opts.MaxAttempts; attempt++ {
		fmt.Fprintf(log, "attempt %d/%d: asking the LLM\n", attempt, opts.MaxAttempts)
		reply, err := callLLM(opts.LLMCmd, buildPrompt(opts, prevYAML, feedback))
		if err != nil {
			return nil, err
		}
		doc := extractYAML(reply)
		if doc == "" {
			prevYAML, feedback = "", []string{"the reply contained no YAML document"}
			continue
		}
		rf, fb := validate(doc, opts)
		if len(fb) == 0 {
			fmt.Fprintf(log, "attempt %d/%d: all examples behaved\n", attempt, opts.MaxAttempts)
			return &Result{YAML: []byte(doc + "\n"), RuleFile: rf, Attempts: attempt}, nil
		}
		fmt.Fprintf(log, "attempt %d/%d: %d validation failure(s)\n", attempt, opts.MaxAttempts, len(fb))
		prevYAML, feedback = doc, fb
	}
	return nil, fmt.Errorf("no passing rule after %d attempts; last failures:\n  %s",
		opts.MaxAttempts, strings.Join(feedback, "\n  "))
}
