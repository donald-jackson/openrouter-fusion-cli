package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/council"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// Human writes a consultation for a person to read.
//
// The ordering is deliberate. A synthesis alone is just another model's answer; what
// makes a council worth its cost is knowing where the panel split, what only one model
// noticed, and what none of them covered. Those sections come before the transcript,
// and disagreement is the loudest thing on the page.
func Human(w io.Writer, c *council.Consultation, s Style, full bool) error {
	p := &printer{w: w, s: s}

	p.header(c)
	p.section("ANSWER", s.bold)
	p.body(c.Synthesis, "  ")

	if a := c.Analysis; a != nil {
		if len(a.Contradictions) > 0 {
			p.section(fmt.Sprintf("DISAGREED  (%d)", len(a.Contradictions)), s.red)
			p.note("the panel split here — treat these as open questions, not settled answers")
			for _, cd := range a.Contradictions {
				p.contradiction(cd)
			}
		}
		if len(a.Consensus) > 0 {
			p.section(fmt.Sprintf("AGREED  (%d)", len(a.Consensus)), s.green)
			for _, point := range a.Consensus {
				p.bullet(point)
			}
		}
		if len(a.UniqueInsights) > 0 {
			p.section(fmt.Sprintf("RAISED BY ONE MODEL  (%d)", len(a.UniqueInsights)), s.cyan)
			for _, ui := range a.UniqueInsights {
				if ui.Text != "" {
					p.bullet(ui.Text)
					continue
				}
				p.attributed(ui.Model, ui.Insight)
			}
		}
		if len(a.PartialCoverage) > 0 {
			p.section(fmt.Sprintf("RAISED BY SOME  (%d)", len(a.PartialCoverage)), s.cyan)
			for _, pc := range a.PartialCoverage {
				if pc.Text != "" {
					p.bullet(pc.Text)
					continue
				}
				p.attributed(strings.Join(pc.Models, ", "), pc.Point)
			}
		}
		if len(a.BlindSpots) > 0 {
			p.section(fmt.Sprintf("NOT COVERED BY ANYONE  (%d)", len(a.BlindSpots)), s.yellow)
			for _, bs := range a.BlindSpots {
				p.bullet(bs)
			}
		}
	} else if c.Answered() > 0 {
		p.section("ANALYSIS", s.dim)
		p.note("the judge returned no structured comparison for this run")
	}

	if full && len(c.Responses) > 0 {
		p.section("PANEL TRANSCRIPT", s.bold)
		for i, r := range c.Responses {
			if i > 0 {
				p.blank()
			}
			p.line(p.s.bold(p.s.blue(r.Model)))
			p.body(r.Content, "    ")
		}
	} else if len(c.Responses) > 0 {
		p.blank()
		p.line(p.s.dim(fmt.Sprintf("  %d panel answers withheld — rerun with --full to read them", len(c.Responses))))
	}

	if len(c.Sources) > 0 {
		p.section(fmt.Sprintf("SOURCES  (%d)", len(c.Sources)), s.dim)
		for i, src := range c.Sources {
			p.line(fmt.Sprintf("  %2d. %s", i+1, truncate(src.Title, s.Width-8)))
			p.line("      " + p.s.dim(src.URL))
		}
	}

	if c.Degraded() {
		p.section(fmt.Sprintf("DEGRADED PANEL  (%d of %d did not answer)",
			len(c.Failed), len(c.Failed)+c.Answered()), s.yellow)
		p.note("the answer and analysis above reflect only the models that responded")
		for _, f := range c.Failed {
			detail := f.Error
			if f.StatusCode != 0 {
				detail = fmt.Sprintf("HTTP %d: %s", f.StatusCode, detail)
			}
			p.line(fmt.Sprintf("  %s  %s", p.s.yellow(f.Model), p.s.dim(detail)))
		}
	}

	for _, warning := range c.Warnings {
		p.blank()
		p.line(p.s.dim("  note: " + warning))
	}

	p.footer(c)
	return p.err
}

// Roster writes the council membership, used by `council models`.
func Roster(w io.Writer, r *registry.Roster, res *registry.Result, s Style, all bool) error {
	p := &printer{w: w, s: s}

	age := "just now"
	if d := res.Age(); d > time.Minute {
		age = compactDuration(d) + " ago"
	}
	status := fmt.Sprintf("fetched %s · refreshes %s", age, compactDuration(time.Until(res.Expires()))+" from now")
	if res.Stale {
		status = s.yellow(fmt.Sprintf("fetched %s · refresh failed, serving stale", age))
	}
	p.line(s.bold("Council roster") + "  " + s.dim(status))
	p.blank()

	members := r.Members
	if all {
		members = r.Aliases
	}
	for _, m := range members {
		lab := m.Lab
		if lab == "" {
			lab = m.Author
		}
		marker := " "
		if !m.Preferred {
			marker = s.yellow("!")
		}
		p.line(fmt.Sprintf("%s %-10s %-34s %9s  %s",
			marker, lab, s.bold(m.Slug), formatContext(m.ContextLength), s.dim(formatPrice(m))))
	}

	if !all && len(r.Members) > 0 {
		p.blank()
		p.line(s.dim(fmt.Sprintf("  judge & synthesis: %s", r.Members[0].Slug)))
	}
	for _, warning := range r.Warnings {
		p.line(s.yellow("  ! " + warning))
	}
	if !all {
		p.blank()
		p.line(s.dim(fmt.Sprintf("  %d ~latest aliases available in total — see --all", len(r.Aliases))))
	}
	return p.err
}

