package council

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// DefaultMaxToolCalls bounds each panellist's and the judge's web-research loop.
// OpenRouter allows up to 16; every extra step multiplies across the whole panel, so
// the default stays modest.
const DefaultMaxToolCalls = 4

// DefaultTimeout is generous because a fusion run is genuinely slow: a measured
// three-model panel with max_tool_calls=1 took roughly 165 seconds end to end.
const DefaultTimeout = 10 * time.Minute

// ErrPanelNotConvened means the API returned a normal completion with no fusion item.
// It indicates the tool was not forced, and the answer came from a single model.
var ErrPanelNotConvened = errors.New("the panel never convened: the response contains no fusion item")

// ErrFusionFailed means every panellist failed, so there is nothing to synthesise.
type ErrFusionFailed struct {
	Reason  string
	Message string
}

func (e *ErrFusionFailed) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("fusion failed (%s)", e.Reason)
	}
	return fmt.Sprintf("fusion failed (%s): %s", e.Reason, e.Message)
}

// Request is one question put to the council.
type Request struct {
	Question string
	Roster   *registry.Roster
	// Panel overrides the roster's slugs when non-empty.
	Panel []string
	// Judge compares the panel's answers; Outer writes the final synthesis.
	// Both default to the first panel member when empty.
	Judge string
	Outer string
	// System is an optional system prompt for the outer model.
	System              string
	MaxToolCalls        int
	MaxCompletionTokens int
	Temperature         *float64
	// Warnings from earlier stages (e.g. discovery) to carry into the result.
	Warnings []string
}

// Asker performs the underlying API call. The concrete implementation is
// *openrouter.Client; the interface keeps this package testable without a network.
type Asker interface {
	Fuse(ctx context.Context, req *openrouter.FusionRequest) (*openrouter.Response, []byte, error)
}

// Ask convenes the council and returns the consultation together with the raw API
// body, which the CLI exposes through --raw.
func Ask(ctx context.Context, client Asker, req Request) (*Consultation, []byte, error) {
	panel := req.Panel
	if len(panel) == 0 {
		if req.Roster == nil {
			return nil, nil, fmt.Errorf("no panel: neither an explicit panel nor a roster was provided")
		}
		panel = req.Roster.Slugs()
	}
	if len(panel) == 0 {
		return nil, nil, fmt.Errorf("no panel: the roster is empty")
	}

	judge := req.Judge
	if judge == "" {
		judge = panel[0]
	}
	outer := req.Outer
	if outer == "" {
		outer = judge
	}
	maxToolCalls := req.MaxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = DefaultMaxToolCalls
	}

	apiReq := &openrouter.FusionRequest{
		Model:        outer,
		Input:        []openrouter.InputItem{{Role: "user", Content: req.Question}},
		Instructions: req.System,
		Tools: []openrouter.Tool{{
			Type: openrouter.FusionToolType,
			Parameters: openrouter.ToolParams{
				AnalysisModels:      panel,
				Model:               judge,
				MaxToolCalls:        maxToolCalls,
				MaxCompletionTokens: req.MaxCompletionTokens,
				Temperature:         req.Temperature,
			},
		}},
		// Without this the outer model answers from its own knowledge and the panel
		// is never convened — a fast, cheap, and entirely uncorroborated answer.
		ToolChoice: openrouter.ForceFusion(),
	}

	started := time.Now()
	resp, raw, err := client.Fuse(ctx, apiReq)
	elapsed := time.Since(started)
	if err != nil {
		return nil, raw, err
	}

	item := resp.Fusion()
	if item == nil {
		return nil, raw, ErrPanelNotConvened
	}
	if item.Status == "error" || (len(item.Responses) == 0 && item.FailureReason != "") {
		return nil, raw, &ErrFusionFailed{Reason: item.FailureReason, Message: item.Error}
	}

	c := &Consultation{
		SchemaVersion: SchemaVersion,
		Question:      req.Question,
		AskedAt:       started,
		ElapsedMS:     elapsed.Milliseconds(),
		Panel:         membersFor(panel, req.Roster),
		Judge:         judge,
		Outer:         outer,
		OuterResolved: resp.Model,
		Synthesis:     resp.Text(),
		Analysis:      item.Analysis,
		Responses:     item.Responses,
		Sources:       item.Sources,
		Failed:        item.FailedModels,
		FailureReason: item.FailureReason,
		ResponseID:    resp.ID,
		Warnings:      req.Warnings,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.TotalTokens,
			CostUSD:      resp.Usage.Cost,
		},
	}
	if c.Analysis.IsEmpty() {
		c.Analysis = nil
	}
	return c, raw, nil
}

// membersFor describes each panel slug, using roster metadata where available so that
// an explicitly overridden panel still renders with context length and pricing.
func membersFor(panel []string, roster *registry.Roster) []registry.Member {
	out := make([]registry.Member, 0, len(panel))
	for _, slug := range panel {
		if roster != nil {
			if m, ok := roster.Find(slug); ok {
				out = append(out, m)
				continue
			}
		}
		out = append(out, registry.Member{Slug: slug, Name: slug})
	}
	return out
}
