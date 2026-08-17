package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestParseCapturedResponse pins the parser to a real API response captured from a
// live fusion run, so a change in either the parser or the API surfaces here.
func TestParseCapturedResponse(t *testing.T) {
	raw, err := os.ReadFile("testdata/fusion_response.json")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	if resp.Status != "completed" {
		t.Errorf("status = %q, want completed", resp.Status)
	}
	// The top-level model reports the concrete model the alias resolved to.
	if resp.Model != "anthropic/claude-opus-5" {
		t.Errorf("model = %q, want the resolved concrete slug", resp.Model)
	}
	if !strings.HasPrefix(resp.ID, "gen-") {
		t.Errorf("id = %q, want a gen- generation id", resp.ID)
	}
	if resp.Usage.Cost <= 0 {
		t.Errorf("usage.cost = %v, want a positive billed cost", resp.Usage.Cost)
	}

	if text := resp.Text(); !strings.Contains(text, "Delaware C-corp") {
		t.Errorf("Text() did not return the synthesis, got %.80q", text)
	}

	fi := resp.Fusion()
	if fi == nil {
		t.Fatal("Fusion() returned nil for a response that contains a panel item")
	}
	if len(fi.Responses) != 2 {
		t.Errorf("got %d panel answers, want 2", len(fi.Responses))
	}
	for _, r := range fi.Responses {
		if r.Model == "" || r.Content == "" {
			t.Errorf("panel answer incomplete: %+v", r)
		}
	}
	if len(fi.Analysis.Consensus) != 4 {
		t.Errorf("got %d consensus points, want 4", len(fi.Analysis.Consensus))
	}
	if len(fi.Sources) != 5 {
		t.Errorf("got %d sources, want 5", len(fi.Sources))
	}
	// This run degraded: one panellist was asked but produced nothing.
	if len(fi.FailedModels) != 1 {
		t.Fatalf("got %d failed models, want 1", len(fi.FailedModels))
	}
	if fi.FailedModels[0].Model != "~openai/gpt-latest" {
		t.Errorf("failed model = %q", fi.FailedModels[0].Model)
	}
}

// TestFusionAbsentWhenToolNotInvoked covers the trap the spike uncovered: with
// tool_choice left as "auto" the request succeeds but no panel ever convenes.
func TestFusionAbsentWhenToolNotInvoked(t *testing.T) {
	raw := []byte(`{"id":"gen-1","model":"anthropic/claude-opus-5","status":"completed",
	  "output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking"}]},
	            {"type":"message","content":[{"type":"output_text","text":"a solo answer"}]}],
	  "usage":{"cost":0.0089}}`)

	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fi := resp.Fusion(); fi != nil {
		t.Error("Fusion() should be nil when the panel never convened")
	}
	if got := resp.Text(); got != "a solo answer" {
		t.Errorf("Text() = %q", got)
	}
}

func TestUnknownOutputItemsIgnored(t *testing.T) {
	raw := []byte(`{"status":"completed","output":[
	  {"type":"some_future_tool","payload":{"anything":true}},
	  {"type":"message","content":[{"type":"output_text","text":"kept"},{"type":"refusal","text":"dropped"}]}
	]}`)

	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Text(); got != "kept" {
		t.Errorf("Text() = %q, want only the output_text part", got)
	}
}

func TestAnalysisAcceptsStringsOrObjects(t *testing.T) {
	// The judge is a language model; the docs promise objects but strings occur.
	raw := []byte(`{"status":"completed","output":[{"type":"openrouter:fusion","status":"completed",
	  "analysis":{
	    "consensus":["agreed point"],
	    "contradictions":[{"topic":"tax","stances":[{"model":"a","position":"yes"},"b says no"]},"bare string"],
	    "unique_insights":[{"model":"a","insight":"only a saw this"}],
	    "partial_coverage":[{"models":["a","b"],"point":"partly covered"}],
	    "blind_spots":["nobody covered this"]}}]}`)

	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := resp.Fusion().Analysis
	if len(a.Contradictions) != 2 {
		t.Fatalf("got %d contradictions, want 2", len(a.Contradictions))
	}
	if a.Contradictions[0].Topic != "tax" {
		t.Errorf("object form lost: %+v", a.Contradictions[0])
	}
	if len(a.Contradictions[0].Stances) != 2 || a.Contradictions[0].Stances[1].Text != "b says no" {
		t.Errorf("mixed stance forms lost: %+v", a.Contradictions[0].Stances)
	}
	if a.Contradictions[1].Text != "bare string" {
		t.Errorf("string form lost: %+v", a.Contradictions[1])
	}
	if a.IsEmpty() {
		t.Error("IsEmpty() should be false for a populated analysis")
	}
}

