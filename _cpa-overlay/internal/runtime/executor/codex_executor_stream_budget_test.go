package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const codexBudgetProgressEvent = `{"type":"response.in_progress","response":{"id":"resp_1"}}`

func codexBudgetServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 200; i++ {
			if _, errWrite := w.Write([]byte("data: " + codexBudgetProgressEvent + "\n\n")); errWrite != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func codexBudgetExecute(t *testing.T, metadata map[string]any) error {
	t.Helper()
	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": codexBudgetServer(t).URL,
		"api_key":  "test",
	}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.6-terra",
		Payload: []byte(`{"model":"gpt-5.6-terra","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Stream:       false,
		Metadata:     metadata,
	})
	return err
}

func TestCodexExecutorExecuteEnforcesStreamBudget(t *testing.T) {
	err := codexBudgetExecute(t, map[string]any{
		cliproxyexecutor.StreamLimitMaxBytesMetadataKey: 256,
	})
	var budgetErr *interfaces.StreamBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("Execute error = %v, want *interfaces.StreamBudgetError", err)
	}
	if !budgetErr.IsRequestScoped() {
		t.Fatal("stream budget error must be request scoped")
	}
	if got := budgetErr.StatusCode(); got != http.StatusBadGateway {
		t.Fatalf("StatusCode() = %d, want %d", got, http.StatusBadGateway)
	}
}

func TestCodexExecutorExecuteWithoutBudgetStaysIncomplete(t *testing.T) {
	err := codexBudgetExecute(t, nil)
	if err == nil {
		t.Fatal("Execute error = nil, want an incomplete-stream error")
	}
	var budgetErr *interfaces.StreamBudgetError
	if errors.As(err, &budgetErr) {
		t.Fatalf("Execute error = %v, want a non-budget error", err)
	}
}

// codexBudgetAnswerServer returns a completed codex response whose only answer
// text is answer.
func codexBudgetAnswerServer(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	item := fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q}]}}`, answer)
	completed := `{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1775555723,"status":"completed","model":"gpt-5.6-terra","output":[],"usage":{"input_tokens":8,"output_tokens":28,"total_tokens":36}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + item + "\n\n"))
		_, _ = w.Write([]byte("data: " + completed + "\n\n"))
	}))
	t.Cleanup(server.Close)
	return server
}

func codexExecuteAgainstServer(t *testing.T, baseURL string, metadata map[string]any) (cliproxyexecutor.Response, error) {
	t.Helper()
	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": baseURL,
		"api_key":  "test",
	}}
	return executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.6-terra",
		Payload: []byte(`{"model":"gpt-5.6-terra","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Stream:       false,
		Metadata:     metadata,
	})
}

func TestCodexExecutorExecuteEnforcesContentCharBudget(t *testing.T) {
	resp, err := codexExecuteAgainstServer(t, codexBudgetAnswerServer(t, strings.Repeat("a", 64)).URL, map[string]any{
		cliproxyexecutor.StreamLimitMaxContentCharsMetadataKey: 16,
	})
	var budgetErr *interfaces.StreamBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("Execute error = %v, want *interfaces.StreamBudgetError; payload=%s", err, resp.Payload)
	}
	if !budgetErr.StreamBudgetExceeded() {
		t.Fatal("content-character abort must carry the stream budget marker")
	}
}

func TestCodexExecutorExecuteContentCharBudgetAllowsShorterAnswer(t *testing.T) {
	resp, err := codexExecuteAgainstServer(t, codexBudgetAnswerServer(t, "ok").URL, map[string]any{
		cliproxyexecutor.StreamLimitMaxContentCharsMetadataKey: 16,
	})
	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if got := gjson.GetBytes(resp.Payload, "choices.0.message.content").String(); got != "ok" {
		t.Fatalf("choices.0.message.content = %q, want ok; payload=%s", got, resp.Payload)
	}
}
