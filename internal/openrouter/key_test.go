package openrouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKeyParsesLiveShape(t *testing.T) {
	// Captured from a real GET /api/v1/key response, trimmed to the fields used.
	const body = `{"data":{"label":"sk-or-v1-abc...xyz","is_management_key":false,
	  "is_provisioning_key":false,"limit":null,"limit_remaining":null,
	  "usage":2.839930625,"is_free_tier":false,"rate_limit":{"requests":-1}}}`

	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New("sk-or-v1-test")
	c.BaseURL = srv.URL
	info, err := c.Key(context.Background())
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if gotPath != "/key" {
		t.Errorf("path = %q, want /key", gotPath)
	}
	if gotAuth != "Bearer sk-or-v1-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if info.Label != "sk-or-v1-abc...xyz" {
		t.Errorf("label = %q", info.Label)
	}
	if info.Usage != 2.839930625 {
		t.Errorf("usage = %v", info.Usage)
	}
	if info.Limit != nil {
		t.Errorf("limit = %v, want nil for an uncapped key", *info.Limit)
	}
	if got, want := info.Spend(), "$2.84 used, no spend limit set"; got != want {
		t.Errorf("Spend() = %q, want %q", got, want)
	}
}

func TestKeySpendWithLimit(t *testing.T) {
	limit, remaining := 10.0, 7.5
	k := &KeyInfo{Usage: 2.5, Limit: &limit, LimitRemaining: &remaining}
	if got, want := k.Spend(), "$2.50 used of $10.00, $7.50 remaining"; got != want {
		t.Errorf("Spend() = %q, want %q", got, want)
	}
}

func TestKeyRejectsBadCredentials(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":401,"message":"User not found."}}`))
	}))
	defer srv.Close()

	c := New("sk-or-v1-bogus")
	c.BaseURL = srv.URL
	_, err := c.Key(context.Background())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an *APIError so the CLI can report exit code 4", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", apiErr.StatusCode)
	}
	// A bad key is not transient; retrying it three times just makes setup slow.
	if attempts != 1 {
		t.Errorf("made %d attempts, want 1", attempts)
	}
}

func TestKeyRejectsEmptyEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New("k")
	c.BaseURL = srv.URL
	if _, err := c.Key(context.Background()); err == nil {
		t.Error("a response with no data should be an error, not a zero KeyInfo")
	}
}
