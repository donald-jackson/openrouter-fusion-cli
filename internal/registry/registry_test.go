package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
)

func loadFixture(t *testing.T) *openrouter.ModelsResponse {
	t.Helper()
	raw, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := openrouter.ParseModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// TestBuildSeatsFourLabs pins the roster against a real catalog snapshot.
func TestBuildSeatsFourLabs(t *testing.T) {
	roster, err := Build(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}

	want := []struct{ lab, slug string }{
		{"OpenAI", "~openai/gpt-latest"},
		{"Anthropic", "~anthropic/claude-opus-latest"},
		{"Google", "~google/gemini-pro-latest"},
		{"xAI", "~x-ai/grok-latest"},
	}
	if len(roster.Members) != len(want) {
		t.Fatalf("got %d members, want %d: %+v", len(roster.Members), len(want), roster.Slugs())
	}
	for i, w := range want {
		got := roster.Members[i]
		if got.Lab != w.lab || got.Slug != w.slug {
			t.Errorf("seat %d = %s/%s, want %s/%s", i, got.Lab, got.Slug, w.lab, w.slug)
		}
		if !got.Preferred {
			t.Errorf("seat %d (%s) should be a preferred pick", i, got.Slug)
		}
	}
	if len(roster.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", roster.Warnings)
	}

	// Anthropic publishes a pricier alias than Opus; the family preference must win
	// so the seat does not silently swap to Fable.
	anthropic := roster.Members[1]
	if anthropic.Slug == "~anthropic/claude-fable-latest" {
		t.Error("price beat the family preference for the Anthropic seat")
	}
	if anthropic.CompletionUSDPerMTok != 25 {
		t.Errorf("Anthropic completion price = %v per Mtok, want 25", anthropic.CompletionUSDPerMTok)
	}
	if anthropic.ContextLength != 1_000_000 {
		t.Errorf("Anthropic context = %d", anthropic.ContextLength)
	}
}

func TestBuildExcludesNonAliases(t *testing.T) {
	roster, err := Build(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range append(roster.Members, roster.Aliases...) {
		if !strings.HasPrefix(m.Slug, "~") {
			t.Errorf("%q is not an alias; concrete models must never be seated", m.Slug)
		}
	}
	// The fixture holds 11 aliases alongside concrete models.
	if len(roster.Aliases) != 11 {
		t.Errorf("got %d aliases, want 11", len(roster.Aliases))
	}
}

func TestBuildNeverSeatsMiniTier(t *testing.T) {
	catalog := loadFixture(t)
	// Drop OpenAI's flagship alias, leaving only the mini tier available.
	var kept []openrouter.Model
	for _, m := range catalog.Data {
		if m.ID != "~openai/gpt-latest" {
			kept = append(kept, m)
		}
	}
	roster, err := Build(&openrouter.ModelsResponse{Data: kept})
	if err != nil {
		t.Fatal(err)
	}
	// gpt-mini is the only OpenAI alias left, and it is excluded, so the seat is
	// left empty rather than filled with a cheaper tier.
	if seat, ok := labSeat(roster, "OpenAI"); ok {
		t.Errorf("OpenAI seated %q; the mini tier is excluded even as a fallback", seat.Slug)
	}
	if len(roster.Members) != 3 {
		t.Errorf("got %d members, want the other 3 labs still seated", len(roster.Members))
	}
	if len(roster.Warnings) == 0 {
		t.Error("an empty seat must warn")
	}
	if !strings.Contains(strings.Join(roster.Warnings, " "), "gpt-mini") {
		t.Errorf("the warning should name what was rejected: %v", roster.Warnings)
	}
}

func TestBuildFallbackWithinLabIsMarkedUnpreferred(t *testing.T) {
	catalog := loadFixture(t)
	// Drop Anthropic's whole preference list, leaving only Haiku — not excluded,
	// so it is seated, but flagged as a fallback rather than a preferred pick.
	var kept []openrouter.Model
	for _, m := range catalog.Data {
		switch m.ID {
		case "~anthropic/claude-opus-latest", "~anthropic/claude-fable-latest", "~anthropic/claude-sonnet-latest":
			continue
		}
		kept = append(kept, m)
	}
	roster, err := Build(&openrouter.ModelsResponse{Data: kept})
	if err != nil {
		t.Fatal(err)
	}
	seat, ok := labSeat(roster, "Anthropic")
	if !ok {
		t.Fatal("Anthropic seat missing")
	}
	if seat.Slug != "~anthropic/claude-haiku-latest" {
		t.Errorf("seated %q, want the only remaining alias", seat.Slug)
	}
	if seat.Preferred {
		t.Error("a fallback pick should be marked Preferred=false")
	}
}

func TestBuildFallsBackToPriciestAlias(t *testing.T) {
	// A lab whose families are all unknown to us still gets seated by price.
	catalog := &openrouter.ModelsResponse{Data: []openrouter.Model{
		{ID: "~google/gemini-ultra-latest", Name: "cheap", Pricing: openrouter.Pricing{Prompt: "0.000001", Completion: "0.000002"}},
		{ID: "~google/gemini-omega-latest", Name: "dear", Pricing: openrouter.Pricing{Prompt: "0.00001", Completion: "0.00009"}},
	}}
	roster, err := Build(catalog)
	if err != nil {
		t.Fatal(err)
	}
	seat, ok := labSeat(roster, "Google")
	if !ok {
		t.Fatal("Google seat missing")
	}
	if seat.Slug != "~google/gemini-omega-latest" {
		t.Errorf("fallback picked %q, want the priciest alias", seat.Slug)
	}
	if seat.Preferred {
		t.Error("fallback picks must not be marked preferred")
	}
}

func TestBuildTolerartesMissingLab(t *testing.T) {
	catalog := &openrouter.ModelsResponse{Data: []openrouter.Model{
		{ID: "~anthropic/claude-opus-latest", Pricing: openrouter.Pricing{Completion: "0.000025"}},
	}}
	roster, err := Build(catalog)
	if err != nil {
		t.Fatalf("a partial council must not be an error: %v", err)
	}
	if len(roster.Members) != 1 {
		t.Fatalf("got %d members, want 1", len(roster.Members))
	}
	if len(roster.Warnings) != 3 {
		t.Errorf("got %d warnings, want one per unseated lab: %v", len(roster.Warnings), roster.Warnings)
	}
}

func TestBuildRejectsCatalogWithoutAliases(t *testing.T) {
	catalog := &openrouter.ModelsResponse{Data: []openrouter.Model{{ID: "anthropic/claude-opus-5"}}}
	if _, err := Build(catalog); err == nil {
		t.Error("a catalog with no aliases must be an error, not an empty council")
	}
	if _, err := Build(nil); err == nil {
		t.Error("a nil catalog must be an error")
	}
}

func TestRosterHelpers(t *testing.T) {
	roster, err := Build(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := roster.Slugs(); len(got) != 4 {
		t.Errorf("Slugs() = %v", got)
	}
	if _, ok := roster.Find("~x-ai/grok-latest"); !ok {
		t.Error("Find should locate a seated member")
	}
	if _, ok := roster.Find("~moonshotai/kimi-latest"); !ok {
		t.Error("Find should fall back to the full alias list")
	}
	if _, ok := roster.Find("nope"); ok {
		t.Error("Find should miss an unknown slug")
	}
}

// --- cache ---

func newCatalogServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Write(body)
	}))
}

