package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// statusErr is a minimal status-carrying error for exercising HTTPStatusFromError.
type statusErr struct {
	code int
	msg  string
}

func (e statusErr) Error() string { return e.msg }

func (e statusErr) StatusCode() int {
	if e.code == 0 {
		return http.StatusBadGateway
	}
	return e.code
}

func TestIsOverloadBootstrapError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "server_is_overloaded marker",
			err:  errors.New(`{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later.","param":null}}`),
			want: true,
		},
		{
			name: "service_unavailable_error marker",
			err:  errors.New(`some wrapper: service_unavailable_error`),
			want: true,
		},
		{
			name: "overloaded phrase",
			err:  errors.New(`upstream 502: Our servers are currently overloaded`),
			want: true,
		},
		{
			name: "502 status without overload text",
			err:  statusErr{code: http.StatusBadGateway, msg: "upstream execution failed: gateway timeout"},
			want: true,
		},
		{
			name: "503 status without overload text",
			err:  statusErr{code: http.StatusServiceUnavailable, msg: "provider unavailable"},
			want: true,
		},
		{
			name: "500 without overload text",
			err:  statusErr{code: http.StatusInternalServerError, msg: "internal error"},
			want: false,
		},
		{
			name: "404 status",
			err:  statusErr{code: http.StatusNotFound, msg: "model not found"},
			want: false,
		},
		{
			name: "unrelated bad gateway without status",
			err:  errors.New(`upstream 502: connection reset`),
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isOverloadBootstrapError(c.err); got != c.want {
				t.Fatalf("isOverloadBootstrapError() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFailoverConfigEnabled(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{Failover: config.FailoverConfig{Enabled: true}}}
	if !h.failoverConfigEnabled("openai", []byte(`{"model":"m","messages":[{"role":"user"}]}`)) {
		t.Fatal("openai chat payload should enable failover")
	}
	if h.failoverConfigEnabled("openai", []byte(`{"model":"m","input":"x"}`)) {
		t.Fatal("non-chat (responses) payload should NOT enable failover")
	}
	if h.failoverConfigEnabled("codex", []byte(`{"model":"m","messages":[]}`)) {
		t.Fatal("non-openai entry should NOT enable failover")
	}
	disabled := &BaseAPIHandler{Cfg: &config.SDKConfig{Failover: config.FailoverConfig{Enabled: false}}}
	if disabled.failoverConfigEnabled("openai", []byte(`{"model":"m","messages":[]}`)) {
		t.Fatal("disabled failover should not enable")
	}
}

func TestFailoverTerminalError(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{Failover: config.FailoverConfig{
		TerminalMessage: "云翻译服务暂不可用",
	}}}
	msg := h.failoverTerminalError()
	if msg == nil {
		t.Fatal("nil terminal error")
	}
	if msg.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", msg.StatusCode)
	}
	if !msg.DirectResponse {
		t.Fatal("terminal error should be a direct response")
	}
	body := string(msg.Body)
	for _, want := range []string{
		`"type":"service_unavailable_error"`,
		`"code":"server_is_overloaded"`,
		`"message":"云翻译服务暂不可用"`,
		`"param":null`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("terminal body missing %s: %s", want, body)
		}
	}
}

func TestIsNoAuthAvailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"auth_not_found core error", &coreauth.Error{Code: "auth_not_found", Message: "no auth available"}, true},
		{"auth_unavailable core error", &coreauth.Error{Code: "auth_unavailable", Message: "no auth available"}, true},
		{"wrapped auth_not_found", fmt.Errorf("execute: %w", &coreauth.Error{Code: "auth_not_found", Message: "selector returned no auth"}), true},
		{"plain text no auth available", errors.New("no auth available"), true},
		{"plain text no eligible auth", errors.New("selector returned no eligible auth"), true},
		{"unrelated", errors.New("upstream 500 boom"), false},
		{"overload body not auth error", errors.New(`{"error":{"code":"server_is_overloaded"}}`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isNoAuthAvailableError(c.err); got != c.want {
				t.Fatalf("isNoAuthAvailableError() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsFailoverTriggerError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"overload body", errors.New(`{"error":{"code":"server_is_overloaded"}}`), true},
		{"502 status", statusErr{code: http.StatusBadGateway, msg: "gateway"}, true},
		{"429 status", statusErr{code: http.StatusTooManyRequests, msg: "rate limited"}, true},
		{"auth_not_found", &coreauth.Error{Code: "auth_not_found", Message: "no auth available"}, true},
		{"auth_unavailable", &coreauth.Error{Code: "auth_unavailable", Message: "no auth available"}, true},
		{"404 status", statusErr{code: http.StatusNotFound, msg: "model not found"}, false},
		{"400 request fault", errors.New("invalid request"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFailoverTriggerError(c.err); got != c.want {
				t.Fatalf("isFailoverTriggerError() = %v, want %v", got, c.want)
			}
		})
	}
}
