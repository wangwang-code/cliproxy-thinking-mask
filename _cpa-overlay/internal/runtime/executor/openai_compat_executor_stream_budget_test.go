package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

// openAICompatBudgetServer serves a single non-streaming chat completion body.
func openAICompatBudgetServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func openAICompatBudgetExecute(t *testing.T, baseURL string, metadata map[string]any) (cliproxyexecutor.Response, error) {
	t.Helper()
	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": baseURL + "/v1",
		"api_key":  "test",
	}}
	return executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "nex-agi/nex-n2.5-mini:free",
		Payload: []byte(`{"model":"nex-agi/nex-n2.5-mini:free","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Stream:       false,
		Metadata:     metadata,
	})
}

// A failover endpoint (openai-compatible provider) must enforce the same
// per-request budget as the codex aggregation path, otherwise a fallback request
// can still run away unbounded.
func TestOpenAICompatExecutorExecuteEnforcesStreamBudget(t *testing.T) {
	body := `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"` +
		strings.Repeat("a", 4000) + `"},"finish_reason":"stop"}]}`
	_, err := openAICompatBudgetExecute(t, openAICompatBudgetServer(t, body).URL, map[string]any{
		cliproxyexecutor.StreamLimitMaxBytesMetadataKey: 256,
	})
	var budgetErr *interfaces.StreamBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("Execute error = %v, want *interfaces.StreamBudgetError", err)
	}
	if !budgetErr.StreamBudgetExceeded() {
		t.Fatal("the byte-cap abort must carry the stream budget marker")
	}
}

func TestOpenAICompatExecutorExecuteWithoutBudgetReadsWholeBody(t *testing.T) {
	body := `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	resp, err := openAICompatBudgetExecute(t, openAICompatBudgetServer(t, body).URL, nil)
	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "ok" {
		t.Fatalf("choices.0.message.content = %q, want ok; payload=%s", got, resp.Payload)
	}
}
