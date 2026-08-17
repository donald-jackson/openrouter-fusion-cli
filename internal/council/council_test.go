package council

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// fakeAsker records the request it was given and replays a canned response.
type fakeAsker struct {
	got *openrouter.FusionRequest
	raw []byte
	err error
}

func (f *fakeAsker) Fuse(ctx context.Context, req *openrouter.FusionRequest) (*openrouter.Response, []byte, error) {
	f.got = req
	if f.err != nil {
		return nil, nil, f.err
	}
	resp, err := openrouter.ParseResponse(f.raw)
	return resp, f.raw, err
}

func fixtureAsker(t *testing.T) *fakeAsker {
	t.Helper()
	raw, err := os.ReadFile("../openrouter/testdata/fusion_response.json")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeAsker{raw: raw}
}

func testRoster() *registry.Roster {
	return &registry.Roster{Members: []registry.Member{
		{Lab: "OpenAI", Slug: "~openai/gpt-latest", Name: "OpenAI GPT Latest"},
		{Lab: "Google", Slug: "~google/gemini-pro-latest", Name: "Google Gemini Pro Latest"},
		{Lab: "xAI", Slug: "~x-ai/grok-latest", Name: "xAI: Grok Latest"},
	}}
}

func TestAskBuildsForcedFusionRequest(t *testing.T) {
	f := fixtureAsker(t)
	_, _, err := Ask(context.Background(), f, Request{Question: "q", Roster: testRoster()})
	if err != nil {
		t.Fatal(err)
	}

	if f.got.ToolChoice != openrouter.ForceFusion() {
		t.Errorf("tool_choice = %#v; the panel must be forced, not left to the model", f.got.ToolChoice)
	}
	if len(f.got.Tools) != 1 || f.got.Tools[0].Type != openrouter.FusionToolType {
		t.Fatalf("tools = %#v", f.got.Tools)
	}
	params := f.got.Tools[0].Parameters
	if len(params.AnalysisModels) != 3 {
		t.Errorf("panel = %v, want the roster's 3 slugs", params.AnalysisModels)
	}
	// Judge and outer both default to the first panel member.
	if params.Model != "~openai/gpt-latest" || f.got.Model != "~openai/gpt-latest" {
		t.Errorf("judge = %q, outer = %q", params.Model, f.got.Model)
	}
	if params.MaxToolCalls != DefaultMaxToolCalls {
		t.Errorf("max_tool_calls = %d, want %d", params.MaxToolCalls, DefaultMaxToolCalls)
	}
}

func TestAskRespectsOverrides(t *testing.T) {
	f := fixtureAsker(t)
	temp := 0.3
	_, _, err := Ask(context.Background(), f, Request{
		Question:            "q",
		Panel:               []string{"a", "b"},
		Judge:               "judge-model",
		Outer:               "outer-model",
		System:              "be terse",
		MaxToolCalls:        9,
		MaxCompletionTokens: 500,
		Temperature:         &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := f.got.Tools[0].Parameters
	if len(params.AnalysisModels) != 2 || params.AnalysisModels[0] != "a" {
		t.Errorf("panel override lost: %v", params.AnalysisModels)
	}
	if params.Model != "judge-model" || f.got.Model != "outer-model" {
		t.Errorf("judge/outer overrides lost: %q / %q", params.Model, f.got.Model)
	}
	if f.got.Instructions != "be terse" {
		t.Errorf("system prompt lost: %q", f.got.Instructions)
	}
	if params.MaxToolCalls != 9 || params.MaxCompletionTokens != 500 {
		t.Errorf("limits lost: %+v", params)
	}
	if params.Temperature == nil || *params.Temperature != 0.3 {
		t.Errorf("temperature lost: %v", params.Temperature)
	}
}

func TestAskMapsCapturedResponse(t *testing.T) {
	f := fixtureAsker(t)
	c, raw, err := Ask(context.Background(), f, Request{Question: "q", Roster: testRoster()})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Error("raw body should be returned for --raw")
	}

	if c.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %d", c.SchemaVersion)
	}
	if c.Answered() != 2 {
		t.Errorf("answered = %d, want 2", c.Answered())
	}
	if !c.Degraded() {
		t.Error("the captured run lost a panellist and must report as degraded")
	}
	if c.Failed[0].Model != "~openai/gpt-latest" {
		t.Errorf("failed panellist = %q", c.Failed[0].Model)
	}
	if c.Synthesis == "" {
		t.Error("synthesis is empty")
	}
	if c.Analysis == nil || len(c.Analysis.Consensus) != 4 {
		t.Errorf("analysis not carried through: %+v", c.Analysis)
	}
	if c.Disagreed() {
		t.Error("the captured run had no contradictions")
	}
	if c.Usage.CostUSD <= 0 {
		t.Errorf("cost = %v, want the full run cost", c.Usage.CostUSD)
	}
	if c.OuterResolved != "anthropic/claude-opus-5" {
		t.Errorf("outer_resolved = %q, want the concrete model the alias resolved to", c.OuterResolved)
	}
	if c.ElapsedMS < 0 {
		t.Error("elapsed should be non-negative")
	}
	// Panel entries must be enriched from the roster.
	if c.Panel[0].Lab != "OpenAI" {
		t.Errorf("panel metadata lost: %+v", c.Panel[0])
	}
}

