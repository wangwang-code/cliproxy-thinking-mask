package helps

import (
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// StreamBudget is the resolved upstream read budget for one non-streaming
// request. A zero value disables every cap.
type StreamBudget struct {
	MaxBytes    int
	MaxDuration time.Duration
	// MaxContentChars caps the client-visible answer characters of the completed
	// response. It is evaluated after aggregation because the byte cap already
	// bounds how much can be read.
	MaxContentChars int
}

// Enabled reports whether any read-time budget cap is active. The
// content-character cap is not a read-time cap, so it is excluded here.
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
	budget.MaxContentChars = metadataInt(opts.Metadata, cliproxyexecutor.StreamLimitMaxContentCharsMetadataKey)
	return budget
}

// ContentCharsExceeded reports whether a completed response payload carries more
// client-visible answer characters than maxChars. A maxChars <= 0 disables the cap.
func ContentCharsExceeded(payload []byte, maxChars int) bool {
	if maxChars <= 0 || len(payload) == 0 {
		return false
	}
	return CountResponseContentChars(payload) > maxChars
}

// CountResponseContentChars returns the number of client-visible answer
// characters in a completed (non-streaming) response payload. Only answer text
// counts: reasoning, tool calls and metadata are ignored, mirroring the
// delta.content accounting of the streaming ForwardStream path.
func CountResponseContentChars(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	root := gjson.ParseBytes(payload)
	// Codex terminal events wrap the response object in an SSE event envelope
	// ({"type":"response.completed","response":{...}}).
	if wrapped := root.Get("response"); wrapped.Type == gjson.JSON {
		root = wrapped
	}
	if choices := root.Get("choices"); choices.IsArray() {
		total := 0
		for _, choice := range choices.Array() {
			if content := choice.Get("message.content"); content.Type == gjson.String {
				total += len([]rune(content.String()))
				continue
			}
			// Legacy /v1/completions responses carry the text directly.
			if text := choice.Get("text"); text.Type == gjson.String {
				total += len([]rune(text.String()))
			}
		}
		return total
	}
	if output := root.Get("output"); output.IsArray() {
		total := 0
		for _, item := range output.Array() {
			total += countResponseContentPartsChars(item.Get("content"))
		}
		return total
	}
	if content := root.Get("content"); content.IsArray() {
		return countResponseContentPartsChars(content)
	}
	if candidates := root.Get("candidates"); candidates.IsArray() {
		total := 0
		for _, candidate := range candidates.Array() {
			parts := candidate.Get("content.parts")
			if !parts.IsArray() {
				continue
			}
			for _, part := range parts.Array() {
				total += len([]rune(part.Get("text").String()))
			}
		}
		return total
	}
	return 0
}

// countResponseContentPartsChars sums the text of OpenAI Responses / Claude
// content parts, skipping reasoning and tool-call parts.
func countResponseContentPartsChars(parts gjson.Result) int {
	if !parts.IsArray() {
		return 0
	}
	total := 0
	for _, part := range parts.Array() {
		switch strings.ToLower(strings.TrimSpace(part.Get("type").String())) {
		case "text", "output_text", "input_text":
			total += len([]rune(part.Get("text").String()))
		}
	}
	return total
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
