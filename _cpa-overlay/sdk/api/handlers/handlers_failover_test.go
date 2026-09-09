package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

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
			name: "unrelated bad gateway",
			err:  errors.New(`upstream 502: connection reset`),
			want: false,
		},
		{
			name: "plain 503 without marker",
			err:  errors.New(`upstream returned status 503`),
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