func TestAskPanelNotConvened(t *testing.T) {
	f := &fakeAsker{raw: []byte(`{"status":"completed","output":[
	  {"type":"message","content":[{"type":"output_text","text":"a lone answer"}]}]}`)}
	_, _, err := Ask(context.Background(), f, Request{Question: "q", Roster: testRoster()})
	if !errors.Is(err, ErrPanelNotConvened) {
		t.Fatalf("got %v, want ErrPanelNotConvened", err)
	}
}

func TestAskHardFailure(t *testing.T) {
	f := &fakeAsker{raw: []byte(`{"status":"completed","output":[{"type":"openrouter:fusion",
	  "status":"error","error":"all panel models failed","failure_reason":"all_panels_failed"}]}`)}
	_, _, err := Ask(context.Background(), f, Request{Question: "q", Roster: testRoster()})
	var failed *ErrFusionFailed
	if !errors.As(err, &failed) {
		t.Fatalf("got %T (%v), want *ErrFusionFailed", err, err)
	}
	if failed.Reason != "all_panels_failed" {
		t.Errorf("reason = %q", failed.Reason)
	}
}

func TestAskEmptyAnalysisBecomesNil(t *testing.T) {
	f := &fakeAsker{raw: []byte(`{"status":"completed","output":[
	  {"type":"openrouter:fusion","status":"completed","responses":[{"model":"a","content":"x"}],
	   "analysis":{"consensus":[],"contradictions":[],"blind_spots":[]}},
	  {"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`)}
	c, _, err := Ask(context.Background(), f, Request{Question: "q", Panel: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Analysis != nil {
		t.Errorf("an entirely empty analysis should be omitted, got %+v", c.Analysis)
	}
	if c.Degraded() {
		t.Error("no failures means not degraded")
	}
}

func TestAskRequiresAPanel(t *testing.T) {
	f := fixtureAsker(t)
	if _, _, err := Ask(context.Background(), f, Request{Question: "q"}); err == nil {
		t.Error("expected an error with neither panel nor roster")
	}
	if _, _, err := Ask(context.Background(), f, Request{Question: "q", Roster: &registry.Roster{}}); err == nil {
		t.Error("expected an error with an empty roster")
	}
}

func TestAskPropagatesClientError(t *testing.T) {
	want := errors.New("boom")
	f := &fakeAsker{err: want}
	if _, _, err := Ask(context.Background(), f, Request{Question: "q", Panel: []string{"a"}}); !errors.Is(err, want) {
		t.Errorf("got %v, want the client error", err)
	}
}
