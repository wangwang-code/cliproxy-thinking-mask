package helps

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// Codex connection-layer retry configuration. Preferred source is the
// `codex.connection-retry` / `codex.connection-retry-interval` config fields.
// When those are unset (zero) the legacy environment variables are honored,
// and when those are also unset the historical default of 2 retries applies:
//
//   - CODEX_CONN_RETRIES: number of extra attempts after the first for
//     connection-layer failures (EOF / connection reset / dial / TLS
//     handshake / timeout).
//   - CODEX_CONN_RETRY_DELAY_MS: delay between attempts.
//
// Only connection-layer failures are retried. Errors that indicate the request
// reached the upstream and produced an HTTP status (4xx/5xx) are not retried
// here — the conductor/credential rotation layers own those.
func codexConnRetrySettings(cfg *config.Config) (retries int, delay time.Duration) {
	// config takes precedence when explicitly set (non-zero)
	if cfg != nil && cfg.Codex.ConnectionRetry != 0 {
		retries = cfg.Codex.ConnectionRetry
		if retries < 0 {
			retries = 0
		}
		delay = codexConnectionRetryInterval(cfg.Codex.ConnectionRetryInterval)
		return retries, delay
	}
	// fallback: env, then historical default of 2
	retries = 2
	delay = 500 * time.Millisecond
	if raw := strings.TrimSpace(os.Getenv("CODEX_CONN_RETRIES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			retries = n
		}
	}
	if raw := strings.TrimSpace(os.Getenv("CODEX_CONN_RETRY_DELAY_MS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			delay = time.Duration(n) * time.Millisecond
		}
	}
	return retries, delay
}

// codexConnectionRetryInterval parses a duration string and falls back to a
// default when empty or invalid.
func codexConnectionRetryInterval(raw string) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil && d > 0 {
		return d
	}
	return 300 * time.Millisecond
}

// isRetryableConnectionError reports whether a RoundTrip error is a
// connection-layer failure that a fresh attempt may fix. These happen before or
// during transport establishment (dial / TLS handshake / proxy connect) or as a
// truncated body read (EOF / reset) where no HTTP status was produced.
func isRetryableConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// A response that carried an HTTP status is not a connection-layer failure.
	var urlErr *net.OpError
	if errors.As(err, &urlErr) {
		if urlErr.Op == "dial" || urlErr.Op == "connect" {
			return true
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"tls handshake",
		"utls: tls handshake",
		"connection reset",
		"connection refused",
		"connection closed",
		"broken pipe",
		"eof",
		"read: connection reset",
		"dial tcp",
		"dial tcp4",
		"dial tcp6",
		"no such host",
		"i/o timeout",
		"context deadline exceeded",
		"server closed idle connection",
		"unexpected eof",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr != nil && netErr.Timeout() {
		return true
	}
	return false
}

// codexRetryRoundTripper wraps a RoundTripper and retries idempotent-safe
// connection-layer failures (EOF / reset / dial / TLS handshake) a bounded
// number of times. Request bodies are replayed via req.GetBody when available,
// which http.NewRequest sets for bytes.Reader bodies (the codex path always
// builds its payload from an in-memory byte slice).
type codexRetryRoundTripper struct {
	base      http.RoundTripper
	retries   int
	delay     time.Duration
	userAgent string
}

func (t *codexRetryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, errors.New("codex retry transport: nil base")
	}
	var lastErr error
	for attempt := 0; attempt <= t.retries; attempt++ {
		if attempt > 0 {
			if t.delay > 0 {
				timer := time.NewTimer(t.delay)
				select {
				case <-req.Context().Done():
					timer.Stop()
					return nil, req.Context().Err()
				case <-timer.C:
				}
			}
			// Replay the body for the retry.
			if req.Body != nil && req.GetBody != nil {
				body, errGet := req.GetBody()
				if errGet != nil {
					return nil, errGet
				}
				req.Body = body
			}
		}
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryableConnectionError(err) {
			break
		}
		if attempt < t.retries {
			log.Debugf("codex transport: connection error (attempt %d/%d), retrying in %s: %v", attempt+1, t.retries+1, t.delay, err)
		}
	}
	return nil, lastErr
}

// NewCodexHTTPClient returns an HTTP client for codex executor requests that
// retries connection-layer failures in-place before surfacing them to the
// conductor. It wraps the standard provider-specific uTLS/fallback client used
// by NewUtlsHTTPClient, so chatgpt.com keeps its Chrome uTLS fingerprint and
// other codex upstreams (e.g. agentrouter) keep their standard/proxy transport.
//
// Retry count and delay come from the codex.connection-retry /
// codex.connection-retry-interval config fields, with the legacy
// CODEX_CONN_RETRIES / CODEX_CONN_RETRY_DELAY_MS env vars as fallback.
func NewCodexHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	base := NewUtlsHTTPClient(ctx, cfg, auth, timeout)
	retries, delay := codexConnRetrySettings(cfg)
	if retries <= 0 {
		return base
	}
	return &http.Client{
		Transport: &codexRetryRoundTripper{
			base:    base.Transport,
			retries: retries,
			delay:   delay,
		},
		Timeout: base.Timeout,
	}
}
