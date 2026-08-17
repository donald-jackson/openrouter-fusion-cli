// Package registry discovers the current frontier model from each target lab and
// caches the discovery for 24 hours.
//
// Discovery deliberately does not rank OpenRouter's ~400 concrete models itself.
// Ranking them by recency or by price both pick the wrong flagship — recency favours
// whichever small fast model shipped most recently, price favours whatever expensive
// legacy model never had its price cut. OpenRouter already publishes the answer as
// "~author/family-latest" alias entries, each of which resolves to the newest model in
// its family. Those aliases are the source of truth here; this package only has to
// choose which alias represents each lab's flagship.
package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
)

// Member is one seat on the council.
type Member struct {
	// Author is the publishing lab, e.g. "anthropic".
	Author string `json:"author"`
	// Lab is the human-facing name of the author, e.g. "Anthropic".
	Lab string `json:"lab"`
	// Slug is the model to call — always an alias, so it stays current.
	Slug string `json:"slug"`
	// Name is OpenRouter's display name for the alias.
	Name string `json:"name"`
	// ContextLength and the prices describe the model the alias resolves to today.
	ContextLength int `json:"context_length"`
	// PromptUSDPerMTok and CompletionUSDPerMTok are per-million-token prices.
	PromptUSDPerMTok     float64 `json:"prompt_usd_per_mtok"`
	CompletionUSDPerMTok float64 `json:"completion_usd_per_mtok"`
	// Preferred is false when the lab's usual flagship alias was missing and this
	// seat was filled by the price fallback instead.
	Preferred bool `json:"preferred"`
}

// lab describes one seat to fill: which author, and which model families to prefer.
//
// The preference lists are seeded from OpenRouter's own Fusion "Quality" preset
// (Claude Opus, GPT, Gemini Pro), with xAI added to complete the four labs. Preferring
// a named family rather than simply taking the priciest alias matters: Anthropic
// publishes ~anthropic/claude-fable-latest at $10/$50 per Mtok against Claude Opus at
// $5/$25, so a pure price rule would quietly swap the panel's Anthropic seat.
type lab struct {
	author     string
	display    string
	families   []string
	exclusions []string
}

var labs = []lab{
	{author: "openai", display: "OpenAI", families: []string{"gpt"}, exclusions: []string{"gpt-mini"}},
	{author: "anthropic", display: "Anthropic", families: []string{"claude-opus", "claude-fable", "claude-sonnet"}},
	{author: "google", display: "Google", families: []string{"gemini-pro", "gemini-flash"}},
	{author: "x-ai", display: "xAI", families: []string{"grok"}},
}

// Roster is the discovered council, plus everything else discovery learned.
type Roster struct {
	Members []Member `json:"members"`
	// Aliases is every "~…-latest" alias in the catalog, for `council models --all`.
	Aliases []Member `json:"aliases,omitempty"`
	// Warnings records labs that could not be seated.
	Warnings []string `json:"warnings,omitempty"`
}

// Slugs returns the member slugs, in roster order — the fusion panel.
func (r *Roster) Slugs() []string {
	out := make([]string, 0, len(r.Members))
	for _, m := range r.Members {
		out = append(out, m.Slug)
	}
	return out
}

// Find returns the member with the given slug, if the roster contains one.
func (r *Roster) Find(slug string) (Member, bool) {
	for _, m := range r.Members {
		if m.Slug == slug {
			return m, true
		}
	}
	for _, m := range r.Aliases {
		if m.Slug == slug {
			return m, true
		}
	}
	return Member{}, false
}

