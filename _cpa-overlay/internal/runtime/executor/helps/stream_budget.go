package helps

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// StreamBudget is the resolved upstream read budget for one non-streaming
// request. A zero value disables every cap.
type StreamBudget struct {
	MaxBytes    int
	MaxDuration time.Duration
}

// Enabled reports whether any budget cap is active.
func (b StreamBudget) Enabled() bool {
	return b.MaxBytes > 0 || b.MaxDuration > 0
}

// StreamBudgetFromOptions extracts the budget resolved by the API handler from
// execution metadata. It returns a zero budget when no rule matched.
func StreamBudgetFromOptions(opts cliproxyexecutor.Options) StreamBudget {
	budget := StreamBudget{}
	if opts.Metadata == nil {
		return budget
	}
	budget.MaxBytes = metadataInt(opts.Metadata, cliproxyexecutor.StreamLimitMaxBytesMetadataKey)
	budget.MaxDuration = time.Duration(metadataInt64(opts.Metadata, cliproxyexecutor.StreamLimitMaxDurationMetadataKey)) * time.Millisecond
	return budget
}

// ReadAllWithStreamBudget reads body until EOF, aborting with a terminal
// *interfaces.StreamBudgetError when the byte budget is exceeded or the
// wall-clock budget elapses. The wall-clock guard closes body to unblock a
// stalled Read; callers remain responsible for their own deferred Close.
func ReadAllWithStreamBudget(body io.ReadCloser, budget StreamBudget) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	if !budget.Enabled() {
		return io.ReadAll(body)
	}

	var timedOut atomic.Bool
	var timer *time.Timer
	if budget.MaxDuration > 0 {
		timer = time.AfterFunc(budget.MaxDuration, func() {
			timedOut.Store(true)
			_ = body.Close()
		})
		defer timer.Stop()
	}

	reader := io.Reader(body)
	if budget.MaxBytes > 0 {
		reader = &streamBudgetReader{reader: body, maxBytes: budget.MaxBytes}
	}

	data, err := io.ReadAll(reader)
	if err != nil && timedOut.Load() {
		return data, interfaces.NewStreamBudgetError()
	}
	return data, err
}

// streamBudgetReader counts bytes and returns a terminal budget error as soon as
// the configured cap is crossed.
type streamBudgetReader struct {
	reader   io.Reader
	maxBytes int
	read     int
}

func (r *streamBudgetReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += n
		if r.maxBytes > 0 && r.read > r.maxBytes {
			return n, interfaces.NewStreamBudgetError()
		}
	}
	return n, err
}

func metadataInt(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func metadataInt64(metadata map[string]any, key string) int64 {
	value, ok := metadata[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
