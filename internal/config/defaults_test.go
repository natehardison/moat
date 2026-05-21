package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)

	t.Run("missing file returns nil,nil", func(t *testing.T) {
		cfg, err := LoadDefaults()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg != nil {
			t.Fatalf("cfg = %#v, want nil", cfg)
		}
	})

	t.Run("present file is parsed", func(t *testing.T) {
		path := filepath.Join(tmp, "defaults.yaml")
		content := `agent: claude
grants:
  - aws
claude:
  base_url: https://llm-proxy.example.com
`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDefaults()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg == nil {
			t.Fatal("cfg = nil, want non-nil")
		}
		if cfg.Agent != "claude" {
			t.Errorf("Agent = %q, want claude", cfg.Agent)
		}
		if len(cfg.Grants) != 1 || cfg.Grants[0] != "aws" {
			t.Errorf("Grants = %v, want [aws]", cfg.Grants)
		}
		if cfg.Claude.BaseURL != "https://llm-proxy.example.com" {
			t.Errorf("Claude.BaseURL = %q, want https://llm-proxy.example.com", cfg.Claude.BaseURL)
		}
	})

	t.Run("malformed yaml returns error", func(t *testing.T) {
		path := filepath.Join(tmp, "defaults.yaml")
		if err := os.WriteFile(path, []byte("agent: [unterminated"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadDefaults()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
