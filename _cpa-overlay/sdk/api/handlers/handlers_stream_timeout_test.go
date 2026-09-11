package handlers

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestStreamingFirstEventTimeout(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "empty disabled", raw: "", want: 0},
		{name: "invalid disabled", raw: "abc", want: 0},
		{name: "zero disabled", raw: "0s", want: 0},
		{name: "five seconds", raw: "5s", want: 5 * time.Second},
		{name: "duration with spaces", raw: " 1500ms ", want: 1500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.SDKConfig{}
			cfg.Streaming.FirstEventTimeout = tt.raw
			if got := StreamingFirstEventTimeout(cfg); got != tt.want {
				t.Fatalf("StreamingFirstEventTimeout(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStreamingFirstEventTimeoutNilConfig(t *testing.T) {
	if got := StreamingFirstEventTimeout(nil); got != 0 {
		t.Fatalf("StreamingFirstEventTimeout(nil) = %v, want 0", got)
	}
}

func TestStreamingStallTimeout(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "empty disabled", raw: "", want: 0},
		{name: "invalid disabled", raw: "abc", want: 0},
		{name: "zero disabled", raw: "0s", want: 0},
		{name: "ten seconds", raw: "10s", want: 10 * time.Second},
		{name: "duration with spaces", raw: " 2s ", want: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.SDKConfig{}
			cfg.Streaming.StallTimeout = tt.raw
			if got := StreamingStallTimeout(cfg); got != tt.want {
				t.Fatalf("StreamingStallTimeout(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStreamingStallTimeoutNilConfig(t *testing.T) {
	if got := StreamingStallTimeout(nil); got != 0 {
		t.Fatalf("StreamingStallTimeout(nil) = %v, want 0", got)
	}
}
