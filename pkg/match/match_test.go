package match

import (
	"testing"

	"github.com/hoophq/warden/pkg/rules"
)

func pat(p string) rules.Rule {
	return rules.Rule{Type: rules.TypePatternMatch, PatternRegex: p}
}

func TestPatternMatchIsUnanchoredSubstring(t *testing.T) {
	r := pat(`(?i)\btruncate\b`)
	if hit, _ := Matches(r, "please TRUNCATE users now"); !hit {
		t.Fatal("expected match anywhere in the query")
	}
	if hit, _ := Matches(r, "SELECT * FROM truncated_view"); hit {
		t.Fatal("word boundary must hold")
	}
}

func TestEmptyPatternNeverMatches(t *testing.T) {
	if hit, _ := Matches(pat(""), "DROP TABLE users"); hit {
		t.Fatal("gateway skips empty regex rules")
	}
}

func TestLookaheadRejectedLikeGateway(t *testing.T) {
	_, err := Matches(pat(`(?i)^\s*UPDATE\s+\w+\s+SET\s+(?!.*WHERE)`), "x")
	if err == nil {
		t.Fatal("RE2 must reject lookahead, as the gateway does")
	}
}

func TestDenyWordsCaseSensitiveContains(t *testing.T) {
	r := rules.Rule{Type: rules.TypeDenyWordsList, Words: []string{"DROP TABLE", ""}}
	if hit, _ := Matches(r, "DROP TABLE users"); !hit {
		t.Fatal("expected substring hit")
	}
	if hit, _ := Matches(r, "drop table users"); hit {
		t.Fatal("deny words are case-sensitive")
	}
}

func TestUnknownTypeErrors(t *testing.T) {
	if _, err := Matches(rules.Rule{Type: "nope"}, "x"); err == nil {
		t.Fatal("expected error for unknown rule type")
	}
}

func TestBlockedAcrossRules(t *testing.T) {
	rf := &rules.RuleFile{Rules: []rules.Rule{
		pat(`(?i)\bdrop\b`),
		{Type: rules.TypeDenyWordsList, Words: []string{"TRUNCATE "}},
	}}
	if hit, _ := Blocked(rf, "TRUNCATE users"); !hit {
		t.Fatal("second rule should block")
	}
	if hit, _ := Blocked(rf, "SELECT 1"); hit {
		t.Fatal("nothing should block a plain select")
	}
}
