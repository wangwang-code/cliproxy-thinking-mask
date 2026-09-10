package openai

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteKeepAliveFakeThinkingSequential(t *testing.T) {
	texts := []string{"first keepalive", "second keepalive", "third keepalive"}
	var buf bytes.Buffer
	index := 0

	index = writeKeepAliveFakeThinking(&buf, "gpt-test", texts, index)
	if index != 1 {
		t.Fatalf("index after first keepalive = %d, want 1", index)
	}
	if !strings.Contains(buf.String(), ": keep-alive\n\n") {
		t.Fatalf("first output missing keepalive comment: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"reasoning_content":"first keepalive"`) {
		t.Fatalf("first output missing first thinking frame: %q", buf.String())
	}
	if got := strings.Count(buf.String(), "data: "); got != 1 {
		t.Fatalf("first output data frames = %d, want 1", got)
	}

	var second bytes.Buffer
	index = writeKeepAliveFakeThinking(&second, "gpt-test", texts, index)
	if index != 2 {
		t.Fatalf("index after second keepalive = %d, want 2", index)
	}
	if !strings.Contains(second.String(), `"reasoning_content":"second keepalive"`) {
		t.Fatalf("second output missing second thinking frame: %q", second.String())
	}

	var third bytes.Buffer
	index = writeKeepAliveFakeThinking(&third, "gpt-test", texts, index)
	if index != 3 {
		t.Fatalf("index after third keepalive = %d, want 3", index)
	}
	if !strings.Contains(third.String(), `"reasoning_content":"third keepalive"`) {
		t.Fatalf("third output missing third thinking frame: %q", third.String())
	}

	var exhausted bytes.Buffer
	index = writeKeepAliveFakeThinking(&exhausted, "gpt-test", texts, index)
	if index != 3 {
		t.Fatalf("index after exhausted keepalive = %d, want 3", index)
	}
	if !strings.Contains(exhausted.String(), ": keep-alive\n\n") {
		t.Fatalf("exhausted output missing keepalive comment: %q", exhausted.String())
	}
	if got := strings.Count(exhausted.String(), "data: "); got != 0 {
		t.Fatalf("exhausted output data frames = %d, want 0", got)
	}
}

func TestWriteKeepAliveFakeThinkingEmptyList(t *testing.T) {
	var buf bytes.Buffer
	index := writeKeepAliveFakeThinking(&buf, "gpt-test", nil, 0)
	if index != 0 {
		t.Fatalf("index = %d, want 0", index)
	}
	if buf.String() != ": keep-alive\n\n" {
		t.Fatalf("output = %q, want only keepalive comment", buf.String())
	}
}

func TestWriteKeepAliveFakeThinkingSkipsEmptyEntry(t *testing.T) {
	texts := []string{"first", "", "   ", "third"}
	var buf bytes.Buffer
	index := 0

	index = writeKeepAliveFakeThinking(&buf, "gpt-test", texts, index)
	if index != 1 {
		t.Fatalf("index after first = %d, want 1", index)
	}
	if got := strings.Count(buf.String(), "data: "); got != 1 {
		t.Fatalf("first output data frames = %d, want 1", got)
	}

	var empty bytes.Buffer
	index = writeKeepAliveFakeThinking(&empty, "gpt-test", texts, index)
	if index != 2 {
		t.Fatalf("index after empty = %d, want 2", index)
	}
	if empty.String() != ": keep-alive\n\n" {
		t.Fatalf("empty output = %q, want only keepalive comment", empty.String())
	}

	var whitespace bytes.Buffer
	index = writeKeepAliveFakeThinking(&whitespace, "gpt-test", texts, index)
	if index != 3 {
		t.Fatalf("index after whitespace = %d, want 3", index)
	}
	if whitespace.String() != ": keep-alive\n\n" {
		t.Fatalf("whitespace output = %q, want only keepalive comment", whitespace.String())
	}

	var third bytes.Buffer
	index = writeKeepAliveFakeThinking(&third, "gpt-test", texts, index)
	if index != 4 {
		t.Fatalf("index after third = %d, want 4", index)
	}
	if !strings.Contains(third.String(), `"reasoning_content":"third"`) {
		t.Fatalf("third output missing third thinking frame: %q", third.String())
	}
}
