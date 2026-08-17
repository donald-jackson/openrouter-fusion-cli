package render

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/council"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

var update = flag.Bool("update", false, "rewrite golden files")

func members() []registry.Member {
	return []registry.Member{
		{Author: "openai", Lab: "OpenAI", Slug: "~openai/gpt-latest", Name: "OpenAI GPT Latest",
			ContextLength: 1_050_000, PromptUSDPerMTok: 5, CompletionUSDPerMTok: 30, Preferred: true},
		{Author: "anthropic", Lab: "Anthropic", Slug: "~anthropic/claude-opus-latest", Name: "Anthropic: Claude Opus Latest",
			ContextLength: 1_000_000, PromptUSDPerMTok: 5, CompletionUSDPerMTok: 25, Preferred: true},
		{Author: "google", Lab: "Google", Slug: "~google/gemini-pro-latest", Name: "Google Gemini Pro Latest",
			ContextLength: 1_048_576, PromptUSDPerMTok: 2, CompletionUSDPerMTok: 12, Preferred: true},
		{Author: "x-ai", Lab: "xAI", Slug: "~x-ai/grok-latest", Name: "xAI: Grok Latest",
			ContextLength: 500_000, PromptUSDPerMTok: 2, CompletionUSDPerMTok: 6, Preferred: true},
	}
}

// fixture is a consultation exercising every section the renderers can emit.
func fixture() *council.Consultation {
	return &council.Consultation{
		SchemaVersion: council.SchemaVersion,
		Question:      "A UK company sells SaaS to EU consumers and US businesses. What are the VAT and US sales-tax obligations, and where do they interact?",
		AskedAt:       time.Date(2026, 8, 17, 14, 5, 0, 0, time.UTC),
		ElapsedMS:     168_000,
		Panel:         members(),
		Judge:         "~anthropic/claude-opus-latest",
		Outer:         "~anthropic/claude-opus-latest",
		OuterResolved: "anthropic/claude-opus-5",
		Synthesis:     "Two separate regimes apply and they do not overlap.\n\nFor EU consumers the UK company must register for VAT under the non-Union OSS scheme and charge the customer's local rate from the first sale — there is no threshold. For US business customers, SaaS taxability varies by state and the reverse-charge concept does not exist.",
		Analysis: &openrouter.Analysis{
			Consensus: []string{
				"Non-Union OSS registration is required for EU B2C sales from the first euro; there is no de minimis threshold.",
				"US sales tax is determined state by state, and economic nexus thresholds are evaluated per state.",
			},
			Contradictions: []openrouter.Contradiction{{
				Topic: "Whether B2B sales to EU businesses need OSS registration",
				Stances: []openrouter.Stance{
					{Model: "~openai/gpt-latest", Position: "No — B2B supplies are covered by the reverse charge, so OSS is not needed for them."},
					{Model: "~google/gemini-pro-latest", Position: "Partially — the reverse charge applies only where the customer's VAT number is validated; unvalidated customers must be treated as B2C."},
				},
			}},
			UniqueInsights: []openrouter.UniqueInsight{
				{Model: "~x-ai/grok-latest", Insight: "Several US states tax SaaS as tangible personal property rather than a service, which changes the sourcing rule."},
			},
			PartialCoverage: []openrouter.PartialCoverage{
				{Models: []string{"~anthropic/claude-opus-latest", "~google/gemini-pro-latest"}, Point: "Invoicing must show the customer's VAT number for the reverse charge to be defensible on audit."},
			},
			BlindSpots: []string{
				"No model addressed the UK's own VAT position on the same supplies after the Brexit transition.",
			},
		},
		Responses: []openrouter.PanelAnswer{
			{Model: "~anthropic/claude-opus-latest", Content: "The two regimes are independent...\n\nEU: register for non-Union OSS."},
			{Model: "~google/gemini-pro-latest", Content: "Start with the place-of-supply rules..."},
			{Model: "~x-ai/grok-latest", Content: "Watch the state-level SaaS classification..."},
		},
		Sources: []openrouter.Source{
			{URL: "https://vat-one-stop-shop.ec.europa.eu/index_en", Title: "VAT One Stop Shop — European Commission"},
			{URL: "https://www.gov.uk/guidance/vat-rules-for-supplies-of-digital-services", Title: "VAT rules for supplies of digital services to consumers — GOV.UK"},
		},
		Failed: []openrouter.FailedModel{
			{Model: "~openai/gpt-latest", Error: "Stream completed without producing any text"},
		},
		Usage:      council.Usage{InputTokens: 764, OutputTokens: 3120, TotalTokens: 3884, CostUSD: 0.3348},
		ResponseID: "gen-1786974238-uxGHDllmFw2ZNwQWJQsK",
		Warnings:   []string{"model catalog is 3h old"},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/render -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestHumanGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Human(&buf, fixture(), Style{Width: 88}, false); err != nil {
		t.Fatal(err)
	}
	golden(t, "human.txt", buf.Bytes())
}

func TestHumanFullGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Human(&buf, fixture(), Style{Width: 88}, true); err != nil {
		t.Fatal(err)
	}
	golden(t, "human_full.txt", buf.Bytes())
}

