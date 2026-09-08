// Package mask rewrites OpenAI-compatible chat completion stream chunks so the
// first content delta also carries a reasoning ("thinking") text. The
// cliproxy-thinking-mask CLIProxyAPI plugin uses it to present a plausible
// thinking preamble when the backend streams an answer without emitting real
// reasoning. The real content of the first chunk is preserved.
package mask

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// DefaultThinkingText is used when no thinking-text is configured.
const DefaultThinkingText = "Let me think through this step by step before giving the final answer."

// PluginConfig holds the plugin's runtime configuration.
type PluginConfig struct {
	// ThinkingText is attached to the first content delta as
	// delta.reasoning_content. When empty, DefaultThinkingText is used.
	ThinkingText string
}

// DefaultConfig returns the built-in configuration.
func DefaultConfig() PluginConfig {
	return PluginConfig{ThinkingText: DefaultThinkingText}
}

func normalizeConfig(cfg PluginConfig) PluginConfig {
	if strings.TrimSpace(cfg.ThinkingText) == "" {
		cfg.ThinkingText = DefaultThinkingText
	}
	return cfg
}

// Masker applies the one-shot "thinking text on the first content chunk"
// rewriting, tracked per streaming request id.
type Masker struct {
	mu    sync.RWMutex
	cfg   PluginConfig
	state *tracker
}

// New creates a Masker with the supplied configuration.
func New(cfg PluginConfig) *Masker {
	return &Masker{cfg: normalizeConfig(cfg), state: newTracker()}
}

// Configure replaces the configuration (called on plugin.reconfigure).
func (m *Masker) Configure(cfg PluginConfig) {
	cfg = normalizeConfig(cfg)
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// ProcessChunk inspects one streamed payload chunk. It returns a replacement
// body when the chunk should be rewritten, or nil to keep the chunk unchanged.
// Header-init calls (chunkIndex < 0) and empty bodies are always passed through.
func (m *Masker) ProcessChunk(requestID string, chunkIndex int, body []byte) []byte {
	if len(body) == 0 || chunkIndex < 0 {
		return nil
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()

	if requestID != "" && m.state.done(requestID) {
		return nil
	}

	out, injected, native := rewriteChatCompletionChunk(body, cfg.ThinkingText)
	switch {
	case native:
		// The backend already streams real reasoning; never fake a second one.
		if requestID != "" {
			m.state.markNative(requestID)
		}
		return nil
	case injected:
		if requestID != "" {
			m.state.markInjected(requestID)
		}
		return out
	default:
		return nil
	}
}

// rewriteChatCompletionChunk returns (replacement, injected, native).
//   - injected is true when this chunk was rewritten.
//   - native is true when this chunk already carries real reasoning text, in
//     which case no rewriting should happen for the request.
//
// Only OpenAI chat.completion.chunk frames with a text content delta are
// candidates. Every other payload is returned untouched.
func rewriteChatCompletionChunk(body []byte, thinking string) (out []byte, injected, native bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false, false
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, false
	}
	var object string
	if err := json.Unmarshal(root["object"], &object); err != nil || object != "chat.completion.chunk" {
		return nil, false, false
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil {
		return nil, false, false
	}

	// If any choice already streams non-empty reasoning, treat the whole request
	// as a native reasoning stream and leave it untouched.
	for _, raw := range choices {
		var choice map[string]json.RawMessage
		if err := json.Unmarshal(raw, &choice); err != nil {
			continue
		}
		deltaRaw, ok := choice["delta"]
		if !ok {
			continue
		}
		var delta map[string]json.RawMessage
		if err := json.Unmarshal(deltaRaw, &delta); err != nil {
			continue
		}
		if hasNativeReasoning(delta) {
			return nil, false, true
		}
	}

	if strings.TrimSpace(thinking) == "" {
		thinking = DefaultThinkingText
	}
	reasoningRaw, errMarshal := json.Marshal(thinking)
	if errMarshal != nil {
		return nil, false, false
	}

	// Attach the thinking text to the first chunk that carries answer content.
	for index, raw := range choices {
		var choice map[string]json.RawMessage
		if err := json.Unmarshal(raw, &choice); err != nil {
			continue
		}
		deltaRaw, ok := choice["delta"]
		if !ok {
			continue
		}
		var delta map[string]json.RawMessage
		if err := json.Unmarshal(deltaRaw, &delta); err != nil {
			continue
		}
		if textContent(delta) == "" {
			continue
		}
		delta["reasoning_content"] = reasoningRaw
		updatedDelta, errDelta := json.Marshal(delta)
		if errDelta != nil {
			return nil, false, false
		}
		choice["delta"] = updatedDelta
		updatedChoice, errChoice := json.Marshal(choice)
		if errChoice != nil {
			return nil, false, false
		}
		choices[index] = updatedChoice
		root["choices"], errMarshal = json.Marshal(choices)
		if errMarshal != nil {
			return nil, false, false
		}
		updated, errRoot := json.Marshal(root)
		if errRoot != nil {
			return nil, false, false
		}
		return updated, true, false
	}
	return nil, false, false
}

// textContent returns the trimmed text content of a delta, or "" when the delta
// has no string content (role chunks, tool-call chunks, null content, ...).
func textContent(delta map[string]json.RawMessage) string {
	raw, ok := delta["content"]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

// hasNativeReasoning reports whether a delta carries non-empty reasoning text.
func hasNativeReasoning(delta map[string]json.RawMessage) bool {
	raw, ok := delta["reasoning_content"]
	if !ok {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return false
	}
	return strings.TrimSpace(text) != ""
}

type trackerEntry struct {
	injected bool
	native   bool
	touched  time.Time
}

// tracker remembers per-request rewriting state so a stream is rewritten at
// most once. Entries expire so memory stays bounded under high concurrency.
type tracker struct {
	mu   sync.Mutex
	seen map[string]trackerEntry
}

const trackerMaxEntries = 4096

func newTracker() *tracker {
	return &tracker{seen: make(map[string]trackerEntry)}
}

// done reports whether the request has already been rewritten or was detected
// as a native reasoning stream.
func (t *tracker) done(requestID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.seen[requestID]
	return ok && (entry.injected || entry.native)
}

func (t *tracker) markInjected(requestID string) {
	t.mark(requestID, true, false)
}

func (t *tracker) markNative(requestID string) {
	t.mark(requestID, false, true)
}

func (t *tracker) mark(requestID string, injected, native bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[requestID] = trackerEntry{injected: injected, native: native, touched: time.Now()}
	if len(t.seen) > trackerMaxEntries {
		t.shrinkLocked()
	}
}

func (t *tracker) shrinkLocked() {
	cutoff := time.Now().Add(-5 * time.Minute)
	for id, entry := range t.seen {
		if entry.touched.Before(cutoff) {
			delete(t.seen, id)
		}
	}
	if len(t.seen) > trackerMaxEntries {
		// Extremely unlikely: more than 4096 concurrent long-lived streams.
		// Resetting only risks an extra thinking preamble on a few streams.
		t.seen = make(map[string]trackerEntry)
	}
}
