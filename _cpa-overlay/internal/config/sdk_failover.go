package config

// FailoverConfig configures silent failover for /v1/chat/completions requests
// whose primary provider (for example a codex OAuth pool) rejects the request
// at bootstrap with an overload / 502 / 503 error. When triggered, CPA re-runs
// the same request against the configured OpenAI-compatible endpoints (each must
// be enabled under the top-level `openai-compatibility` list); if every endpoint
// also fails, a synthetic overload error (terminal-message etc.) is returned.
type FailoverConfig struct {
	// Enabled turns the whole mechanism on.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// PrimaryProviders lists provider keys that are considered "primary". Failover
	// only triggers when the failing attempt was routed to one of them (matching
	// case-insensitively). Empty means any provider whose overload error qualifies.
	PrimaryProviders []string `yaml:"primary-providers" json:"primary-providers"`

	// Endpoints is the ordered list of OpenAI-compatible endpoints to try, in
	// ascending priority order. Each Name must match an entry name of the top-level
	// `openai-compatibility` list that is enabled at runtime.
	Endpoints []FailoverEndpoint `yaml:"endpoints" json:"endpoints"`

	// TerminalStatus is the HTTP status returned when every endpoint fails.
	// <= 0 defaults to 502.
	TerminalStatus int `yaml:"terminal-status,omitempty" json:"terminal-status,omitempty"`

	// TerminalType / TerminalCode / TerminalMessage build the synthetic error body
	// returned when every endpoint fails. Empty values fall back to
	// "service_unavailable_error" / "server_is_overloaded" /
	// "云翻译服务暂不可用，请稍后重试".
	TerminalType    string `yaml:"terminal-type,omitempty" json:"terminal-type,omitempty"`
	TerminalCode    string `yaml:"terminal-code,omitempty" json:"terminal-code,omitempty"`
	TerminalMessage string `yaml:"terminal-message,omitempty" json:"terminal-message,omitempty"`
}

// FailoverEndpoint describes one OpenAI-compatible fallback endpoint.
type FailoverEndpoint struct {
	// Name matches an enabled `openai-compatibility` entry name.
	Name string `yaml:"name" json:"name"`

	// Priority orders attempts ascending (lower first). Equal priorities keep the
	// config order.
	Priority int `yaml:"priority,omitempty" json:"priority,omitempty"`

	// Model optionally overrides the model sent to this endpoint. When empty the
	// requested model is kept and the endpoint's own alias mapping (if any) is
	// used.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

// DefaultFailoverTerminalStatus returns the HTTP status used for the synthetic
// terminal error when none is configured.
func DefaultFailoverTerminalStatus() int { return 502 }

// DefaultFailoverTerminalType is the error.type used for the synthetic terminal error.
func DefaultFailoverTerminalType() string { return "service_unavailable_error" }

// DefaultFailoverTerminalCode is the error.code used for the synthetic terminal error.
func DefaultFailoverTerminalCode() string { return "server_is_overloaded" }

// DefaultFailoverTerminalMessage is the error.message used for the synthetic terminal error.
func DefaultFailoverTerminalMessage() string { return "云翻译服务暂不可用，请稍后重试" }
