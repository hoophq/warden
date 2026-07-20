package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodYAML = `name: "no-truncate"
description: "blocks TRUNCATE"
message: "TRUNCATE is not allowed"
rules:
  - type: "pattern_match"
    pattern_regex: "(?i)\\btruncate\\b"
examples:
  must_block:
    - "TRUNCATE TABLE users;"
    - "truncate audit_log"
  must_allow:
    - "SELECT * FROM truncated_reports"
    - "DELETE FROM users WHERE id = 1"
`

func TestExtractYAML(t *testing.T) {
	fenced := "Here you go:\n```yaml\nname: x\n```\ntrailing prose"
	if got := extractYAML(fenced); got != "name: x" {
		t.Fatalf("fenced: got %q", got)
	}
	if got := extractYAML("  name: y\n"); got != "name: y" {
		t.Fatalf("bare: got %q", got)
	}
}

func TestValidateFeedback(t *testing.T) {
	// Pattern misses one of its own must_block examples.
	doc := strings.Replace(goodYAML, `(?i)\\btruncate\\b`, `\\btruncate\\b`, 1)
	_, fb := validate(doc, Options{})
	if len(fb) == 0 {
		t.Fatal("case-sensitive pattern must fail the uppercase example")
	}
	found := false
	for _, f := range fb {
		if strings.Contains(f, "SLIPS THROUGH") && strings.Contains(f, "TRUNCATE TABLE users;") {
			found = true
		}
	}
	if !found {
		t.Fatalf("feedback missing the slipping example: %v", fb)
	}
}

func TestValidateRequiresUserQueries(t *testing.T) {
	_, fb := validate(goodYAML, Options{BlockQueries: []string{"TRUNCATE other;"}})
	if len(fb) != 1 || !strings.Contains(fb[0], "must_block is missing") {
		t.Fatalf("expected one missing-query failure, got %v", fb)
	}
}

func TestRunFirstAttempt(t *testing.T) {
	dir := t.TempDir()
	reply := filepath.Join(dir, "reply.txt")
	if err := os.WriteFile(reply, []byte("```yaml\n"+goodYAML+"```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{
		Intent: "block TRUNCATE",
		LLMCmd: "cat " + reply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts != 1 || res.RuleFile.Name != "no-truncate" {
		t.Fatalf("attempts=%d name=%q", res.Attempts, res.RuleFile.Name)
	}
}

func TestRunRetriesWithFeedback(t *testing.T) {
	// A stub LLM that fails on the first call and succeeds on the second,
	// but only if the retry prompt carried the failure feedback.
	dir := t.TempDir()
	stamp := filepath.Join(dir, "called")
	good := filepath.Join(dir, "good.txt")
	bad := strings.Replace(goodYAML, `(?i)\\btruncate\\b`, `(?i)\\bnever\\b`, 1)
	badFile := filepath.Join(dir, "bad.txt")
	for f, content := range map[string]string{good: goodYAML, badFile: bad} {
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := fmt.Sprintf(
		`if [ -f %[1]s ]; then grep -q "SLIPS THROUGH" && cat %[2]s; else touch %[1]s; cat %[3]s; fi`,
		stamp, good, badFile)

	res, err := Run(Options{Intent: "block TRUNCATE", LLMCmd: cmd, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected success on attempt 2, got %d", res.Attempts)
	}
}

func TestRunGivesUp(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(goodYAML, `(?i)\\btruncate\\b`, `(?i)\\bnever\\b`, 1)
	badFile := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(badFile, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{Intent: "block TRUNCATE", LLMCmd: "cat " + badFile, MaxAttempts: 2})
	if err == nil || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("expected give-up error, got %v", err)
	}
}
