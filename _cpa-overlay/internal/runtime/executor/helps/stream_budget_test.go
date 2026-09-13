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
	if !budget.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}
