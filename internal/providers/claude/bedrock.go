package claude

// BedrockEnv returns the moat-injected process env vars that put Claude Code
// into Bedrock mode. Model pinning is left to claude.env
// (ANTHROPIC_DEFAULT_*_MODEL / ANTHROPIC_MODEL), and credential refresh is
// driven by the Expiration field in the awsCredentialExport envelope.
func BedrockEnv() []string {
	return []string{"CLAUDE_CODE_USE_BEDROCK=1"}
}
