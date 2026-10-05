package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestTimeoutAndMaxAttempts(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New("k")
	c.BaseURL = srv.URL
	c.MaxAttempts = 1
	c.RequestTimeout = 50 * time.Millisecond
	_, err := c.get(context.Background(), "/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}
