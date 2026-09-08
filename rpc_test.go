package main

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func chunkRequest(requestID string, chunkIndex int, body []byte) []byte {
	raw, _ := json.Marshal(map[string]any{
		"RequestID":  requestID,
		"ChunkIndex": chunkIndex,
		"Body":       body, // []byte is base64-encoded by encoding/json
	})
	return raw
}

func chatChunk(content string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{"role": "assistant", "content": content},
			},
		},
	})
	return raw
}

func decodeEnvelope(t *testing.T, raw []byte) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope ok = false: %s", string(raw))
	}
	return env
}

func TestHandleMethodRegister(t *testing.T) {
	out, errHandle := handleMethod("plugin.register", nil)
	if errHandle != nil {
		t.Fatalf("register failed: %v", errHandle)
	}
	env := decodeEnvelope(t, out)
	var reg registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("registration result invalid: %v", err)
	}
	if reg.SchemaVersion == 0 {
		t.Fatal("schema_version missing")
	}
	if !reg.Capabilities.ResponseStreamInterceptor {
		t.Fatal("response_stream_interceptor capability not declared")
	}
}

func TestHandleMethodInterceptStreamChunkInjects(t *testing.T) {
	req := chunkRequest("req-1", 0, chatChunk("Hello"))
	out, errHandle := handleMethod("response.intercept_stream_chunk", req)
	if errHandle != nil {
		t.Fatalf("intercept failed: %v", errHandle)
	}
	env := decodeEnvelope(t, out)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("result invalid: %v", err)
	}
	bodyRaw, ok := result["Body"]
	if !ok {
		t.Fatalf("result has no Body; unchanged response: %s", string(env.Result))
	}
	var b64 string
	if err := json.Unmarshal(bodyRaw, &b64); err != nil {
		t.Fatalf("Body is not a base64 string: %v", err)
	}
	decoded, errDecode := base64.StdEncoding.DecodeString(b64)
	if errDecode != nil {
		t.Fatalf("Body is not valid base64: %v", errDecode)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &root); err != nil {
		t.Fatalf("replacement body is not valid JSON: %v", err)
	}
	if got := string(pathValue(t, root, "choices.0.delta.content")); got != `"Hello"` {
		t.Fatalf("content = %s, want \"Hello\" (real content must be preserved)", got)
	}
	if got := string(pathValue(t, root, "choices.0.delta.reasoning_content")); got == "" || got == "null" {
		t.Fatalf("reasoning_content missing in replacement body: %s", string(decoded))
	}
}

func TestHandleMethodInterceptStreamChunkPassesOtherProtocols(t *testing.T) {
	other := []byte(`{"type":"response.output_text.delta","delta":"hi"}`)
	req := chunkRequest("req-other", 0, other)
	out, errHandle := handleMethod("response.intercept_stream_chunk", req)
	if errHandle != nil {
		t.Fatalf("intercept failed: %v", errHandle)
	}
	env := decodeEnvelope(t, out)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("result invalid: %v", err)
	}
	if _, ok := result["Body"]; ok {
		t.Fatalf("non-OpenAI chat chunk must not be rewritten: %s", string(env.Result))
	}
}

// pathValue walks a dotted JSON path (object keys and numeric array indexes)
// starting from a decoded JSON object, returning the raw value at the path.
func pathValue(t *testing.T, root map[string]json.RawMessage, path string) json.RawMessage {
	t.Helper()
	current := json.RawMessage(nil)
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if i == 0 {
			var ok bool
			current, ok = root[part]
			if !ok {
				return nil
			}
			continue
		}
		if index, errIndex := strconv.Atoi(part); errIndex == nil {
			var items []json.RawMessage
			if err := json.Unmarshal(current, &items); err != nil || index < 0 || index >= len(items) {
				return nil
			}
			current = items[index]
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return nil
		}
		var ok bool
		current, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return current
}
