package provider

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// requestTimeout bounds a single HTTP round trip, retries included. The
	// Registry also applies a per-operation context deadline; this is the floor
	// that protects any call made outside of it.
	requestTimeout = 60 * time.Second

	defaultMaxRetries     = 3
	defaultInitialBackoff = 500 * time.Millisecond
	maxBackoff            = 5 * time.Second
)

// newHTTPClient returns the client every provider talks to its API through.
//
// Providers are hit by an agent that fires several tool calls back to back, so
// rate limiting is normal traffic rather than an exceptional condition: the
// transport absorbs 429s with backoff instead of surfacing them as failures.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &retryTransport{
			base:    http.DefaultTransport,
			max:     defaultMaxRetries,
			initial: defaultInitialBackoff,
		},
	}
}

// retryTransport replays rate-limited and transiently failed requests with
// exponential backoff, honouring Retry-After when the server sends it.
type retryTransport struct {
	base    http.RoundTripper
	max     int
	initial time.Duration
}

func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	backoff := rt.initial

	for attempt := 0; ; attempt++ {
		attemptReq := req
		if attempt > 0 {
			var err error
			if attemptReq, err = rewind(req); err != nil {
				return nil, err
			}
		}

		resp, err := rt.base.RoundTrip(attemptReq)

		var wait time.Duration
		switch {
		case err != nil:
			// A dead context is final — never spend retries on a caller that
			// already gave up.
			if req.Context().Err() != nil || !replayable(req) {
				return nil, err
			}
			wait = backoff
		case retryableStatus(resp.StatusCode, req.Method):
			wait = retryAfter(resp.Header, backoff)
		default:
			return resp, nil
		}

		if attempt >= rt.max {
			return resp, err
		}
		if resp != nil {
			// Drain a little before closing so the connection returns to the pool.
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			resp.Body.Close()
		}

		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// retryableStatus reports whether a response is worth replaying.
//
// A 429 is always safe: the request was rejected before the server acted on it.
// A 5xx is only safe for reads — replaying a failed POST risks creating the
// same task twice, which is worse than surfacing the error.
func retryableStatus(code int, method string) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	if code < 500 {
		return false
	}
	return method == http.MethodGet || method == http.MethodHead
}

// replayable reports whether the request body can be produced a second time.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func rewind(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.GetBody == nil {
		return clone, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewinding request body for retry: %w", err)
	}
	clone.Body = body
	return clone, nil
}

// retryAfter reads the Retry-After header in either of its two legal forms,
// falling back to the caller's backoff and never waiting longer than maxBackoff
// — a hostile or misconfigured value must not stall the agent.
func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	wait := fallback
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			wait = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				wait = d
			}
		}
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}
