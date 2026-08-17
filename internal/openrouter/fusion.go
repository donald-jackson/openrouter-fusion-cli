package openrouter

// Fusion request/response modelling for POST /api/v1/responses with the
// openrouter:fusion server tool.
//
// The shapes below were confirmed against the live API rather than taken from the
// docs alone. What the capture in testdata/fusion_response.json established:
//
//  1. The panel item's discriminator in output[] is exactly "openrouter:fusion"
//     (not "fusion_call", which the web_search naming convention would suggest).
//  2. analysis, responses, sources and failed_models sit directly on that item.
//  3. The final prose is a separate output[] item of type "message", whose content[]
//     carries parts of type "output_text". A "reasoning" item may also be present.
//  4. usage.cost is populated, and the top-level id ("gen-…") is the generation ID.
//     The top-level model field reports the concrete model an alias resolved to
//     (e.g. "anthropic/claude-opus-5" for "~anthropic/claude-opus-latest").
//  5. tool_choice MUST name or require the tool. Under the default "auto" the model
//     answers from its own knowledge and never convenes the panel — the request then
//     returns in seconds for cents, which looks like success but is not a consultation.

import (
	"context"
	"encoding/json"
	"fmt"
)

// FusionToolType is the server-tool discriminator, used both to request the tool and
// to recognise its output item.
const FusionToolType = "openrouter:fusion"

// FusionRequest is the POST /api/v1/responses payload.
type FusionRequest struct {
	Model      string      `json:"model"`
	Input      []InputItem `json:"input"`
	Tools      []Tool      `json:"tools"`
	ToolChoice any         `json:"tool_choice"`
	// Instructions is the system prompt, applied to the outer (synthesising) model.
	Instructions string `json:"instructions,omitempty"`
}

// InputItem is a single message in the request input.
type InputItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool declares a server tool.
type Tool struct {
	Type       string     `json:"type"`
	Parameters ToolParams `json:"parameters,omitempty"`
}

// ToolParams configures the fusion panel.
type ToolParams struct {
	// AnalysisModels is the panel: 1–8 model slugs answering the prompt in parallel.
	AnalysisModels []string `json:"analysis_models,omitempty"`
	// Model is the judge, which compares the panel's answers and produces the analysis.
	Model string `json:"model,omitempty"`
	// MaxToolCalls bounds each panelist's and the judge's web-research loop (1–16).
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// MaxCompletionTokens caps the output of each panel and judge call.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	// Temperature is forwarded to the panel; the judge always runs at 0.
	Temperature *float64 `json:"temperature,omitempty"`
}

// NamedToolChoice forces the model to invoke a specific server tool.
type NamedToolChoice struct {
	Type string `json:"type"`
}

// ForceFusion returns the tool_choice value that compels the fusion panel to convene.
func ForceFusion() NamedToolChoice { return NamedToolChoice{Type: FusionToolType} }

// Response is the parsed POST /api/v1/responses body.
type Response struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  Usage             `json:"usage"`
	Error  *APIError         `json:"error"`
}

// Usage reports token counts and the billed cost of the whole request, panel included.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// FusionItem is the openrouter:fusion entry in output[].
type FusionItem struct {
	Type          string        `json:"type"`
	ID            string        `json:"id"`
	Status        string        `json:"status"`
	Responses     []PanelAnswer `json:"responses"`
	Analysis      *Analysis     `json:"analysis"`
	Sources       []Source      `json:"sources"`
	FailedModels  []FailedModel `json:"failed_models"`
	Error         string        `json:"error"`
	FailureReason string        `json:"failure_reason"`
}

// PanelAnswer is one panellist's full response.
type PanelAnswer struct {
	Model   string `json:"model"`
	Content string `json:"content"`
}

// FailedModel records a panellist that was asked but produced nothing. A run with
// failures is degraded but still usable — the analysis reflects the surviving panel.
type FailedModel struct {
	Model      string `json:"model"`
	Error      string `json:"error"`
	StatusCode int    `json:"status_code,omitempty"`
}

