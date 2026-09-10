package handlers

import (
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

// rewriteExecutionErrorMessage applies the configured upstream-error rewrite to
// an ErrorMessage produced by executionErrorMessage. It returns the original msg
// when rewriting is disabled, no message matches the status, or msg is nil.
func (h *BaseAPIHandler) rewriteExecutionErrorMessage(msg *interfaces.ErrorMessage) *interfaces.ErrorMessage {
	if h == nil || h.Cfg == nil || msg == nil {
		return msg
	}
	cfg := h.Cfg.ErrorRewrite
	if !cfg.Enabled {
		return msg
	}
	status := msg.StatusCode
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	message := cfg.MessageForStatus(status)
	if message == "" {
		return msg
	}
	// Replace the full error with only our configured message. This intentionally
	// drops upstream body, headers, addon headers, and the direct-response flag so
	// the upstream identity is never forwarded to the client.
	return &interfaces.ErrorMessage{
		StatusCode: status,
		Error:      fmt.Errorf("%s", message),
	}
}

// RewriteExecutionErrorMessage is the exported form of rewriteExecutionErrorMessage.
// It lets sibling packages (for example the responses WebSocket writer) apply the
// same configured upstream-error rewrite to an ErrorMessage.
func (h *BaseAPIHandler) RewriteExecutionErrorMessage(msg *interfaces.ErrorMessage) *interfaces.ErrorMessage {
	return h.rewriteExecutionErrorMessage(msg)
}

// executionErrorMessageForHandler is the config-aware equivalent of
// executionErrorMessage. It converts err to an ErrorMessage and then applies the
// configured upstream-error rewrite.
func (h *BaseAPIHandler) executionErrorMessageForHandler(err error) *interfaces.ErrorMessage {
	return h.rewriteExecutionErrorMessage(executionErrorMessage(err))
}
