package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

func TestRewriteExecutionErrorMessageAppliesStatusMessage(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		ErrorRewrite: config.ErrorRewriteConfig{
			Enabled: true,
			StatusMessages: map[string]string{
				"429": "[云翻译]被上游限流，请稍微再试",
			},
		},
	}}

	orig := &interfaces.ErrorMessage{
		StatusCode:     http.StatusTooManyRequests,
		Error:          errors.New(`upstream 429: rate limit via openai`),
		DirectResponse: true,
		Body:           []byte(`{"error":{"message":"raw upstream body","type":"rate_limit_error"}}`),
		Headers:        http.Header{"X-Upstream": {"leak"}},
		Addon:          http.Header{"X-Upstream-Addon": {"leak"}},
	}

	got := h.rewriteExecutionErrorMessage(orig)
	if got == orig {
		t.Fatal("rewriteExecutionErrorMessage() returned the original message")
	}
	if got.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want %d", got.StatusCode, http.StatusTooManyRequests)
	}
	if got.Error == nil || got.Error.Error() != "[云翻译]被上游限流，请稍微再试" {
		t.Fatalf("Error = %v, want custom message", got.Error)
	}
	if got.DirectResponse {
		t.Fatal("DirectResponse should be cleared")
	}
	if got.Body != nil {
		t.Fatalf("Body = %q, want nil", got.Body)
	}
	if got.Headers != nil {
		t.Fatalf("Headers = %#v, want nil", got.Headers)
	}
	if got.Addon != nil {
		t.Fatalf("Addon = %#v, want nil", got.Addon)
	}
}

func TestRewriteExecutionErrorMessageUsesDefaultMessage(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		ErrorRewrite: config.ErrorRewriteConfig{
			Enabled:        true,
			DefaultMessage: "[云翻译]上游服务暂时不可用，请稍后重试",
			StatusMessages: map[string]string{"429": "[云翻译]被上游限流，请稍微再试"},
		},
	}}
	orig := &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("upstream 502 boom")}
	got := h.rewriteExecutionErrorMessage(orig)
	if got == orig {
		t.Fatal("rewriteExecutionErrorMessage() returned the original message")
	}
	if got.Error == nil || got.Error.Error() != "[云翻译]上游服务暂时不可用，请稍后重试" {
		t.Fatalf("Error = %v, want default message", got.Error)
	}
}

func TestRewriteExecutionErrorMessageDisabledReturnsOriginal(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		ErrorRewrite: config.ErrorRewriteConfig{
			Enabled:        false,
			StatusMessages: map[string]string{"429": "[云翻译]被上游限流，请稍微再试"},
		},
	}}
	orig := &interfaces.ErrorMessage{StatusCode: http.StatusTooManyRequests, Error: errors.New("raw upstream")}
	if got := h.rewriteExecutionErrorMessage(orig); got != orig {
		t.Fatal("rewriteExecutionErrorMessage() should return original when disabled")
	}
}

func TestRewriteExecutionErrorMessageNonMatchingStatusReturnsOriginal(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		ErrorRewrite: config.ErrorRewriteConfig{
			Enabled:        true,
			StatusMessages: map[string]string{"429": "[云翻译]被上游限流，请稍微再试"},
		},
	}}
	orig := &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("raw upstream 502")}
	if got := h.rewriteExecutionErrorMessage(orig); got != orig {
		t.Fatal("rewriteExecutionErrorMessage() should return original for non-matching status")
	}
}

func TestExecutionErrorMessageForHandlerRewrites(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		ErrorRewrite: config.ErrorRewriteConfig{
			Enabled: true,
			StatusMessages: map[string]string{
				"429": "[云翻译]被上游限流，请稍微再试",
			},
		},
	}}
	err := &statusCodeError{code: http.StatusTooManyRequests, msg: "upstream 429 raw"}
	got := h.executionErrorMessageForHandler(err)
	if got.Error == nil || got.Error.Error() != "[云翻译]被上游限流，请稍微再试" {
		t.Fatalf("Error = %v, want custom message", got.Error)
	}
	if got.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want %d", got.StatusCode, http.StatusTooManyRequests)
	}
}

type statusCodeError struct {
	code int
	msg  string
}

func (e *statusCodeError) Error() string {
	if e == nil {
		return ""
	}
	return e.msg
}

func (e *statusCodeError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.code
}
