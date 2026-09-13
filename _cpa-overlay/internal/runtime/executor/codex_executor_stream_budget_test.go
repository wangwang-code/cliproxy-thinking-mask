package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
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
