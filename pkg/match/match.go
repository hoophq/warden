// Package match evaluates Warden rules against queries with the semantics of
// hoop's built-in guardrail engine (libhoop/redactor/localguardrails and
// gateway/guardrails): pattern rules are Go RE2 compiled and matched
// unanchored against the raw query; deny words are case-sensitive substring
// checks; empty entries are skipped. Because this runs on Go's regexp itself,
// it agrees with that engine by construction. Deployments with a MSPresidio
// DLP provider evaluate guardrails through the Presidio analyzer (Python re)
// instead; portable patterns behave the same there, while Python-only
// constructs (lookahead) fall outside what this package can check.
package match

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hoophq/warden/pkg/rules"
)

// Matches reports whether a single rule would block the query.
func Matches(r rules.Rule, query string) (bool, error) {
	switch r.Type {
	case rules.TypeDenyWordsList:
		for _, word := range r.Words {
			if word == "" {
				continue
			}
			if strings.Contains(query, word) {
				return true, nil
			}
		}
		return false, nil
	case rules.TypePatternMatch:
		if r.PatternRegex == "" {
			return false, nil
		}
		re, err := regexp.Compile(r.PatternRegex)
		if err != nil {
			return false, fmt.Errorf("failed parsing regex, reason=%v", err)
		}
		return re.MatchString(query), nil
	default:
		return false, fmt.Errorf("unknown rule type %q", r.Type)
	}
}

// Blocked reports whether any rule in the file blocks the query.
func Blocked(rf *rules.RuleFile, query string) (bool, error) {
	for _, r := range rf.Rules {
		hit, err := Matches(r, query)
		if err != nil {
			return false, err
		}
		if hit {
			return true, nil
		}
	}
	return false, nil
}
