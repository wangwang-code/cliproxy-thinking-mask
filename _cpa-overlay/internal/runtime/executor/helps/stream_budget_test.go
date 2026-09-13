package helps

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestReadAllWithStreamBudgetPassthrough(t *testing.T) {
	body := io.NopCloser(strings.NewReader("hello"))
	data, err := ReadAllWithStreamBudget(body, StreamBudget{})
	if err != nil {
		t.Fatalf("ReadAllWithStreamBudget() error = %v, want nil", err)
	}
	if string(data) != "hello" {
		t.Fatalf("ReadAllWithStreamBudget() = %q, want hello", string(data))
	}
}

func TestReadAllWithStreamBudgetByteCap(t *testing.T) {
	body := io.NopCloser(strings.NewReader(strings.Repeat("a", 100)))
	_, err := ReadAllWithStreamBudget(body, StreamBudget{MaxBytes: 10})
	var budgetErr *interfaces.StreamBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("ReadAllWithStreamBudget() error = %v, want *interfaces.StreamBudgetError", err)
	}
}

func TestReadAllWithStreamBudgetDurationCap(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() {
		if errClose := writer.Close(); errClose != nil {
			t.Errorf("close pipe writer: %v", errClose)
		}
	}()

	start := time.Now()
	_, err := ReadAllWithStreamBudget(reader, StreamBudget{MaxDuration: 20 * time.Millisecond})
	var budgetErr *interfaces.StreamBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("ReadAllWithStreamBudget() error = %v, want *interfaces.StreamBudgetError", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("duration cap took %v, want well under 1s", elapsed)
	}
}

func TestStreamBudgetFromOptions(t *testing.T) {
	budget := StreamBudgetFromOptions(cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.StreamLimitMaxBytesMetadataKey:        int64(4096),
		cliproxyexecutor.StreamLimitMaxDurationMetadataKey:     int64(1500),
		cliproxyexecutor.StreamLimitMaxContentCharsMetadataKey: 128,
	}})
	if budget.MaxBytes != 4096 {
		t.Fatalf("MaxBytes = %d, want 4096", budget.MaxBytes)
	}
	if budget.MaxDuration != 1500*time.Millisecond {
		t.Fatalf("MaxDuration = %v, want 1.5s", budget.MaxDuration)
	}
	if budget.MaxContentChars != 128 {
		t.Fatalf("MaxContentChars = %d, want 128", budget.MaxContentChars)
	}
	if !budget.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}

func TestCountResponseContentChars(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    int
	}{
		{
			name:    "chat message content counts runes",
			payload: `{"choices":[{"message":{"content":"héllo"}}]}`,
			want:    5,
		},
		{
			name:    "chat reasoning is not answer content",
			payload: `{"choices":[{"message":{"content":"ok","reasoning_content":"thinking very hard"}}]}`,
			want:    2,
		},
		{
			name:    "legacy completions text",
			payload: `{"choices":[{"text":"abc"}]}`,
			want:    3,
		},
		{
			name:    "responses output_text parts",
			payload: `{"output":[{"type":"message","content":[{"type":"output_text","text":"hello"},{"type":"reasoning_text","text":"ignored"}]}]}`,
			want:    5,
		},
		{
			name:    "codex terminal event envelope",
			payload: `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"hello world"}]}]}}`,
			want:    11,
		},
		{
			name:    "claude text parts",
			payload: `{"content":[{"type":"text","text":"abcd"},{"type":"tool_use","name":"lookup"}]}`,
			want:    4,
		},
		{
			name:    "gemini candidate parts",
			payload: `{"candidates":[{"content":{"parts":[{"text":"hey"},{"text":"!"}]}}]}`,
			want:    4,
		},
		{
			name:    "no content",
			payload: `{"id":"resp_1","output":[]}`,
			want:    0,
		},
		{
			name:    "empty payload",
			payload: ``,
			want:    0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountResponseContentChars([]byte(c.payload)); got != c.want {
				t.Fatalf("CountResponseContentChars(%s) = %d, want %d", c.payload, got, c.want)
			}
		})
	}
}

func TestContentCharsExceeded(t *testing.T) {
	payload := []byte(`{"choices":[{"message":{"content":"12345"}}]}`)
	if ContentCharsExceeded(payload, 0) {
		t.Fatal("a disabled cap (0) must never report an overflow")
	}
	if ContentCharsExceeded(payload, 5) {
		t.Fatal("content equal to the cap must not report an overflow")
	}
	if !ContentCharsExceeded(payload, 4) {
		t.Fatal("content above the cap must report an overflow")
	}
	if ContentCharsExceeded(nil, 1) {
		t.Fatal("an empty payload must never report an overflow")
	}
}
