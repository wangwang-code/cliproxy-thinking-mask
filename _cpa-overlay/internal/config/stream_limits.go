package config

// StreamLimitsConfig configures per-request upstream stream budgets. Rules are
// matched against the downstream API key and/or model, and the byte budget can
// be computed dynamically from the request input size.
type StreamLimitsConfig struct {
	// Enabled turns stream limits on.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Rules are evaluated in order; the first matching rule wins.
	Rules []StreamLimitRule `yaml:"rules" json:"rules"`
}

// StreamLimitRule describes one matched stream budget.
type StreamLimitRule struct {
	// Name is a human-readable label used in diagnostics.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// APIKeys matches the downstream API key exactly. Empty matches any key.
	APIKeys []string `yaml:"api-keys,omitempty" json:"api-keys,omitempty"`

	// Models matches the requested model name. Supports shell-style wildcards
	// such as "gpt-*". Empty matches any model.
	Models []string `yaml:"models,omitempty" json:"models,omitempty"`

	// InputBudget computes the maximum upstream stream bytes from the request's
	// input text length.
	InputBudget StreamLimitInputBudget `yaml:"input-budget" json:"input-budget"`

	// MaxStreamDuration is a hard wall-clock cap for one upstream stream.
	// Empty or invalid disables the duration cap.
	MaxStreamDuration string `yaml:"max-stream-duration,omitempty" json:"max-stream-duration,omitempty"`

	// MaxContentChars caps the cumulative content characters forwarded from the
	// upstream stream. <= 0 disables the cap.
	MaxContentChars int `yaml:"max-content-chars,omitempty" json:"max-content-chars,omitempty"`
}

// StreamLimitInputBudget computes max upstream stream bytes from input chars.
type StreamLimitInputBudget struct {
	// BaseBytes is added to every request budget.
	BaseBytes int `yaml:"base-bytes,omitempty" json:"base-bytes,omitempty"`
	// BytesPerInputChar scales with the request's input text character count.
	BytesPerInputChar int `yaml:"bytes-per-input-char,omitempty" json:"bytes-per-input-char,omitempty"`
	// MinBytes / MaxBytes clamp the computed budget.
	MinBytes int `yaml:"min-bytes,omitempty" json:"min-bytes,omitempty"`
	MaxBytes int `yaml:"max-bytes,omitempty" json:"max-bytes,omitempty"`
}
