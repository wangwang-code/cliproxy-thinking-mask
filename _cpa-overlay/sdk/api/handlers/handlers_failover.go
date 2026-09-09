package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/sjson"
	"golang.org/x/net/context"
)

// openAIEntryProtocol is the entry protocol label used by the OpenAI-compatible
// endpoints (/v1/chat/completions). Failover is intentionally restricted to it.
const openAIEntryProtocol = "openai"

// isOverloadBootstrapError reports whether an upstream execution error looks like
// a transient capacity rejection (OpenAI "server_is_overloaded" / HTTP 502-503)
// that a different credential/provider may be able to serve. HTTP 502/503 from
// the codex bootstrap path are already classified as overload-class failures by
// CPA, so any 502/503 is treated as eligible without requiring the text to name
// the overload condition. HTTP 500 only counts when the text names overload.
func isOverloadBootstrapError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "server_is_overloaded") ||
		strings.Contains(text, "service_unavailable_error") ||
		strings.Contains(text, "servers are currently overloaded") {
		return true
	}
	switch statusFromError(err) {
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	case http.StatusInternalServerError:
		return text == "" || strings.Contains(text, "overload")
	default:
		return false
	}
}

// failoverConfigEnabled reports whether chat.completions failover is switched on
// for the given entry protocol and request payload.
func (h *BaseAPIHandler) failoverConfigEnabled(entryProtocol string, payload []byte) bool {
	if h == nil || h.Cfg == nil || !h.Cfg.Failover.Enabled {
		return false
	}
	if entryProtocol != openAIEntryProtocol {
		return false
	}
	// Only OpenAI chat/completions style payloads (they carry a messages array).
	if len(payload) == 0 || !bytes.Contains(payload, []byte(`"messages"`)) {
		return false
	}
	return true
}

// failoverMatchesPrimary reports whether the providers that just failed include a
// configured primary provider. An empty primary-providers list matches any.
func (h *BaseAPIHandler) failoverMatchesPrimary(providers []string) bool {
	if h == nil || h.Cfg == nil {
		return false
	}
	wants := h.Cfg.Failover.PrimaryProviders
	if len(wants) == 0 {
		return true
	}
	for _, provider := range providers {
		for _, want := range wants {
			if strings.EqualFold(strings.TrimSpace(provider), strings.TrimSpace(want)) {
				return true
			}
		}
	}
	return false
}

// failoverEndpoints returns the configured endpoints ordered by ascending priority
// (stable for equal priorities).
func (h *BaseAPIHandler) failoverEndpoints() []config.FailoverEndpoint {
	if h == nil || h.Cfg == nil {
		return nil
	}
	out := append([]config.FailoverEndpoint(nil), h.Cfg.Failover.Endpoints...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Priority < out[j].Priority
	})
	return out
}

// failoverAttempt rewrites the request for one fallback endpoint: it switches the
// provider list to that endpoint's internal key and, when a model override is
// configured, replaces the model in both req.Model and the payload.
func (h *BaseAPIHandler) failoverAttempt(endpoint config.FailoverEndpoint, providers []string, req coreexecutor.Request) ([]string, coreexecutor.Request) {
	attemptProviders := []string{util.OpenAICompatibleProviderKey(endpoint.Name)}
	attemptReq := req
	override := strings.TrimSpace(endpoint.Model)
	if override != "" && override != req.Model {
		attemptReq.Model = override
		if len(attemptReq.Payload) > 0 {
			if rewritten, errSet := sjson.SetBytes(attemptReq.Payload, "model", override); errSet == nil {
				attemptReq.Payload = rewritten
			}
		}
	}
	return attemptProviders, attemptReq
}

// failoverTerminalError builds the synthetic overload error returned when every
// configured fallback endpoint also fails.
func (h *BaseAPIHandler) failoverTerminalError() *interfaces.ErrorMessage {
	cfg := config.FailoverConfig{}
	if h != nil && h.Cfg != nil {
		cfg = h.Cfg.Failover
	}
	status := cfg.TerminalStatus
	if status <= 0 {
		status = config.DefaultFailoverTerminalStatus()
	}
	errorType := strings.TrimSpace(cfg.TerminalType)
	if errorType == "" {
		errorType = config.DefaultFailoverTerminalType()
	}
	errorCode := strings.TrimSpace(cfg.TerminalCode)
	if errorCode == "" {
		errorCode = config.DefaultFailoverTerminalCode()
	}
	message := strings.TrimSpace(cfg.TerminalMessage)
	if message == "" {
		message = config.DefaultFailoverTerminalMessage()
	}
	body := []byte(fmt.Sprintf(
		`{"error":{"type":%q,"code":%q,"message":%q,"param":null}}`,
		errorType, errorCode, message,
	))
	return &interfaces.ErrorMessage{
		StatusCode:     status,
		DirectResponse: true,
		Error:          fmt.Errorf("%s", string(body)),
		Body:           body,
	}
}

