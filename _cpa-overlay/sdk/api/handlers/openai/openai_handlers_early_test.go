package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildEarlyThinkingChunkCustomText(t *testing.T) {
	raw := buildEarlyThinkingChunk("gpt-test", "Let me analyze this carefully.")
	if len(raw) == 0 {
		t.Fatal("buildEarlyThinkingChunk returned empty payload")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("chunk is not valid JSON: %v", err)
	}
	var object string
	if err := json.Unmarshal(root["object"], &object); err != nil || object != "chat.completion.chunk" {
		t.Fatalf("object = %q, want chat.completion.chunk", object)
	}
	var model string
	if err := json.Unmarshal(root["model"], &model); err != nil || model != "gpt-test" {
		t.Fatalf("model = %q, want gpt-test", model)
	}
	content, errContent := json.Marshal(root)
	_ = content
	_ = errContent
	var chunk struct {
		Choices []struct {
			Delta struct {
				Role             string          `json:"role"`
				ReasoningContent string          `json:"reasoning_content"`
				Content          json.RawMessage `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		t.Fatalf("chunk decode failed: %v", err)
	}
	if len(chunk.Choices) == 0 {
		t.Fatal("chunk has no choices")
	}
	delta := chunk.Choices[0].Delta
	if delta.Role != "assistant" {
		t.Fatalf("role = %q, want assistant", delta.Role)
	}
	if delta.ReasoningContent != "Let me analyze this carefully." {
		t.Fatalf("reasoning_content = %q, want configured text", delta.ReasoningContent)
	}
	if len(delta.Content) != 0 {
		t.Fatalf("content must be omitted in the early thinking chunk, got %s", string(delta.Content))
	}
}

func TestBuildEarlyThinkingChunkUsesDefaultText(t *testing.T) {
	raw := buildEarlyThinkingChunk("gpt-test", "   ")
	var chunk struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		t.Fatalf("chunk decode failed: %v", err)
	}
	if len(chunk.Choices) == 0 || strings.TrimSpace(chunk.Choices[0].Delta.ReasoningContent) == "" {
		t.Fatal("expected a non-empty default thinking text")
	}
}

func TestBuildEarlyThinkingChunkPreservesLeadingNewline(t *testing.T) {
	raw := buildEarlyThinkingChunk("gpt-test", "\n云翻译处于灰测中")
	var chunk struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		t.Fatalf("chunk decode failed: %v", err)
	}
	if len(chunk.Choices) == 0 {
		t.Fatal("chunk has no choices")
	}
	got := chunk.Choices[0].Delta.ReasoningContent
	if got != "\n云翻译处于灰测中" {
		t.Fatalf("reasoning_content = %q, want leading newline preserved", got)
	}
}