// TestHumanLeadsWithDisagreement guards the whole point of the tool: the section
// naming where models split must come before the ones where they agreed.
func TestHumanLeadsWithDisagreement(t *testing.T) {
	var buf bytes.Buffer
	if err := Human(&buf, fixture(), Style{Width: 88}, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	disagreed := strings.Index(out, "DISAGREED")
	agreed := strings.Index(out, "AGREED")
	if disagreed < 0 || agreed < 0 {
		t.Fatalf("missing sections: disagreed=%d agreed=%d", disagreed, agreed)
	}
	if disagreed > agreed {
		t.Error("DISAGREED must be rendered before AGREED")
	}
	if !strings.Contains(out, "open questions, not settled") {
		t.Error("the disagreement caveat is missing")
	}
	// 3 panellists answered, 1 failed — the footer must say so rather than implying
	// a full panel.
	if !strings.Contains(out, "3 of 4 answered") {
		t.Errorf("footer should report the degraded panel, got:\n%s", out)
	}
}

func TestHumanOmitsEmptySections(t *testing.T) {
	c := fixture()
	c.Analysis = nil
	c.Sources = nil
	c.Failed = nil
	c.Warnings = nil

	var buf bytes.Buffer
	if err := Human(&buf, c, Style{Width: 88}, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, absent := range []string{"DISAGREED", "AGREED", "SOURCES", "DEGRADED", "NOT COVERED"} {
		if strings.Contains(out, absent) {
			t.Errorf("section %q should be omitted when empty:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "ANSWER") {
		t.Error("the synthesis must always render")
	}
}

func TestHumanColorOnlyWhenEnabled(t *testing.T) {
	var plain, colored bytes.Buffer
	if err := Human(&plain, fixture(), Style{Width: 88}, false); err != nil {
		t.Fatal(err)
	}
	if err := Human(&colored, fixture(), Style{Width: 88, Color: true}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("ANSI escapes leaked into uncoloured output")
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Error("colour was requested but no ANSI escapes were emitted")
	}
}

func TestJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fixture()); err != nil {
		t.Fatal(err)
	}
	golden(t, "consultation.json", buf.Bytes())
}

// TestJSONContract pins the top-level keys agents parse. Adding a key is fine;
// renaming or removing one is a breaking change that must bump SchemaVersion.
func TestJSONContract(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, fixture()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	required := []string{
		"schema_version", "question", "asked_at", "elapsed_ms", "panel", "judge",
		"outer", "synthesis", "analysis", "responses", "sources", "failed", "usage",
	}
	var missing []string
	for _, k := range required {
		if _, ok := doc[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the JSON contract is missing %v", missing)
	}

	var analysis map[string]json.RawMessage
	if err := json.Unmarshal(doc["analysis"], &analysis); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"consensus", "contradictions", "partial_coverage", "unique_insights", "blind_spots"} {
		if _, ok := analysis[k]; !ok {
			t.Errorf("analysis is missing %q", k)
		}
	}
}

func TestSkillGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Skill(&buf, &registry.Roster{Members: members()}); err != nil {
		t.Fatal(err)
	}
	golden(t, "SKILL.md", buf.Bytes())
}

// TestSkillShape checks the parts an agent runtime depends on.
func TestSkillShape(t *testing.T) {
	var buf bytes.Buffer
	if err := Skill(&buf, &registry.Roster{Members: members()}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.HasPrefix(out, "---\n") {
		t.Fatal("SKILL.md must open with YAML frontmatter")
	}
	end := strings.Index(out[4:], "\n---\n")
	if end < 0 {
		t.Fatal("frontmatter is not closed")
	}
	front := out[4 : end+4]
	for _, key := range []string{"name:", "description:"} {
		if !strings.Contains(front, key) {
			t.Errorf("frontmatter is missing %q", key)
		}
	}
	// The description is a single line; a stray newline breaks skill parsers.
	for _, line := range strings.Split(front, "\n") {
		if strings.HasPrefix(line, "description:") && len(line) < 40 {
			t.Errorf("description looks truncated: %q", line)
		}
	}
	// The live roster must be baked in, not described abstractly.
	for _, m := range members() {
		if !strings.Contains(out, m.Slug) {
			t.Errorf("SKILL.md does not name %q", m.Slug)
		}
	}
	if !strings.Contains(out, "Never flatten a disagreement") {
		t.Error("the disagreement rule is missing from the generated skill")
	}
	if !strings.Contains(out, "cost_usd") {
		t.Error("the skill should document the cost field")
	}
}

func TestRosterHuman(t *testing.T) {
	res := &registry.Result{
		Roster:    &registry.Roster{Members: members(), Aliases: members()},
		FetchedAt: time.Now().Add(-4 * time.Hour),
		FromCache: true,
	}
	var buf bytes.Buffer
	if err := Roster(&buf, res.Roster, res, Style{Width: 100}, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, m := range members() {
		if !strings.Contains(out, m.Slug) {
			t.Errorf("roster output missing %q", m.Slug)
		}
	}
	if !strings.Contains(out, "4h") {
		t.Errorf("cache age not shown:\n%s", out)
	}
	if !strings.Contains(out, "judge & synthesis") {
		t.Error("the judge should be identified")
	}
}

func TestRosterJSON(t *testing.T) {
	res := &registry.Result{
		Roster:    &registry.Roster{Members: members(), Aliases: members(), Warnings: []string{"careful"}},
		FetchedAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := RosterJSON(&buf, res, true); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SchemaVersion int               `json:"schema_version"`
		Members       []registry.Member `json:"members"`
		Aliases       []registry.Member `json:"aliases"`
		Warnings      []string          `json:"warnings"`
		ExpiresAt     time.Time         `json:"expires_at"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != council.SchemaVersion || len(doc.Members) != 4 || len(doc.Aliases) != 4 {
		t.Errorf("unexpected roster document: %+v", doc)
	}
	if want := res.FetchedAt.Add(registry.TTL); !doc.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", doc.ExpiresAt, want)
	}
	if len(doc.Warnings) != 1 {
		t.Error("warnings should survive into JSON")
	}
}

func TestWrapPreservesStructure(t *testing.T) {
	in := "para one is quite long and will need to be broken across lines\n\n```\ncode  spaced  out\n```\n\n- a bullet"
	got := wrap(in, 30, "  ")
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "```") {
		t.Error("fences lost")
	}
	if !strings.Contains(joined, "code  spaced  out") {
		t.Error("fenced content must not be re-wrapped or re-spaced")
	}
	if !strings.Contains(joined, "- a bullet") {
		t.Error("list marker lost")
	}
	for _, line := range got {
		if runeLen(line) > 30 && !strings.Contains(line, "code") {
			t.Errorf("line exceeds width: %q", line)
		}
	}
}

func TestCompactDuration(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond:  "500ms",
		2500 * time.Millisecond: "2.5s",
		168 * time.Second:       "2m48s",
		4 * time.Hour:           "4h00m",
		50 * time.Hour:          "2d",
	}
	for d, want := range cases {
		if got := compactDuration(d); got != want {
			t.Errorf("compactDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("a very long title indeed", 10); runeLen(got) > 10 {
		t.Errorf("truncate too long: %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate altered a short string: %q", got)
	}
}

func TestAutoStyleRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "72")
	s := AutoStyle(os.Stdout)
	if s.Color {
		t.Error("NO_COLOR must disable colour")
	}
	if s.Width != 72 {
		t.Errorf("width = %d, want 72 from COLUMNS", s.Width)
	}

	t.Setenv("COLUMNS", "5") // nonsense, must be ignored
	if s := AutoStyle(os.Stdout); s.Width != DefaultWidth {
		t.Errorf("width = %d, want the default for an implausible COLUMNS", s.Width)
	}
}
