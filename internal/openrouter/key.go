package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
)

// KeyInfo describes an API key, as returned by GET /api/v1/key.
//
// The endpoint is the cheapest possible authenticated call — it bills nothing and
// touches no model — which makes it the right way to check a key is real before
// writing it to disk. An invalid key returns HTTP 401 with the usual error envelope,
// so the client's normal error handling reports it without any special casing.
type KeyInfo struct {
	// Label is OpenRouter's own masked form of the key ("sk-or-v1-abcd…xyz") unless
	// the key was given a name in the dashboard. It is safe to display: OpenRouter
	// masks the middle itself.
	Label string `json:"label"`

	// Usage is the dollar amount spent on this key so far.
	Usage float64 `json:"usage"`

	// Limit is the key's spend cap in dollars, or nil when uncapped.
	Limit *float64 `json:"limit"`

	// LimitRemaining is the dollars left under Limit, nil when uncapped.
	LimitRemaining *float64 `json:"limit_remaining"`

	IsFreeTier bool `json:"is_free_tier"`

	// IsProvisioning marks a provisioning key, which manages other keys and cannot
	// be used for inference — a plausible thing to paste in by mistake.
	IsProvisioning bool `json:"is_provisioning_key"`
}

type keyEnvelope struct {
	Data *KeyInfo `json:"data"`
}

// Key fetches metadata for the key the client is configured with. It is used to
// validate a key without spending anything.
func (c *Client) Key(ctx context.Context) (*KeyInfo, error) {
	raw, err := c.get(ctx, "/key")
	if err != nil {
		return nil, err
	}
	var env keyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding key info: %w", err)
	}
	if env.Data == nil {
		return nil, fmt.Errorf("key info response contained no data")
	}
	return env.Data, nil
}

// Spend renders the key's usage and remaining balance for display.
func (k *KeyInfo) Spend() string {
	if k.Limit == nil {
		return fmt.Sprintf("$%.2f used, no spend limit set", k.Usage)
	}
	remaining := 0.0
	if k.LimitRemaining != nil {
		remaining = *k.LimitRemaining
	}
	return fmt.Sprintf("$%.2f used of $%.2f, $%.2f remaining", k.Usage, *k.Limit, remaining)
}