// printer accumulates the first write error rather than checking every call site.
type printer struct {
	w   io.Writer
	s   Style
	err error
}

func (p *printer) line(text string) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintln(p.w, text)
}

func (p *printer) blank() { p.line("") }

func (p *printer) section(title string, paint func(string) string) {
	p.blank()
	p.line(paint(title))
	p.line(p.s.dim(strings.Repeat("─", min(runeLen(title), p.s.Width))))
}

func (p *printer) note(text string) {
	for _, l := range wrap(text, p.s.Width, "  ") {
		p.line(p.s.dim(l))
	}
}

func (p *printer) body(text, indent string) {
	if strings.TrimSpace(text) == "" {
		p.line(indent + p.s.dim("(empty)"))
		return
	}
	for _, l := range wrap(text, p.s.Width, indent) {
		p.line(l)
	}
}

func (p *printer) bullet(text string) {
	lines := wrap(text, p.s.Width, "    ")
	for i, l := range lines {
		if i == 0 {
			p.line("  • " + strings.TrimLeft(l, " "))
			continue
		}
		p.line(l)
	}
}

func (p *printer) attributed(who, what string) {
	if who == "" {
		p.bullet(what)
		return
	}
	p.line("  " + p.s.blue(who))
	for _, l := range wrap(what, p.s.Width, "      ") {
		p.line(l)
	}
}

func (p *printer) contradiction(c openrouter.Contradiction) {
	if c.Text != "" {
		p.bullet(c.Text)
		return
	}
	if c.Topic != "" {
		p.line("  " + p.s.bold(c.Topic))
	}
	for _, st := range c.Stances {
		switch {
		case st.Text != "":
			for _, l := range wrap(st.Text, p.s.Width, "      ") {
				p.line(l)
			}
		case st.Model != "":
			p.line("    " + p.s.blue(st.Model))
			for _, l := range wrap(st.Position, p.s.Width, "      ") {
				p.line(l)
			}
		default:
			for _, l := range wrap(st.Position, p.s.Width, "      ") {
				p.line(l)
			}
		}
	}
}

func (p *printer) header(c *council.Consultation) {
	s := p.s
	p.line(s.bold("Council") + s.dim(fmt.Sprintf("  %s", c.AskedAt.Format("2006-01-02 15:04"))))
	for _, l := range wrap(c.Question, s.Width, "  ") {
		p.line(s.bold(l))
	}
	p.blank()

	seats := make([]string, 0, len(c.Panel))
	for _, m := range c.Panel {
		seats = append(seats, m.Slug)
	}
	for _, l := range wrap("panel: "+strings.Join(seats, "  "), s.Width, "  ") {
		p.line(s.dim(l))
	}
	judge := fmt.Sprintf("judge: %s", c.Judge)
	if c.OuterResolved != "" && c.OuterResolved != c.Outer {
		// The resolved slug is the only place the API says what the alias pointed at.
		// The separator has to be a printing character: wrapping collapses runs of
		// spaces, which would run the two labels together.
		judge += fmt.Sprintf(" · synthesis: %s → %s", c.Outer, c.OuterResolved)
	}
	for _, l := range wrap(judge, s.Width, "  ") {
		p.line(s.dim(l))
	}
}

func (p *printer) footer(c *council.Consultation) {
	s := p.s
	answered := fmt.Sprintf("%d of %d answered", c.Answered(), c.Answered()+len(c.Failed))
	if c.Degraded() {
		answered = s.yellow(answered)
	}
	p.blank()
	p.line(s.dim(strings.Repeat("─", min(60, s.Width))))
	p.line(s.dim(fmt.Sprintf("%s · %s · $%.4f · %d tokens",
		answered, compactDuration(c.Elapsed()), c.Usage.CostUSD, c.Usage.TotalTokens)))
}

func formatContext(n int) string {
	switch {
	case n == 0:
		return ""
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM ctx", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK ctx", n/1_000)
	default:
		return fmt.Sprintf("%d ctx", n)
	}
}

func formatPrice(m registry.Member) string {
	if m.PromptUSDPerMTok == 0 && m.CompletionUSDPerMTok == 0 {
		return ""
	}
	return fmt.Sprintf("$%g/$%g per Mtok", m.PromptUSDPerMTok, m.CompletionUSDPerMTok)
}

// compactDuration renders a duration the way a person would say it.
func compactDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
