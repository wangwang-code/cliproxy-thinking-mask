package handlers

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/tidwall/gjson"
)

// streamLimitsContextKey stores the per-request stream budget in gin.Context.
const streamLimitsContextKey = "cpaStreamLimits"

// requestLifecycleContextKey stores the request lifecycle tracker in gin.Context
// so downstream forwarding code can report terminal failures (for example a
// stream-budget abort) to plugins and monitoring.
const requestLifecycleContextKey = "cpaRequestLifecycle"

// bindRequestLifecycle stores the lifecycle tracker on the gin context carried
// by ctx. It is a no-op when no gin context is available (SDK callers, tests).
func bindRequestLifecycle(ctx context.Context, lifecycle *requestLifecycleTracker) {
	if ctx == nil || lifecycle == nil {
		return
	}
	ginCtx, ok := ctx.Value("gin").(*gin.Context)
	if !ok || ginCtx == nil {
		return
	}
	ginCtx.Set(requestLifecycleContextKey, lifecycle)
}

// upstreamResponseTooLargeJSON is the exact client-facing payload emitted when a
// stream exceeds its configured budget. It follows the OpenAI error object shape
// (message/type/param/code) and is already valid JSON, so
// BuildErrorResponseBody forwards it verbatim.
const upstreamResponseTooLargeJSON = `{"error":{"message":"模型输出失控，已中止","type":"server_error","param":null,"code":"upstream_response_too_large"}}`

// streamLimits is the resolved per-request budget applied in ForwardStream.
type streamLimits struct {
	maxBytes        int
	maxDuration     time.Duration
	maxContentChars int
}

// streamLimitError is the error value used for a stream-budget abort. It
// implements StatusCode so the usage reporter records a 502 failure instead of
// an unknown status.
type streamLimitError struct {
	payload string
}

func (e *streamLimitError) Error() string {
	return e.payload
}

func (e *streamLimitError) StatusCode() int {
	return http.StatusBadGateway
}

// newUpstreamResponseTooLargeError builds the terminal error for a runaway stream.
func newUpstreamResponseTooLargeError() *interfaces.ErrorMessage {
	return &interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      &streamLimitError{payload: upstreamResponseTooLargeJSON},
	}
}

// ApplyStreamLimits is the exported form of applyStreamLimits for sibling
// packages (for example the OpenAI chat handler).
func (h *BaseAPIHandler) ApplyStreamLimits(c *gin.Context, modelName string, rawJSON []byte) {
	h.applyStreamLimits(c, modelName, rawJSON)
}

// recordStreamLimitError mirrors WriteErrorResponse's bookkeeping for the
// post-headers SSE case: it records the error body and appends the error to the
// request-logging middleware's API_RESPONSE_ERROR list. That list drives
// error-only request logs, which CPAM's request monitoring reads.
func (h *BaseAPIHandler) recordStreamLimitError(c *gin.Context, errMsg *interfaces.ErrorMessage) {
	if c == nil || errMsg == nil {
		return
	}
	status := errMsg.StatusCode
	if status <= 0 {
		status = http.StatusBadGateway
	}
	errText := http.StatusText(status)
	if errMsg.Error != nil && errMsg.Error.Error() != "" {
		errText = errMsg.Error.Error()
	}
	appendAPIResponse(c, BuildErrorResponseBody(status, errText))
	if existing, ok := c.Get("API_RESPONSE_ERROR"); ok {
		if list, ok := existing.([]*interfaces.ErrorMessage); ok {
			merged := make([]*interfaces.ErrorMessage, 0, len(list)+1)
			merged = append(merged, list...)
			merged = append(merged, errMsg)
			c.Set("API_RESPONSE_ERROR", merged)
			return
		}
	}
	c.Set("API_RESPONSE_ERROR", []*interfaces.ErrorMessage{errMsg})
}

