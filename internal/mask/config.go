package mask

import (
	"bufio"
	"bytes"
	"strings"
)

// ParseConfig extracts the plugin's own settings from the normalized plugin YAML
// subtree supplied by the host on plugin.register/plugin.reconfigure. The host
// always adds at least `enabled` and `priority` keys, plus any user-defined keys
// from plugins.configs.<id>. Unknown keys and non-scalar values are ignored, so
// an unsupported YAML feature can never break loading.
func ParseConfig(data []byte) PluginConfig {
	cfg := DefaultConfig()
	if len(data) == 0 {
		return cfg
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(stripYAMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if key != "thinking-text" && key != "thinking_text" && key != "text" {
			continue
		}
		if text := unquoteYAML(value); text != "" {
			cfg.ThinkingText = text
		}
	}
	return cfg
}

// stripYAMLComment removes a trailing inline YAML comment. Values that contain
// " #" inside quotes are not unescaped here, which is acceptable for the simple
// scalar configuration this plugin accepts.
func stripYAMLComment(line string) string {
	if index := strings.Index(line, " #"); index >= 0 {
		return line[:index]
	}
	return line
}

// unquoteYAML strips a surrounding single or double quote from a scalar value
// and unescapes the common double-quote escapes.
func unquoteYAML(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}
	last := len(value) - 1
	switch {
	case value[0] == '\'' && value[last] == '\'':
		return value[1:last]
	case value[0] == '"' && value[last] == '"':
		inner := value[1:last]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	default:
		return value
	}
}
