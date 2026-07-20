// Package export turns a Warden rule file into the payload Hoop's
// POST /api/guardrails endpoint accepts, and into a ready-to-run curl that
// ships it there. Mirrors the builder UI's Hoop export (lib/warden in the
// hoop.dev landing-page repo): credentials come from a logged-in hoop CLI
// via `hoop config view`, so the printed command works as-is.
package export

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoophq/warden/pkg/rules"
)

// apiPayload is the create-guardrail request body.
type apiPayload struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Input       ruleSet `json:"input"`
	Output      ruleSet `json:"output"`
}

type ruleSet struct {
	Rules []rules.Rule `json:"rules"`
}

// Payload renders the JSON body for POST /api/guardrails. Per-rule messages
// fall back to the file-level message, and words is always an array so the
// document matches what the gateway decodes.
func Payload(rf *rules.RuleFile) ([]byte, error) {
	in := make([]rules.Rule, len(rf.Rules))
	for i, r := range rf.Rules {
		if r.Message == "" {
			r.Message = rf.Message
		}
		if r.Words == nil {
			r.Words = []string{}
		}
		in[i] = r
	}
	return json.MarshalIndent(apiPayload{
		Name:        rf.Name,
		Description: rf.Description,
		Input:       ruleSet{Rules: in},
		Output:      ruleSet{Rules: []rules.Rule{}},
	}, "", "  ")
}

// Curl renders a ready-to-run curl that creates the guardrail through Hoop's
// API, pulling the API URL and token from a logged-in hoop CLI (`hoop login`).
func Curl(rf *rules.RuleFile) (string, error) {
	payload, err := Payload(rf)
	if err != nil {
		return "", err
	}
	// Single quotes inside the payload must not terminate the shell string.
	escaped := strings.ReplaceAll(string(payload), `'`, `'\''`)
	return fmt.Sprintf(`curl -sf -X POST "$(hoop config view api_url)/api/guardrails" \
  -H "Authorization: Bearer $(hoop config view token)" \
  -H "Content-Type: application/json" \
  -d '%s'
`, escaped), nil
}
