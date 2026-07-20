// Package rules defines the Warden rule file: the canonical, git-committable
// YAML document the web UI exports and the CLI consumes. It carries the rule
// in both regex dialects (Go RE2 for hoop's gateway, Python re for Presidio)
// plus the allow/block examples that prove it.
package rules

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Rule mirrors hoop's gateway guardrail rule shape.
type Rule struct {
	Type         string   `yaml:"type" json:"type"`
	Words        []string `yaml:"words,omitempty" json:"words"`
	PatternRegex string   `yaml:"pattern_regex,omitempty" json:"pattern_regex"`
	// PatternRegexPython is the Python re dialect used by Presidio exports.
	// Informational for the Go matcher, which runs the RE2 pattern.
	PatternRegexPython string `yaml:"pattern_regex_python,omitempty" json:"-"`
	Message            string `yaml:"message,omitempty" json:"message"`
}

const (
	TypeDenyWordsList = "deny_words_list"
	TypePatternMatch  = "pattern_match"
)

// Examples are the queries a rule must block and must allow.
type Examples struct {
	MustBlock []string `yaml:"must_block"`
	MustAllow []string `yaml:"must_allow"`
}

// RuleFile is one Warden rule document.
type RuleFile struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Message     string   `yaml:"message,omitempty"`
	Rules       []Rule   `yaml:"rules"`
	Examples    Examples `yaml:"examples"`
}

// Load reads and validates a rule file.
func Load(path string) (*RuleFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, path)
}

// Parse validates a rule document held in memory. The source string names the
// document in error messages.
func Parse(data []byte, source string) (*RuleFile, error) {
	var rf RuleFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML: %w", source, err)
	}
	if len(rf.Rules) == 0 {
		return nil, fmt.Errorf("%s: no rules defined", source)
	}
	for i, r := range rf.Rules {
		switch r.Type {
		case TypeDenyWordsList, TypePatternMatch:
		default:
			return nil, fmt.Errorf("%s: rules[%d]: unknown rule type %q", source, i, r.Type)
		}
	}
	return &rf, nil
}
