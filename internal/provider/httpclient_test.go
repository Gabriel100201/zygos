package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testClient is newHTTPClient with the backoff collapsed so tests stay fast.
func testClient(max int) *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &retryTransport{
			base:    http.DefaultTransport,
			max:     max,
			initial: time.Millisecond,
		},
	}
}

func TestRetryTransportRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := testClient(3).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d calls, want 3 (two 429s then success)", got)
	}
}

// A 429 rejects the request before the server acts on it, so replaying a write
// is safe — and necessary, since an agent fires several tool calls in a row.
func TestRetryTransportReplaysBodyOnRateLimit(t *testing.T) {
	var calls atomic.Int32
	bodies := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := testClient(3).Post(srv.URL, "application/json", strings.NewReader(`{"title":"hola"}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	resp.Body.Close()
	close(bodies)

	n := 0
	for got := range bodies {
		n++
		if got != `{"title":"hola"}` {
			t.Errorf("attempt %d sent body %q, want the original payload", n, got)
		}
	}
	if n != 2 {
		t.Errorf("server saw %d attempts, want 2", n)
	}
}

func TestRetryTransportRetriesServerErrorsOnReads(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := testClient(3).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// Replaying a failed POST could create the same task twice, which is worse than
// surfacing the error.
func TestRetryTransportDoesNotReplayWritesOnServerError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	resp, err := testClient(3).Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want the 500 to surface", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("server saw %d calls, want exactly 1 — a failed write must not be replayed", got)
	}
}

func TestRetryTransportGivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	resp, err := testClient(2).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want the final 429 to surface", resp.StatusCode)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d calls, want 3 (initial + 2 retries)", got)
	}
}

func TestRetryTransportStopsWhenContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	client := &http.Client{Transport: &retryTransport{base: http.DefaultTransport, max: 50, initial: 10 * time.Millisecond}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retry loop ignored the cancelled context")
	}
}

func TestRetryAfterHeader(t *testing.T) {
	fallback := 500 * time.Millisecond
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"absent falls back", "", fallback},
		{"delay in seconds", "2", 2 * time.Second},
		{"unparsable falls back", "soon", fallback},
		{"negative falls back", "-5", fallback},
		{"clamped to the ceiling", "3600", maxBackoff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("Retry-After", tc.header)
			}
			if got := retryAfter(h, fallback); got != tc.want {
				t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestRetryAfterAcceptsHTTPDate(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	got := retryAfter(h, time.Minute)
	if got <= 0 || got > maxBackoff {
		t.Errorf("retryAfter(HTTP-date) = %v, want a positive value under %v", got, maxBackoff)
	}
}

func TestRetryableStatus(t *testing.T) {
	cases := []struct {
		code   int
		method string
		want   bool
	}{
		{http.StatusTooManyRequests, http.MethodGet, true},
		{http.StatusTooManyRequests, http.MethodPost, true},
		{http.StatusTooManyRequests, http.MethodPatch, true},
		{http.StatusInternalServerError, http.MethodGet, true},
		{http.StatusServiceUnavailable, http.MethodHead, true},
		{http.StatusInternalServerError, http.MethodPost, false},
		{http.StatusBadGateway, http.MethodPatch, false},
		{http.StatusNotFound, http.MethodGet, false},
		{http.StatusUnauthorized, http.MethodGet, false},
		{http.StatusOK, http.MethodGet, false},
	}
	for _, tc := range cases {
		if got := retryableStatus(tc.code, tc.method); got != tc.want {
			t.Errorf("retryableStatus(%d, %s) = %v, want %v", tc.code, tc.method, got, tc.want)
		}
	}
}

func TestNewHTTPClientHasATimeout(t *testing.T) {
	// Without this the client waits forever on a hung server, and the only thing
	// standing between the agent and a permanent stall is the caller's context.
	if c := newHTTPClient(); c.Timeout <= 0 {
		t.Fatal("newHTTPClient must set a timeout")
	}
}

func TestRetryTransportSurfacesNetworkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more

	_, err := testClient(1).Get(url)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("connection failure was misreported as cancellation: %v", err)
	}
}
