package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

func TestComputeStreamByteBudget(t *testing.T) {
	budget := config.StreamLimitInputBudget{
		BaseBytes:         65536,
		BytesPerInputChar: 32,
		MinBytes:          131072,
		MaxBytes:          1048576,
	}
	if got := computeStreamByteBudget(budget, 100); got != 131072 {
		t.Fatalf("small input budget = %d, want min 131072", got)
	}
	if got := computeStreamByteBudget(budget, 1000); got != 131072 {
		t.Fatalf("1000-char budget = %d, want min clamp 131072", got)
	}
	if got := computeStreamByteBudget(budget, 100000); got != 1048576 {
		t.Fatalf("large input budget = %d, want max 1048576", got)
	}
}

func TestMatchStreamLimitRule(t *testing.T) {
	rules := []config.StreamLimitRule{
		{Name: "other", APIKeys: []string{"other-key"}, Models: []string{"gpt-*"}},
		{Name: "translation", APIKeys: []string{"translation-key"}, Models: []string{"gpt-5.6-*"}},
	}
	if got := matchStreamLimitRule(rules, "translation-key", "gpt-5.6-luna"); got == nil || got.Name != "translation" {
		t.Fatalf("matchStreamLimitRule() = %#v, want translation", got)
	}
	if got := matchStreamLimitRule(rules, "translation-key", "other-model"); got != nil {
		t.Fatalf("matchStreamLimitRule() = %#v, want nil", got)
	}
	if got := matchStreamLimitRule(rules, "unknown", "gpt-5.6-luna"); got != nil {
		t.Fatalf("matchStreamLimitRule() = %#v, want nil", got)
	}
}

func TestCountRequestTextChars(t *testing.T) {
	stringContent := []byte(`{"messages":[{"role":"system","content":"hello"},{"role":"user","content":"世界"}]}`)
	if got := countRequestTextChars(stringContent); got != 7 {
		t.Fatalf("string content chars = %d, want 7", got)
	}
	arrayContent := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"x"}}]}]}`)
	if got := countRequestTextChars(arrayContent); got != 5 {
		t.Fatalf("array content chars = %d, want 5", got)
	}
	geminiContent := []byte(`{"contents":[{"parts":[{"text":"hello"},{"text":"世界"}]}]}`)
	if got := countRequestTextChars(geminiContent); got != 7 {
		t.Fatalf("gemini content chars = %d, want 7", got)
	}
}

func TestApplyStreamLimitsStoresDynamicBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("userApiKey", "translation-key")

	h := &BaseAPIHandler{Cfg: &config.SDKConfig{
		StreamLimits: config.StreamLimitsConfig{
			Enabled: true,
			Rules: []config.StreamLimitRule{{
				Name:    "translation",
				APIKeys: []string{"translation-key"},
				Models:  []string{"gpt-5.6-*"},
				InputBudget: config.StreamLimitInputBudget{
					BaseBytes:         1000,
					BytesPerInputChar: 10,
					MinBytes:          2000,
					MaxBytes:          5000,
				},
				MaxStreamDuration: "5s",
				MaxContentChars:   123,
			}},
		},
	}}
	h.ApplyStreamLimits(c, "gpt-5.6-luna", []byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	limits, ok := streamLimitsFromContext(c)
	if !ok {
		t.Fatal("streamLimitsFromContext() = false, want true")
	}
	if limits.maxBytes != 2000 {
		t.Fatalf("maxBytes = %d, want 2000", limits.maxBytes)
	}
	if limits.maxDuration != 5*time.Second {
		t.Fatalf("maxDuration = %v, want 5s", limits.maxDuration)
	}
	if limits.maxContentChars != 123 {
		t.Fatalf("maxContentChars = %d, want 123", limits.maxContentChars)
	}
}

func TestForwardStreamMaxStreamBytesEmitsExactError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	data := make(chan []byte, 2)
	data <- []byte("0123456789")
	data <- []byte("0123456789")
	errs := make(chan *interfaces.ErrorMessage)

	maxBytes := 15
	disabledKeepAlive := time.Duration(0)
	var written, canceled string
	h := &BaseAPIHandler{}
	h.ForwardStream(c, recorder, func(err error) {
		if err != nil {
			canceled = err.Error()
		}
	}, data, errs, StreamForwardOptions{
		KeepAliveInterval:  &disabledKeepAlive,
		MaxStreamBytes:     &maxBytes,
		WriteChunk:         func([]byte) {},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) { written = errMsg.Error.Error() },
	})

	if written != upstreamResponseTooLargeJSON {
		t.Fatalf("written = %q, want %q", written, upstreamResponseTooLargeJSON)
	}
	if canceled != upstreamResponseTooLargeJSON {
		t.Fatalf("canceled = %q, want %q", canceled, upstreamResponseTooLargeJSON)
	}
}

func TestForwardStreamMaxContentCharsEmitsExactError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	data := make(chan []byte, 2)
	data <- []byte(`{"choices":[{"delta":{"content":"abcd"}}]}`)
	data <- []byte(`{"choices":[{"delta":{"content":"efgh"}}]}`)
	errs := make(chan *interfaces.ErrorMessage)

	maxContentChars := 5
	disabledKeepAlive := time.Duration(0)
	var written string
	h := &BaseAPIHandler{}
	h.ForwardStream(c, recorder, func(error) {}, data, errs, StreamForwardOptions{
		KeepAliveInterval:  &disabledKeepAlive,
		MaxContentChars:    &maxContentChars,
		WriteChunk:         func([]byte) {},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) { written = errMsg.Error.Error() },
	})

	if written != upstreamResponseTooLargeJSON {
		t.Fatalf("written = %q, want %q", written, upstreamResponseTooLargeJSON)
	}
}

func TestForwardStreamMaxStreamDurationEmitsExactError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage)
	maxDuration := 20 * time.Millisecond
	disabledKeepAlive := time.Duration(0)
	var written string
	h := &BaseAPIHandler{}
	h.ForwardStream(c, recorder, func(error) {}, data, errs, StreamForwardOptions{
		KeepAliveInterval:  &disabledKeepAlive,
		MaxStreamDuration:  &maxDuration,
		WriteChunk:         func([]byte) {},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) { written = errMsg.Error.Error() },
	})

	if written != upstreamResponseTooLargeJSON {
		t.Fatalf("written = %q, want %q", written, upstreamResponseTooLargeJSON)
	}
}

func TestUpstreamResponseTooLargeJSONIsValid(t *testing.T) {
	if !strings.Contains(upstreamResponseTooLargeJSON, `"code":"upstream_response_too_large"`) {
		t.Fatalf("unexpected payload: %s", upstreamResponseTooLargeJSON)
	}
	if !strings.Contains(upstreamResponseTooLargeJSON, `"message":"模型输出失控，已中止"`) {
		t.Fatalf("unexpected payload: %s", upstreamResponseTooLargeJSON)
	}
}
