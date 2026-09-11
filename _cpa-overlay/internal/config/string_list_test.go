package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSaveConfigPreserveCommentsWithLeadingNewlineList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := []byte("streaming:\n" +
		"  fake-thinking-texts:\n" +
		"    - \"\\n云翻译处于灰测中\"\n" +
		"    - \"\"\n" +
		"    - \"再耐心等等\"\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(original, &cfg); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}

	if err := SaveConfigPreserveComments(path, &cfg); err != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	var roundTrip Config
	if err := yaml.Unmarshal(saved, &roundTrip); err != nil {
		t.Fatalf("saved config is not valid YAML: %v\n%s", err, string(saved))
	}
	got := roundTrip.Streaming.FakeThinkingTexts
	want := StringList{"\n云翻译处于灰测中", "", "再耐心等等"}
	if len(got) != len(want) {
		t.Fatalf("fake-thinking-texts length = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fake-thinking-texts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