func TestAnalysisIsEmpty(t *testing.T) {
	var nilAnalysis *Analysis
	if !nilAnalysis.IsEmpty() {
		t.Error("a nil analysis is empty")
	}
	if !(&Analysis{}).IsEmpty() {
		t.Error("a zero analysis is empty")
	}
}

func TestHardFailureSurfacesReason(t *testing.T) {
	raw := []byte(`{"status":"completed","output":[{"type":"openrouter:fusion","status":"error",
	  "error":"all panel models failed","failure_reason":"all_panels_failed"}]}`)

	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	fi := resp.Fusion()
	if fi.Status != "error" || fi.FailureReason != "all_panels_failed" {
		t.Errorf("hard failure not surfaced: %+v", fi)
	}
}

func TestFuseSendsForcedToolChoice(t *testing.T) {
	var got FusionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		// Slow OpenRouter responses arrive with whitespace keep-alive padding.
		w.Write([]byte("\n     \n     \n"))
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer srv.Close()

	c := New("test-key")
	c.BaseURL = srv.URL
	resp, _, err := c.Fuse(context.Background(), &FusionRequest{
		Model:      "~anthropic/claude-opus-latest",
		Input:      []InputItem{{Role: "user", Content: "q"}},
		Tools:      []Tool{{Type: FusionToolType, Parameters: ToolParams{AnalysisModels: []string{"~openai/gpt-latest"}}}},
		ToolChoice: ForceFusion(),
	})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if resp.Text() != "ok" {
		t.Errorf("keep-alive padding broke decoding: %q", resp.Text())
	}

	choice, ok := got.ToolChoice.(map[string]any)
	if !ok || choice["type"] != FusionToolType {
		t.Errorf("tool_choice = %#v, want the fusion tool named", got.ToolChoice)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != FusionToolType {
		t.Errorf("tools = %#v", got.Tools)
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":429,"message":"rate limited"}}`))
			return
		}
		w.Write([]byte(`{"status":"completed","output":[]}`))
	}))
	defer srv.Close()

	c := New("k")
	c.BaseURL = srv.URL
	if _, _, err := c.Models(context.Background()); err == nil {
		// Models on an empty catalog errors, but the retry must have happened first.
		t.Log("unexpected success")
	}
	if attempts != 3 {
		t.Errorf("made %d attempts, want 3", attempts)
	}
}

func TestDoesNotRetryClientErrors(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":401,"message":"No auth credentials found"}}`))
	}))
	defer srv.Close()

	c := New("bad")
	c.BaseURL = srv.URL
	_, _, err := c.Models(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("got %T, want *APIError", err)
	}
	if apiErr.StatusCode != 401 || !strings.Contains(apiErr.Message, "No auth credentials") {
		t.Errorf("unexpected error: %+v", apiErr)
	}
	if attempts != 1 {
		t.Errorf("made %d attempts, want 1 — 4xx must not be retried", attempts)
	}
}

func TestContextCancellationIsFinal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New("k")
	c.BaseURL = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, _, err := c.Models(ctx); err == nil {
		t.Fatal("expected a context error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v — a cancelled context must not be retried", elapsed)
	}
}

func TestMalformedJSON(t *testing.T) {
	if _, err := ParseResponse([]byte("not json")); err == nil {
		t.Error("expected an error for malformed JSON")
	}
	if _, err := ParseModels([]byte(`{"data":[]}`)); err == nil {
		t.Error("expected an error for an empty catalog")
	}
}
