package config

import "testing"

func TestErrorRewriteMessageForStatus(t *testing.T) {
	cfg := ErrorRewriteConfig{
		StatusMessages: map[string]string{
			"429": "[云翻译]被上游限流，请稍微再试",
		},
		DefaultMessage: "[云翻译]上游服务暂时不可用，请稍后重试",
	}

	if got := cfg.MessageForStatus(429); got != "[云翻译]被上游限流，请稍微再试" {
		t.Fatalf("MessageForStatus(429) = %q", got)
	}
	if got := cfg.MessageForStatus(502); got != "[云翻译]上游服务暂时不可用，请稍后重试" {
		t.Fatalf("MessageForStatus(502) = %q", got)
	}
	if got := cfg.MessageForStatus(0); got != "[云翻译]上游服务暂时不可用，请稍后重试" {
		t.Fatalf("MessageForStatus(0) = %q", got)
	}
	if !cfg.Matches(429) || !cfg.Matches(503) {
		t.Fatal("Matches() should be true for 429 and 503")
	}
}

func TestErrorRewriteNoMessage(t *testing.T) {
	cfg := ErrorRewriteConfig{}
	if got := cfg.MessageForStatus(429); got != "" {
		t.Fatalf("MessageForStatus(429) = %q, want empty", got)
	}
	if cfg.Matches(429) {
		t.Fatal("Matches(429) should be false when no messages configured")
	}
}
