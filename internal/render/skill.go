package render

import (
	_ "embed"
	"fmt"
	"io"
	"text/template"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

//go:embed templates/skill.md.tmpl
var skillTemplate string

// skillData is what the SKILL.md template renders against.
type skillData struct {
	Members []registry.Member
	// Latency and Cost are measured figures for a typical consultation, so the
	// generated skill warns an agent with real numbers rather than a vague caution.
	Latency string
	Cost    string
}

// Measured on a live four-lab panel; see the README for the methodology. These are
// order-of-magnitude guidance for the agent, not a quote.
const (
	typicalLatency = "3 minutes"
	typicalCost    = "0.35"
)

// Skill writes a SKILL.md that teaches an agent when and how to consult the council.
// The current roster is baked in, so the skill names the models actually in use.
func Skill(w io.Writer, r *registry.Roster) error {
	tmpl, err := template.New("skill").Parse(skillTemplate)
	if err != nil {
		return fmt.Errorf("parsing skill template: %w", err)
	}
	return tmpl.Execute(w, skillData{
		Members: r.Members,
		Latency: typicalLatency,
		Cost:    typicalCost,
	})
}