// tryNonStreamFailover runs the non-streaming fallback chain. Return semantics:
//   - applied=false: failover not applicable; caller keeps the original error.
//   - applied=true, ok=true: a fallback endpoint succeeded; resp is usable.
//   - applied=true, ok=false: every endpoint failed; caller must emit the
//     synthetic terminal error.
//
// failoverDiagnosticState resolves which preconditions for failover are met, for logging.
func (h *BaseAPIHandler) failoverDiagnosticState(entryProtocol string, payload []byte) (enabled, entryOK, hasMessages bool) {
	enabled = h != nil && h.Cfg != nil && h.Cfg.Failover.Enabled
	entryOK = entryProtocol == openAIEntryProtocol
	hasMessages = len(payload) > 0 && bytes.Contains(payload, []byte(`"messages"`))
	return
}

func (h *BaseAPIHandler) tryNonStreamFailover(ctx context.Context, entryProtocol string, providers []string, req coreexecutor.Request, opts coreexecutor.Options, payload []byte, cause error) (resp coreexecutor.Response, applied, ok bool) {
	if h == nil || h.AuthManager == nil {
		log.Warnf("failover: skipped (nil handler/auth) protocol=%s providers=%v", entryProtocol, providers)
		return resp, false, false
	}
	enabled, entryOK, hasMessages := h.failoverDiagnosticState(entryProtocol, payload)
	if !enabled || !entryOK || !hasMessages {
		log.Warnf("failover: skipped (preconditions) enabled=%v entryOK=%v hasMessages=%v protocol=%q err=%v", enabled, entryOK, hasMessages, entryProtocol, cause)
		return resp, false, false
	}
	if !isOverloadBootstrapError(cause) {
		log.Warnf("failover: skipped (not overload) protocol=%s providers=%v status=%d err=%v", entryProtocol, providers, statusFromError(cause), cause)
		return resp, false, false
	}
	if !h.failoverMatchesPrimary(providers) {
		log.Warnf("failover: skipped (primary mismatch) providers=%v configured=%v", providers, h.Cfg.Failover.PrimaryProviders)
		return resp, false, false
	}
	for _, endpoint := range h.failoverEndpoints() {
		if strings.TrimSpace(endpoint.Name) == "" {
			continue
		}
		attemptProviders, attemptReq := h.failoverAttempt(endpoint, providers, req)
		log.Warnf("failover: trying endpoint=%q providers=%v model=%q", endpoint.Name, attemptProviders, attemptReq.Model)
		attemptResp, attemptErr := h.AuthManager.Execute(ctx, attemptProviders, attemptReq, opts)
		if attemptErr == nil {
			return attemptResp, true, true
		}
		log.Warnf("failover: endpoint=%q failed err=%v", endpoint.Name, attemptErr)
	}
	return resp, true, false
}

// tryStreamFailover runs the streaming fallback chain (same semantics as
// tryNonStreamFailover, returning a stream result instead of a response).
func (h *BaseAPIHandler) tryStreamFailover(ctx context.Context, entryProtocol string, providers []string, req coreexecutor.Request, opts coreexecutor.Options, payload []byte, cause error) (result *coreexecutor.StreamResult, applied, ok bool) {
	if h == nil || h.AuthManager == nil {
		log.Warnf("failover: skipped (nil handler/auth) protocol=%s providers=%v", entryProtocol, providers)
		return nil, false, false
	}
	enabled, entryOK, hasMessages := h.failoverDiagnosticState(entryProtocol, payload)
	if !enabled || !entryOK || !hasMessages {
		log.Warnf("failover: skipped (preconditions) enabled=%v entryOK=%v hasMessages=%v protocol=%q err=%v", enabled, entryOK, hasMessages, entryProtocol, cause)
		return nil, false, false
	}
	if !isOverloadBootstrapError(cause) {
		log.Warnf("failover: skipped (not overload) protocol=%s providers=%v status=%d err=%v", entryProtocol, providers, statusFromError(cause), cause)
		return nil, false, false
	}
	if !h.failoverMatchesPrimary(providers) {
		log.Warnf("failover: skipped (primary mismatch) providers=%v configured=%v", providers, h.Cfg.Failover.PrimaryProviders)
		return nil, false, false
	}
	for _, endpoint := range h.failoverEndpoints() {
		if strings.TrimSpace(endpoint.Name) == "" {
			continue
		}
		attemptProviders, attemptReq := h.failoverAttempt(endpoint, providers, req)
		log.Warnf("failover: trying endpoint=%q providers=%v model=%q", endpoint.Name, attemptProviders, attemptReq.Model)
		attemptResult, attemptErr := h.AuthManager.ExecuteStream(ctx, attemptProviders, attemptReq, opts)
		if attemptErr == nil && attemptResult != nil && attemptResult.Chunks != nil {
			return attemptResult, true, true
		}
		log.Warnf("failover: endpoint=%q failed err=%v", endpoint.Name, attemptErr)
	}
	return nil, true, false
}
