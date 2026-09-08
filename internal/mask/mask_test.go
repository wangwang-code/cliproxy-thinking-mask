package mask

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func contentChunk(content string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": nil,
			},
		},
	})
	return raw
}

func reasoningChunk(reasoning string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role":              "assistant",
					"content":           nil,
					"reasoning_content": reasoning,
				},
				"finish_reason": nil,
			},
		},
	})
	return raw
}

func TestRewriteInjectsThinkingAndKeepsContent(t *testing.T) {
	original := contentChunk("Hello there")
	out, injected, native := rewriteChatCompletionChunk(original, "Thinking hard")

	if native {
		t.Fatal("native = true, want false for a plain content chunk")
	}
	if !injected {
		t.Fatal("injected = false, want true for a plain content chunk")
	}
	if out == nil {
		t.Fatal("out is nil, want replacement body")
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("replacement is not valid JSON: %v", err)
	}
	content := gjsonString(root, "choices.0.delta.content")
	if content != "Hello there" {
		t.Fatalf("delta.content = %q, want %q (real content must be preserved)", content, "Hello there")
	}
	reasoning := gjsonString(root, "choices.0.delta.reasoning_content")
	if reasoning != "Thinking hard" {
		t.Fatalf("delta.reasoning_content = %q, want %q", reasoning, "Thinking hard")
	}
}

func TestRewriteDefaultsThinkingText(t *testing.T) {
	out, injected, _ := rewriteChatCompletionChunk(contentChunk("hi"), "")
	if !injected || out == nil {
		t.Fatalf("expected injection with default text, injected=%v out=%v", injected, out != nil)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got := gjsonString(root, "choices.0.delta.reasoning_content"); strings.TrimSpace(got) == "" {
		t.Fatalf("reasoning_content empty, want default text")
	}
}

func TestRewriteLeavesNativeReasoningUntouched(t *testing.T) {
	out, injected, native := rewriteChatCompletionChunk(reasoningChunk("real reasoning"), "Fake thinking")
	if !native {
		t.Fatal("native = false, want true for a chunk with reasoning_content")
	}
	if injected || out != nil {
		t.Fatalf("expected pass-through, injected=%v out=%v", injected, out != nil)
	}
}

func TestRewriteLeavesRoleChunkUntouched(t *testing.T) {
	role := []byte(`{"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	out, injected, native := rewriteChatCompletionChunk(role, "Fake thinking")
	if injected || native || out != nil {
		t.Fatalf("role chunk should pass through untouched, injected=%v native=%v out=%v", injected, native, out != nil)
	}
}

func TestRewriteLeavesNonOpenAIChunkUntouched(t *testing.T) {
	responses := []byte(`{"type":"response.output_text.delta","delta":"hi"}`)
	out, injected, native := rewriteChatCompletionChunk(responses, "Fake thinking")
	if injected || native || out != nil {
		t.Fatalf("non chat.completion.chunk should pass through, injected=%v native=%v out=%v", injected, native, out != nil)
	}
}

func TestRewriteLeavesInvalidJSONUntouched(t *testing.T) {
	out, injected, native := rewriteChatCompletionChunk([]byte(`not-json`), "Fake thinking")
	if injected || native || out != nil {
		t.Fatalf("invalid JSON should pass through, injected=%v native=%v out=%v", injected, native, out != nil)
	}
}

func TestProcessChunkInjectsOncePerRequest(t *testing.T) {
	masker := New(DefaultConfig())

	first := masker.ProcessChunk("req-1", 0, contentChunk("Hello"))
	if first == nil {
		t.Fatal("first content chunk should be rewritten")
	}
	if got := gjsonString(rawMessage(first), "choices.0.delta.reasoning_content"); got == "" {
		t.Fatalf("first chunk missing reasoning_content: %s", string(first))
	}

	second := masker.ProcessChunk("req-1", 1, contentChunk(" there"))
	if second != nil {
		t.Fatalf("second chunk of the same request should not be rewritten, got %s", string(second))
	}

	// A different request is still eligible.
	other := masker.ProcessChunk("req-2", 0, contentChunk("Hello again"))
	if other == nil {
		t.Fatal("first chunk of a new request should be rewritten")
	}
}

func TestProcessChunkHeaderInitPassedThrough(t *testing.T) {
	masker := New(DefaultConfig())
	if out := masker.ProcessChunk("req-h", -1, contentChunk("Hello")); out != nil {
		t.Fatalf("header-init call should pass through, got %s", string(out))
	}
}

func TestProcessChunkNativeShortCircuitsRequest(t *testing.T) {
	masker := New(DefaultConfig())
	if out := masker.ProcessChunk("req-n", 0, reasoningChunk("native reasoning")); out != nil {
		t.Fatalf("native reasoning chunk should pass through, got %s", string(out))
	}
	// Later content chunks on the same request must stay untouched.
	if out := masker.ProcessChunk("req-n", 1, contentChunk("answer")); out != nil {
		t.Fatalf("content after native reasoning should not be rewritten, got %s", string(out))
	}
}

func TestProcessChunkEmptyBodyPassedThrough(t *testing.T) {
	masker := New(DefaultConfig())
	if out := masker.ProcessChunk("req-e", 0, nil); out != nil {
		t.Fatalf("empty body should pass through, got %v", out)
	}
}

func TestConfigureUpdatesThinkingText(t *testing.T) {
	masker := New(DefaultConfig())
	masker.Configure(PluginConfig{ThinkingText: "Configured thinking"})

	out := masker.ProcessChunk("req-c", 0, contentChunk("hi"))
	if got := gjsonString(rawMessage(out), "choices.0.delta.reasoning_content"); got != "Configured thinking" {
		t.Fatalf("reasoning_content = %q, want %q", got, "Configured thinking")
	}
}

func TestParseConfigExtractsThinkingText(t *testing.T) {
	cfg := ParseConfig([]byte("enabled: true\npriority: 1\nthinking-text: \"Say something thoughtful\"\n"))
	if cfg.ThinkingText != "Say something thoughtful" {
		t.Fatalf("ThinkingText = %q, want %q", cfg.ThinkingText, "Say something thoughtful")
	}
}

func TestParseConfigIgnoresUnknownKeysAndKeepsDefault(t *testing.T) {
	cfg := ParseConfig([]byte("enabled: false\npriority: 0\nother: value\n"))
	if cfg.ThinkingText != DefaultThinkingText {
		t.Fatalf("ThinkingText = %q, want default %q", cfg.ThinkingText, DefaultThinkingText)
	}
}

// gjsonString is a tiny test helper that walks a dotted JSON path such as
// "choices.0.delta.content" — kept dependency-free.
func gjsonString(root map[string]json.RawMessage, path string) string {
	parts := strings.Split(path, ".")
	current := json.RawMessage(nil)
	ok := false
	for i, part := range parts {
		if i == 0 {
			current, ok = root[part]
			if !ok {
				return ""
			}
			continue
		}
		if index, errIndex := strconv.Atoi(part); errIndex == nil {
			var items []json.RawMessage
			if err := json.Unmarshal(current, &items); err != nil || index < 0 || index >= len(items) {
				return ""
			}
			current = items[index]
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return ""
		}
		current, ok = obj[part]
		if !ok {
			return ""
		}
	}
	var text string
	if err := json.Unmarshal(current, &text); err != nil {
		return ""
	}
	return text
}

func rawMessage(data []byte) map[string]json.RawMessage {
	var root map[string]json.RawMessage
	_ = json.Unmarshal(data, &root)
	return root
}
