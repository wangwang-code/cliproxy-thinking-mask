package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsRetryableConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"io EOF", io.EOF, true},
		{"utls handshake EOF", errors.New("utls: TLS handshake: EOF"), true},
		{"tls handshake reset", errors.New("tls: handshake failure: read tcp: connection reset by peer"), true},
		{"connection refused", errors.New("dial tcp 1.2.3.4:443: connect: connection refused"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"post EOF", errors.New(`Post "https://x/responses": EOF`), true},
		{"dial timeout", errors.New("dial tcp: i/o timeout"), true},
		{"context deadline", context.DeadlineExceeded, true},
		{"http 502 status err", errors.New(`{"error":{"code":"server_is_overloaded"}}`), false},
		{"http 401 status err", errors.New(`{"error":{"type":"authentication_error"}}`), false},
		{"plain bad request", errors.New("400 invalid request"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryableConnectionError(c.err); got != c.want {
				t.Fatalf("isRetryableConnectionError(%q) = %v, want %v", c.err.Error(), got, c.want)
			}
		})
	}
}

func TestCodexRetryRoundTripperRetriesOnEOF(t *testing.T) {
	var calls int32
	rt := &codexRetryRoundTripper{
		base: stubRT(func(req *http.Request) (*http.Response, error) {
			n := atomic.AddInt32(&calls, 1)
			if n < 3 {
				return nil, errors.New(`Post "https://agentrouter.org/v1/responses": EOF`)
			}
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		retries: 3,
		delay:   0,
	}
	req, err := http.NewRequest(http.MethodPost, "https://agentrouter.org/v1/responses", strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls = %d, want 3 (2 retries)", atomic.LoadInt32(&calls))
	}
}

func TestCodexRetryRoundTripperGivesUp(t *testing.T) {
	var calls int32
	rt := &codexRetryRoundTripper{
		base: stubRT(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return nil, errors.New("dial tcp: connection refused")
		}),
		retries: 2,
		delay:   0,
	}
	req, _ := http.NewRequest(http.MethodPost, "https://agentrouter.org/v1/responses", strings.NewReader(`{}`))
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 retries)", atomic.LoadInt32(&calls))
	}
}

func TestCodexRetryRoundTripperDoesNotRetryHTTPStatus(t *testing.T) {
	var calls int32
	rt := &codexRetryRoundTripper{
		base: stubRT(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return nil, errors.New(`{"error":{"code":"server_is_overloaded"}}`)
		}),
		retries: 3,
		delay:   0,
	}
	req, _ := http.NewRequest(http.MethodPost, "https://agentrouter.org/v1/responses", strings.NewReader(`{}`))
	_, _ = rt.RoundTrip(req)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no retry for HTTP-status errors)", atomic.LoadInt32(&calls))
	}
}

type stubRT func(*http.Request) (*http.Response, error)

func (f stubRT) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