func TestCacheAvoidsSecondFetch(t *testing.T) {
	var hits int
	srv := newCatalogServer(t, &hits)
	defer srv.Close()

	c := newTestCatalog(t, srv.URL, time.Now)

	first, err := c.Load(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache {
		t.Error("the first load must hit the network")
	}

	second, err := c.Load(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Error("the second load must be served from cache")
	}
	if hits != 1 {
		t.Errorf("made %d requests, want 1", hits)
	}
	if len(second.Roster.Members) != 4 {
		t.Errorf("cached roster lost members: %v", second.Roster.Slugs())
	}
}

func TestCacheExpiresAfterTTL(t *testing.T) {
	var hits int
	srv := newCatalogServer(t, &hits)
	defer srv.Close()

	now := time.Now()
	c := newTestCatalog(t, srv.URL, func() time.Time { return now })

	if _, err := c.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(TTL + time.Minute)
	res, err := c.Load(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.FromCache {
		t.Error("an expired entry must trigger a refetch")
	}
	if hits != 2 {
		t.Errorf("made %d requests, want 2", hits)
	}
}

func TestRefreshForcesFetch(t *testing.T) {
	var hits int
	srv := newCatalogServer(t, &hits)
	defer srv.Close()

	c := newTestCatalog(t, srv.URL, time.Now)
	if _, err := c.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Load(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("made %d requests, want 2 — refresh must bypass the cache", hits)
	}
}

// TestStaleCacheServedWhenRefreshFails is the behaviour that keeps a consultation
// possible when OpenRouter's catalog endpoint is briefly unreachable.
func TestStaleCacheServedWhenRefreshFails(t *testing.T) {
	var hits int
	var down bool
	body, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if down {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"code":500,"message":"upstream down"}}`))
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	now := time.Now()
	var warnings []string
	c := newTestCatalog(t, srv.URL, func() time.Time { return now })
	c.Warn = func(m string) { warnings = append(warnings, m) }

	if _, err := c.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	down = true
	now = now.Add(TTL + time.Hour)

	res, err := c.Load(context.Background(), false)
	if err != nil {
		t.Fatalf("a failed refresh with a cache present must not be fatal: %v", err)
	}
	if !res.Stale || !res.FromCache {
		t.Errorf("expected a stale cache hit, got %+v", res)
	}
	if len(res.Roster.Members) != 4 {
		t.Error("stale roster should still be complete")
	}
	if len(warnings) == 0 {
		t.Error("serving a stale catalog must warn")
	}
}

func TestNoCacheAndFailedFetchIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":401,"message":"nope"}}`))
	}))
	defer srv.Close()

	c := newTestCatalog(t, srv.URL, time.Now)
	if _, err := c.Load(context.Background(), false); err == nil {
		t.Error("with no cache and a failed fetch there is nothing to serve")
	}
}

