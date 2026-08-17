package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/council"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// JSON writes the consultation as indented JSON. This is the agent-facing contract:
// the field names come from the struct tags on council.Consultation and are covered by
// a golden test, so they cannot drift silently.
func JSON(w io.Writer, c *council.Consultation) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(c)
}

// rosterDoc is the JSON shape of `council models --json`.
type rosterDoc struct {
	SchemaVersion int               `json:"schema_version"`
	FetchedAt     time.Time         `json:"fetched_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
	FromCache     bool              `json:"from_cache"`
	Stale         bool              `json:"stale"`
	Members       []registry.Member `json:"members"`
	Aliases       []registry.Member `json:"aliases,omitempty"`
	Warnings      []string          `json:"warnings,omitempty"`
}

// RosterJSON writes the discovered roster as JSON.
func RosterJSON(w io.Writer, res *registry.Result, all bool) error {
	doc := rosterDoc{
		SchemaVersion: council.SchemaVersion,
		FetchedAt:     res.FetchedAt,
		ExpiresAt:     res.Expires(),
		FromCache:     res.FromCache,
		Stale:         res.Stale,
		Members:       res.Roster.Members,
		Warnings:      res.Roster.Warnings,
	}
	if all {
		doc.Aliases = res.Roster.Aliases
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}
