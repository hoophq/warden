package export

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/warden/pkg/rules"
)

func sampleFile() *rules.RuleFile {
	return &rules.RuleFile{
		Name:        "no-tautology",
		Description: "Created with Warden",
		Message:     "always-true conditions are blocked",
		Rules: []rules.Rule{
			{
				Type:               rules.TypePatternMatch,
				PatternRegex:       `(?i)\bwhere\s+'1'\s*=\s*'1'`,
				PatternRegexPython: `(?i)python-only`,
			},
		},
	}
}

func TestPayloadShape(t *testing.T) {
	out, err := Payload(sampleFile())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name  string `json:"name"`
		Input struct {
			Rules []map[string]any `json:"rules"`
		} `json:"input"`
		Output struct {
			Rules []map[string]any `json:"rules"`
		} `json:"output"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if doc.Name != "no-tautology" {
		t.Errorf("name = %q", doc.Name)
	}
	if len(doc.Input.Rules) != 1 {
		t.Fatalf("input rules = %d", len(doc.Input.Rules))
	}
	r := doc.Input.Rules[0]
	if r["message"] != "always-true conditions are blocked" {
		t.Errorf("file-level message not inherited: %v", r["message"])
	}
	if _, isArray := r["words"].([]any); !isArray {
		t.Errorf("words must be an array, got %T", r["words"])
	}
	if strings.Contains(string(out), "python-only") {
		t.Error("Python dialect pattern must not ship in the gateway payload")
	}
	if doc.Output.Rules == nil || len(doc.Output.Rules) != 0 {
		t.Errorf("output rules must be an empty array")
	}
}

func TestCurlReadsHoopConfigAndEscapesQuotes(t *testing.T) {
	cmd, err := Curl(sampleFile())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`$(hoop config view api_url)/api/guardrails`,
		`Bearer $(hoop config view token)`,
		`'\''`, // single quotes in the pattern must not break the shell string
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("curl missing %q:\n%s", want, cmd)
		}
	}
}
