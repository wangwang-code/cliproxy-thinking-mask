package config

import (
	"strconv"
	"strings"
)

// ErrorRewriteConfig configures rewriting of upstream error responses before
// they are returned to clients. When enabled, errors whose HTTP status matches
// a configured entry are replaced with a custom message instead of leaking the
// upstream's raw error body, type, code, or response headers.
type ErrorRewriteConfig struct {
	// Enabled turns the rewrite on.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// DefaultMessage is used when a status code has no explicit entry in
	// StatusMessages. Empty means statuses without an explicit message are left
	// untouched.
	DefaultMessage string `yaml:"default-message,omitempty" json:"default-message,omitempty"`

	// StatusMessages maps an HTTP status code (as a string key, e.g. "429") to
	// the message returned to clients. If both StatusMessages and DefaultMessage
	// are empty, rewrite is effectively off.
	StatusMessages map[string]string `yaml:"status-messages,omitempty" json:"status-messages,omitempty"`
}

// MessageForStatus returns the configured rewrite message for status, falling
// back to DefaultMessage when the status has no specific entry.
func (c ErrorRewriteConfig) MessageForStatus(status int) string {
	if status > 0 {
		if m := strings.TrimSpace(c.StatusMessages[strconv.Itoa(status)]); m != "" {
			return m
		}
	}
	return strings.TrimSpace(c.DefaultMessage)
}

// Matches reports whether this rewrite config has a message for the status.
func (c ErrorRewriteConfig) Matches(status int) bool {
	return c.MessageForStatus(status) != ""
}
