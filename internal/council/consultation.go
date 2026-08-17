// Package council turns a question and a roster into a Consultation: the panel's
// answers, the judge's comparison of them, and what it all cost.
package council

import (
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// SchemaVersion identifies the --json output contract. Bump it on any breaking change
// to the shape below, since agents parse it.
const SchemaVersion = 1

// Consultation is the result of asking the council a question. It is the single type
// every renderer consumes, and its JSON tags are the machine-readable contract.
type Consultation struct {
	SchemaVersion int       `json:"schema_version"`
	Question      string    `json:"question"`
	AskedAt       time.Time `json:"asked_at"`
	ElapsedMS     int64     `json:"elapsed_ms"`

	// Panel is the roster that was asked. Answered and Failed below partition it.
	Panel []registry.Member `json:"panel"`
	Judge string            `json:"judge"`
	// Outer is the model that wrote the final synthesis.
	Outer string `json:"outer"`
	// OuterResolved is the concrete model the outer slug resolved to, which is the
	// only place the API discloses what a "~…-latest" alias actually pointed at.
	OuterResolved string `json:"outer_resolved,omitempty"`

	// Synthesis is the final merged answer.
	Synthesis string `json:"synthesis"`
	// Analysis is the judge's structured comparison. Nil if the judge produced none.
	Analysis *openrouter.Analysis `json:"analysis,omitempty"`
	// Responses holds each panellist's full answer, in roster order.
	Responses []openrouter.PanelAnswer `json:"responses"`
	Sources   []openrouter.Source      `json:"sources,omitempty"`

	// Failed lists panellists that were asked but produced nothing. A consultation
	// with failures is degraded, not invalid — the analysis reflects who answered.
	Failed []openrouter.FailedModel `json:"failed,omitempty"`
	// FailureReason is set only when the whole fusion run failed.
	FailureReason string `json:"failure_reason,omitempty"`

	Usage      Usage  `json:"usage"`
	ResponseID string `json:"response_id,omitempty"`
	// Warnings carries non-fatal notes, such as a stale model catalog.
	Warnings []string `json:"warnings,omitempty"`
}

// Usage reports what the consultation consumed. Cost covers the entire run — every
// panellist, the judge, and the outer model — not just the final completion.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Degraded reports whether any panellist failed to answer.
func (c *Consultation) Degraded() bool { return len(c.Failed) > 0 }

// Answered returns the number of panellists that produced an answer.
func (c *Consultation) Answered() int { return len(c.Responses) }

// Elapsed returns the wall-clock duration of the consultation.
func (c *Consultation) Elapsed() time.Duration {
	return time.Duration(c.ElapsedMS) * time.Millisecond
}

// Disagreed reports whether the judge found any contradiction between panellists.
// This is the signal worth acting on: a council that agrees adds confidence, and a
// council that splits marks a question that is genuinely open.
func (c *Consultation) Disagreed() bool {
	return c.Analysis != nil && len(c.Analysis.Contradictions) > 0
}