// Source is a web page the panel or judge retrieved, deduplicated across the run.
type Source struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Analysis is the judge's structured comparison of the panel's answers.
type Analysis struct {
	Consensus       []string          `json:"consensus"`
	Contradictions  []Contradiction   `json:"contradictions"`
	PartialCoverage []PartialCoverage `json:"partial_coverage"`
	UniqueInsights  []UniqueInsight   `json:"unique_insights"`
	BlindSpots      []string          `json:"blind_spots"`
}

// IsEmpty reports whether the judge found nothing to say in any category.
func (a *Analysis) IsEmpty() bool {
	if a == nil {
		return true
	}
	return len(a.Consensus) == 0 && len(a.Contradictions) == 0 &&
		len(a.PartialCoverage) == 0 && len(a.UniqueInsights) == 0 && len(a.BlindSpots) == 0
}

// Contradiction is a point on which the panel disagreed.
type Contradiction struct {
	Topic   string   `json:"topic,omitempty"`
	Stances []Stance `json:"stances,omitempty"`
	Text    string   `json:"text,omitempty"` // when the judge returned a bare string
}

// Stance is one model's position within a contradiction.
type Stance struct {
	Model    string `json:"model,omitempty"`
	Position string `json:"position,omitempty"`
	Text     string `json:"text,omitempty"`
}

// PartialCoverage is a point only some of the panel raised.
type PartialCoverage struct {
	Models []string `json:"models,omitempty"`
	Point  string   `json:"point,omitempty"`
	Text   string   `json:"text,omitempty"`
}

// UniqueInsight is a point exactly one model raised.
type UniqueInsight struct {
	Model   string `json:"model,omitempty"`
	Insight string `json:"insight,omitempty"`
	Text    string `json:"text,omitempty"`
}

// The judge is itself a language model, so a category the docs describe as a list of
// objects can come back as a list of plain strings. Each type below accepts either,
// preserving the string form in Text rather than failing the whole consultation.

func (c *Contradiction) UnmarshalJSON(b []byte) error {
	type alias Contradiction
	return decodeStringOrObject(b, (*alias)(c), &c.Text)
}

func (p *PartialCoverage) UnmarshalJSON(b []byte) error {
	type alias PartialCoverage
	return decodeStringOrObject(b, (*alias)(p), &p.Text)
}

func (u *UniqueInsight) UnmarshalJSON(b []byte) error {
	type alias UniqueInsight
	return decodeStringOrObject(b, (*alias)(u), &u.Text)
}

func (s *Stance) UnmarshalJSON(b []byte) error {
	type alias Stance
	return decodeStringOrObject(b, (*alias)(s), &s.Text)
}

// decodeStringOrObject unmarshals b into obj, or, when b is a JSON string, stores it
// in text. The alias type breaks the recursion back into the custom unmarshaller.
func decodeStringOrObject(b []byte, obj any, text *string) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*text = s
		return nil
	}
	return json.Unmarshal(b, obj)
}

// Fuse runs a fusion consultation. It returns the parsed response together with the
// raw body, which the CLI exposes through --raw.
func (c *Client) Fuse(ctx context.Context, req *FusionRequest) (*Response, []byte, error) {
	raw, err := c.post(ctx, "/responses", req)
	if err != nil {
		return nil, raw, err
	}
	parsed, err := ParseResponse(raw)
	return parsed, raw, err
}

// ParseResponse decodes a raw POST /api/v1/responses body.
func ParseResponse(raw []byte) (*Response, error) {
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return &out, out.Error
	}
	return &out, nil
}

// Fusion returns the panel item from output[], or nil when the model never convened
// the panel — which happens whenever tool_choice was left as "auto".
func (r *Response) Fusion() *FusionItem {
	for _, raw := range r.Output {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.Type != FusionToolType {
			continue
		}
		var item FusionItem
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		return &item
	}
	return nil
}

// Text returns the outer model's final prose, concatenating every output_text part of
// every message item. Item types the CLI does not model — "reasoning" today, whatever
// OpenRouter adds tomorrow — are skipped rather than treated as an error.
func (r *Response) Text() string {
	var out []byte
	for _, raw := range r.Output {
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" && part.Text != "" {
				if len(out) > 0 {
					out = append(out, '\n', '\n')
				}
				out = append(out, part.Text...)
			}
		}
	}
	return string(out)
}
