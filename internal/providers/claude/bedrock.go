package claude

import "github.com/majorcontext/moat/internal/config"

// bedrockModelSet pairs a Bedrock model ID with its human display name.
// The *Name (display name) fields are always taken from defaultBedrockModels and are not user-overridable; only the model IDs may be overridden via moat.yaml claude.bedrock.models.
type bedrockModelSet struct {
	Haiku, HaikuName   string
	Sonnet, SonnetName string
	Opus, OpusName     string
	Custom, CustomName string
}

// defaultBedrockModels mirrors the model IDs agentbox currently ships.
// Override individual entries via moat.yaml claude.bedrock.models.
var defaultBedrockModels = bedrockModelSet{
	Haiku: "global.anthropic.claude-haiku-4-5-20251001-v1:0", HaikuName: "Haiku 4.5",
	Sonnet: "global.anthropic.claude-sonnet-4-6[1m]", SonnetName: "Sonnet 4.6",
	Opus: "global.anthropic.claude-opus-4-6-v1[1m]", OpusName: "Opus 4.6",
	Custom: "global.anthropic.claude-opus-4-7[1m]", CustomName: "Opus 4.7",
}

// pickModel returns override if non-empty, else fallback.
func pickModel(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// BedrockEnv returns the moat-injected process env vars that put Claude Code
// into Bedrock mode. These are the highest-precedence env layer (spec §3.4):
// they are emitted via proxyEnv, not the merged settings.json env block.
//
// AWS_SDK_LOAD_CONFIG is deliberately NOT set: it is an AWS SDK for Go v1
// flag with no effect on Claude Code's bundled Rust AWS SDK.
func BedrockEnv(bc config.BedrockConfig) []string {
	haiku := pickModel(bc.Models.Haiku, defaultBedrockModels.Haiku)
	sonnet := pickModel(bc.Models.Sonnet, defaultBedrockModels.Sonnet)
	opus := pickModel(bc.Models.Opus, defaultBedrockModels.Opus)
	custom := pickModel(bc.Models.Custom, defaultBedrockModels.Custom)
	return []string{
		"CLAUDE_CODE_USE_BEDROCK=1",
		"AWS_SDK_UA_APP_ID=ClaudeCode-Sandbox",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + haiku,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME=" + defaultBedrockModels.HaikuName,
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + sonnet,
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME=" + defaultBedrockModels.SonnetName,
		"ANTHROPIC_DEFAULT_OPUS_MODEL=" + opus,
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME=" + defaultBedrockModels.OpusName,
		"ANTHROPIC_CUSTOM_MODEL_OPTION=" + custom,
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME=" + defaultBedrockModels.CustomName,
	}
}

// BedrockTTLMillis is the value for CLAUDE_CODE_API_KEY_HELPER_TTL_MS, which
// controls how often Claude Code re-runs awsCredentialExport. The moat AWS
// endpoint already refreshes STS creds server-side and caches with a 5-minute
// pre-expiry buffer, so a conservative fixed 5-minute TTL is always safe.
func BedrockTTLMillis() string {
	return "300000"
}