// applyStreamLimits resolves and stores the stream budget for one request.
// It is a no-op when stream limits are disabled or no rule matches.
func (h *BaseAPIHandler) applyStreamLimits(c *gin.Context, modelName string, rawJSON []byte) {
	if h == nil || h.Cfg == nil || c == nil || !h.Cfg.StreamLimits.Enabled {
		return
	}
	apiKey := strings.TrimSpace(c.GetString("userApiKey"))
	rule := matchStreamLimitRule(h.Cfg.StreamLimits.Rules, apiKey, modelName)
	if rule == nil {
		return
	}

	inputChars := countRequestTextChars(rawJSON)
	limits := streamLimits{
		maxBytes:        computeStreamByteBudget(rule.InputBudget, inputChars),
		maxContentChars: rule.MaxContentChars,
	}
	if d, err := time.ParseDuration(strings.TrimSpace(rule.MaxStreamDuration)); err == nil && d > 0 {
		limits.maxDuration = d
	}
	if limits.maxBytes <= 0 && limits.maxDuration <= 0 && limits.maxContentChars <= 0 {
		return
	}
	c.Set(streamLimitsContextKey, limits)
}

// streamLimitsFromContext returns the resolved budget for a request, if any.
func streamLimitsFromContext(c *gin.Context) (streamLimits, bool) {
	if c == nil {
		return streamLimits{}, false
	}
	value, ok := c.Get(streamLimitsContextKey)
	if !ok {
		return streamLimits{}, false
	}
	limits, ok := value.(streamLimits)
	return limits, ok
}

func matchStreamLimitRule(rules []config.StreamLimitRule, apiKey, modelName string) *config.StreamLimitRule {
	for i := range rules {
		rule := &rules[i]
		if !matchAnyString(rule.APIKeys, apiKey) {
			continue
		}
		if !matchModelPatterns(rule.Models, modelName) {
			continue
		}
		return rule
	}
	return nil
}

func matchAnyString(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

func matchModelPatterns(patterns []string, model string) bool {
	if len(patterns) == 0 {
		return true
	}
	model = strings.TrimSpace(model)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, err := path.Match(pattern, model); err == nil && ok {
			return true
		}
	}
	return false
}

func computeStreamByteBudget(budget config.StreamLimitInputBudget, inputChars int) int {
	if inputChars < 0 {
		inputChars = 0
	}
	value := budget.BaseBytes + inputChars*budget.BytesPerInputChar
	if budget.MinBytes > 0 && value < budget.MinBytes {
		value = budget.MinBytes
	}
	if budget.MaxBytes > 0 && value > budget.MaxBytes {
		value = budget.MaxBytes
	}
	if value < 0 {
		value = 0
	}
	return value
}

// countRequestTextChars estimates the request's text input size across the
// common OpenAI/Claude/Gemini content shapes. It is used only for configured
// stream-limit rules, so a best-effort estimate is sufficient.
func countRequestTextChars(rawJSON []byte) int {
	if len(rawJSON) == 0 {
		return 0
	}
	if messages := gjson.GetBytes(rawJSON, "messages"); messages.IsArray() {
		total := 0
		for _, message := range messages.Array() {
			total += countContentTextChars(message.Get("content"))
		}
		return total
	}
	if contents := gjson.GetBytes(rawJSON, "contents"); contents.IsArray() {
		total := 0
		for _, content := range contents.Array() {
			parts := content.Get("parts")
			if !parts.IsArray() {
				continue
			}
			for _, part := range parts.Array() {
				total += len([]rune(part.Get("text").String()))
			}
		}
		return total
	}
	return len([]rune(gjson.GetBytes(rawJSON, "input").String()))
}

func countContentTextChars(content gjson.Result) int {
	if !content.Exists() {
		return 0
	}
	if content.Type == gjson.String {
		return len([]rune(content.String()))
	}
	if !content.IsArray() {
		return 0
	}
	total := 0
	for _, part := range content.Array() {
		switch strings.ToLower(strings.TrimSpace(part.Get("type").String())) {
		case "text", "input_text", "output_text":
			total += len([]rune(part.Get("text").String()))
		}
	}
	return total
}
