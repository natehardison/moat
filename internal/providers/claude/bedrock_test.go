package claude

import "testing"

func TestBedrockEnv(t *testing.T) {
	env := BedrockEnv()

	if len(env) != 1 || env[0] != "CLAUDE_CODE_USE_BEDROCK=1" {
		t.Fatalf("BedrockEnv() = %v, want exactly [CLAUDE_CODE_USE_BEDROCK=1]", env)
	}
}