// Build selects the council from a model catalog.
//
// For each lab it takes the first alias matching that lab's family preferences and, if
// none matches, falls back to the lab's most expensive alias — expensive is a decent
// proxy for flagship, and the fallback keeps the roster working if OpenRouter renames a
// family. A lab with no aliases at all is skipped with a warning rather than treated as
// fatal: a three-model council is still a council.
func Build(catalog *openrouter.ModelsResponse) (*Roster, error) {
	if catalog == nil || len(catalog.Data) == 0 {
		return nil, fmt.Errorf("empty model catalog")
	}

	byAuthor := make(map[string][]openrouter.Model)
	var all []Member
	for _, m := range catalog.Data {
		if !m.IsAlias() {
			continue
		}
		byAuthor[m.Author()] = append(byAuthor[m.Author()], m)
		all = append(all, toMember(m, displayName(m.Author()), true))
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("model catalog contains no ~latest aliases")
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Slug < all[j].Slug })

	roster := &Roster{Aliases: all}
	for _, l := range labs {
		candidates := byAuthor[l.author]
		if len(candidates) == 0 {
			roster.Warnings = append(roster.Warnings,
				fmt.Sprintf("%s has no ~latest alias in the catalog; seat left empty", l.display))
			continue
		}
		if m, ok := pickPreferred(candidates, l); ok {
			roster.Members = append(roster.Members, toMember(m, l.display, true))
			continue
		}

		// Fall back to the lab's priciest alias, but never past an exclusion: seating
		// a mini tier because the flagship alias vanished would quietly turn a
		// frontier council into a cheap one. An empty seat is the honest outcome.
		eligible := eligibleFor(candidates, l)
		if len(eligible) == 0 {
			roster.Warnings = append(roster.Warnings, fmt.Sprintf(
				"%s: no frontier alias available (only %s); seat left empty",
				l.display, strings.Join(slugsOf(candidates), ", ")))
			continue
		}
		m := priciest(eligible)
		roster.Members = append(roster.Members, toMember(m, l.display, false))
		roster.Warnings = append(roster.Warnings, fmt.Sprintf(
			"%s: no alias matched %s; using %s instead",
			l.display, strings.Join(l.families, "/"), m.ID))
	}

	if len(roster.Members) == 0 {
		return nil, fmt.Errorf("no council members could be selected from %d aliases", len(all))
	}
	return roster, nil
}

// pickPreferred returns the highest-ranked alias matching the lab's family preferences.
func pickPreferred(candidates []openrouter.Model, l lab) (openrouter.Model, bool) {
	for _, family := range l.families {
		var matches []openrouter.Model
		for _, m := range candidates {
			if m.Family() != family || excluded(m, l) {
				continue
			}
			matches = append(matches, m)
		}
		if len(matches) > 0 {
			return priciest(matches), true
		}
	}
	return openrouter.Model{}, false
}

// eligibleFor returns the candidates a lab is willing to seat at all.
func eligibleFor(candidates []openrouter.Model, l lab) []openrouter.Model {
	var out []openrouter.Model
	for _, m := range candidates {
		if !excluded(m, l) {
			out = append(out, m)
		}
	}
	return out
}

func slugsOf(models []openrouter.Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func excluded(m openrouter.Model, l lab) bool {
	for _, ex := range l.exclusions {
		if m.Family() == ex {
			return true
		}
	}
	return false
}

// priciest returns the model with the highest completion price, breaking ties on the
// slug so selection is deterministic across runs.
func priciest(models []openrouter.Model) openrouter.Model {
	best := models[0]
	for _, m := range models[1:] {
		switch {
		case m.CompletionPrice() > best.CompletionPrice():
			best = m
		case m.CompletionPrice() == best.CompletionPrice() && m.ID < best.ID:
			best = m
		}
	}
	return best
}

func toMember(m openrouter.Model, lab string, preferred bool) Member {
	const perMillion = 1_000_000
	return Member{
		Author:               m.Author(),
		Lab:                  lab,
		Slug:                 m.ID,
		Name:                 m.Name,
		ContextLength:        m.ContextLength,
		PromptUSDPerMTok:     m.PromptPrice() * perMillion,
		CompletionUSDPerMTok: m.CompletionPrice() * perMillion,
		Preferred:            preferred,
	}
}

// displayName maps an author slug to a human-facing lab name, falling back to the slug
// itself for labs the CLI does not seat by default.
func displayName(author string) string {
	for _, l := range labs {
		if l.author == author {
			return l.display
		}
	}
	return author
}
