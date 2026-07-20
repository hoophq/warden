// Package scan wires Alcatraz into Warden: PII detection over query results,
// with optional user-defined recognizers loaded from a YAML pack. Everything
// runs in-process, with the same zero-network posture as the rest of the tool.
package scan

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hoophq/alcatraz/analyzer"
	"github.com/hoophq/alcatraz/recognizers"
	"gopkg.in/yaml.v3"
)

// CustomPattern is one regex with a confidence score.
type CustomPattern struct {
	Name  string  `yaml:"name,omitempty"`
	Regex string  `yaml:"regex"`
	Score float64 `yaml:"score,omitempty"`
}

// CustomRecognizer is a user-defined pattern recognizer, the "way to add"
// detections Alcatraz doesn't ship.
type CustomRecognizer struct {
	Name     string          `yaml:"name"`
	Entity   string          `yaml:"entity"`
	Patterns []CustomPattern `yaml:"patterns"`
}

// CustomFile is the YAML pack format.
type CustomFile struct {
	CustomRecognizers []CustomRecognizer `yaml:"custom_recognizers"`
}

// NewEngine builds an Alcatraz engine with the full default recognizer set,
// plus the recognizers from customPath when given.
func NewEngine(customPath string) (*analyzer.Engine, error) {
	reg := analyzer.NewRegistry("en")
	recognizers.LoadDefaults(reg, "en")

	if customPath != "" {
		data, err := os.ReadFile(customPath)
		if err != nil {
			return nil, err
		}
		var cf CustomFile
		if err := yaml.Unmarshal(data, &cf); err != nil {
			return nil, fmt.Errorf("%s: invalid YAML: %w", customPath, err)
		}
		for _, cr := range cf.CustomRecognizers {
			if cr.Name == "" || cr.Entity == "" || len(cr.Patterns) == 0 {
				return nil, fmt.Errorf("%s: recognizer needs name, entity and at least one pattern", customPath)
			}
			var pats []*analyzer.Pattern
			for i, cp := range cr.Patterns {
				score := cp.Score
				if score == 0 {
					score = 0.6
				}
				name := cp.Name
				if name == "" {
					name = fmt.Sprintf("%s-%d", cr.Name, i)
				}
				p, err := analyzer.NewPattern(name, cp.Regex, score)
				if err != nil {
					return nil, fmt.Errorf("%s: recognizer %q pattern %d: %w", customPath, cr.Name, i, err)
				}
				pats = append(pats, p)
			}
			reg.Add("en", analyzer.NewPatternRecognizer(cr.Name, cr.Entity, "en", pats))
		}
	}

	return analyzer.NewEngine(reg, []string{"en"}), nil
}

// Finding is an aggregated detection for reporting.
type Finding struct {
	Entity   string
	Count    int
	Samples  []string // masked
	MaxScore float64
}

// Mask hides the middle of a detected value.
func Mask(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	keep := 2
	if len(s) >= 12 {
		keep = 4
	}
	return s[:keep] + strings.Repeat("*", len(s)-2*keep) + s[len(s)-keep:]
}

// Aggregate groups raw results into per-entity findings, masked for display.
func Aggregate(results []analyzer.Result) []Finding {
	byEntity := map[string]*Finding{}
	for _, r := range results {
		f, ok := byEntity[r.EntityType]
		if !ok {
			f = &Finding{Entity: r.EntityType}
			byEntity[r.EntityType] = f
		}
		f.Count++
		if r.Score > f.MaxScore {
			f.MaxScore = r.Score
		}
		if len(f.Samples) < 3 {
			masked := Mask(r.Text)
			seen := false
			for _, s := range f.Samples {
				if s == masked {
					seen = true
					break
				}
			}
			if !seen {
				f.Samples = append(f.Samples, masked)
			}
		}
	}
	out := make([]Finding, 0, len(byEntity))
	for _, f := range byEntity {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entity < out[j].Entity })
	return out
}