func TestCorruptCacheIsRecoveredFrom(t *testing.T) {
	var hits int
	srv := newCatalogServer(t, &hits)
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cacheFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Catalog{Client: clientFor(srv.URL), Dir: dir, Now: time.Now}
	res, err := c.Load(context.Background(), false)
	if err != nil {
		t.Fatalf("a corrupt cache should be replaced, not fatal: %v", err)
	}
	if res.FromCache {
		t.Error("a corrupt cache must not be served")
	}
}

func TestCacheWriteIsAtomic(t *testing.T) {
	var hits int
	srv := newCatalogServer(t, &hits)
	defer srv.Close()

	dir := t.TempDir()
	c := &Catalog{Client: clientFor(srv.URL), Dir: dir, Now: time.Now}
	if _, err := c.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != cacheFile {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache dir = %v, want exactly %q with no temp files left behind", names, cacheFile)
	}

	// The stored body must be the raw API payload, not a re-encoding of our structs.
	raw, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		t.Fatal(err)
	}
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(e.Body) {
		t.Error("cached body is not valid JSON")
	}
	if e.FetchedAt.IsZero() {
		t.Error("cache entry has no timestamp")
	}
}

func newTestCatalog(t *testing.T, baseURL string, now func() time.Time) *Catalog {
	t.Helper()
	return &Catalog{Client: clientFor(baseURL), Dir: t.TempDir(), Now: now}
}

func clientFor(baseURL string) *openrouter.Client {
	c := openrouter.New("test-key")
	c.BaseURL = baseURL
	return c
}

func labSeat(r *Roster, lab string) (Member, bool) {
	for _, m := range r.Members {
		if m.Lab == lab {
			return m, true
		}
	}
	return Member{}, false
}
