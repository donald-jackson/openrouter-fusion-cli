package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Model is one entry from GET /api/v1/models.
//
// Only the fields the CLI actually uses are modelled; OpenRouter returns considerably
// more. Note that for a "~author/family-latest" alias the pricing and context length
// describe the model the alias currently resolves to, while CanonicalSlug is the alias
// itself — the concrete target is not exposed by the catalog.
type Model struct {
	ID            string   `json:"id"`
	CanonicalSlug string   `json:"canonical_slug"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Created       int64    `json:"created"`
	ContextLength int      `json:"context_length"`
	Pricing       Pricing  `json:"pricing"`
	Architecture  Arch     `json:"architecture"`
	SupportedArgs []string `json:"supported_parameters"`
}

// Pricing holds per-token prices as decimal strings, exactly as the API returns them.
// A price of "-1" means the real cost depends on which model a router selects.
type Pricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

// Arch describes a model's input and output modalities.
type Arch struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

// ModelsResponse is the envelope returned by GET /api/v1/models.
type ModelsResponse struct {
	Data       []Model `json:"data"`
	TotalCount int     `json:"total_count"`
}

// IsAlias reports whether the model is one of OpenRouter's "~author/family-latest"
// aliases, which always resolve to the newest model in that family.
func (m Model) IsAlias() bool { return strings.HasPrefix(m.ID, "~") }

// Author returns the publisher segment of the model ID, with any alias prefix removed
// ("~anthropic/claude-opus-latest" and "anthropic/claude-opus-5" both yield "anthropic").
func (m Model) Author() string {
	id := strings.TrimPrefix(m.ID, "~")
	author, _, _ := strings.Cut(id, "/")
	return author
}

// Family returns the portion of an alias ID between the author and the "-latest"
// suffix: "~anthropic/claude-opus-latest" yields "claude-opus". For a non-alias model
// it returns the full slug after the author.
func (m Model) Family() string {
	id := strings.TrimPrefix(m.ID, "~")
	_, slug, _ := strings.Cut(id, "/")
	return strings.TrimSuffix(slug, "-latest")
}

// CompletionPrice parses the per-token completion price. Router pseudo-models price
// themselves as "-1" (cost depends on the models they select); those yield 0 so they
// never win a price-based comparison.
func (m Model) CompletionPrice() float64 {
	f, err := strconv.ParseFloat(m.Pricing.Completion, 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

// PromptPrice parses the per-token prompt price. See CompletionPrice for the "-1" case.
func (m Model) PromptPrice() float64 {
	f, err := strconv.ParseFloat(m.Pricing.Prompt, 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

// Models fetches the full model catalog. This endpoint is free and needs no
// authentication. It returns the parsed catalog alongside the raw body, so callers
// can cache the bytes verbatim rather than a lossy re-encoding.
func (c *Client) Models(ctx context.Context) (*ModelsResponse, []byte, error) {
	raw, err := c.get(ctx, "/models?limit=1000")
	if err != nil {
		return nil, raw, err
	}
	parsed, err := ParseModels(raw)
	return parsed, raw, err
}

// ParseModels decodes a raw GET /api/v1/models body.
func ParseModels(raw []byte) (*ModelsResponse, error) {
	var out ModelsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding model catalog: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("model catalog is empty")
	}
	return &out, nil
}
