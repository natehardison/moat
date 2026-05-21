package claude

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestBedrockEnvDefaults(t *testing.T) {
	env := BedrockEnv(config.BedrockConfig{Enabled: true}) // Enabled is intentionally ignored by BedrockEnv; the caller gates on it
	m := envSliceToMap(env)
	if m["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q, want 1", m["CLAUDE_CODE_USE_BEDROCK"])
	}
	if m["ANTHROPIC_DEFAULT_SONNET_MODEL"] != defaultBedrockModels.Sonnet {
		t.Errorf("sonnet = %q, want default %q", m["ANTHROPIC_DEFAULT_SONNET_MODEL"], defaultBedrockModels.Sonnet)
	}
	if m["AWS_SDK_UA_APP_ID"] != "ClaudeCode-Sandbox" {
		t.Errorf("AWS_SDK_UA_APP_ID = %q", m["AWS_SDK_UA_APP_ID"])
	}
	if _, ok := m["AWS_SDK_LOAD_CONFIG"]; ok {
		t.Error("AWS_SDK_LOAD_CONFIG must NOT be set (Go-SDK-only flag)")
	}
}

func TestBedrockEnvOverride(t *testing.T) {
	env := BedrockEnv(config.BedrockConfig{
		Enabled: true,
		Models:  config.BedrockModels{Opus: "my.opus", Haiku: ""},
	})
	m := envSliceToMap(env)
	if m["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "my.opus" {
		t.Errorf("opus override = %q, want my.opus", m["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if m["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != defaultBedrockModels.Haiku {
		t.Errorf("empty haiku should fall back to default, got %q", m["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	}
}

func TestBedrockTTLMillis(t *testing.T) {
	if got := BedrockTTLMillis(); got != "300000" {
		t.Errorf("BedrockTTLMillis() = %q, want 300000", got)
	}
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				m[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return m
}
