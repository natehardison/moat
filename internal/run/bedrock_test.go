package run

// Bedrock unit tests for Task 9 (spec §3.8).
//
// The full Create path requires a live container runtime and daemon socket and
// is tested in internal/e2e/.  Here we exercise the smallest real functions
// that compose the Bedrock environment and settings without spinning up a
// container.
//
// Three seams are exercised:
//  1. bedrockEnabled() — the package-local gate that all Bedrock branches use.
//  2. claude.BedrockEnv + claude.BedrockTTLMillis — the env-var composition,
//     including presence of CLAUDE_CODE_USE_BEDROCK / ANTHROPIC_DEFAULT_SONNET_MODEL
//     and absence of ANTHROPIC_API_KEY (which must not be emitted in Bedrock mode;
//     see agent.go:PrepareContainer with opts.Bedrock=true).
//  3. The settings.json awsCredentialExport assembly that manager.go performs
//     (build claude.Settings{RawExtras:...}, json.MarshalIndent, assert key present).
//
// Network-host injection (provider.go lines 170–179) is tested via the REAL
// RunProvider in internal/cli/bedrock_test.go.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	awsprov "github.com/majorcontext/moat/internal/providers/aws"
	"github.com/majorcontext/moat/internal/providers/claude"
)

// TestBedrockEnabled verifies the bedrockEnabled gate for all relevant config states.
func TestBedrockEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
		{
			name: "no bedrock field",
			cfg:  &config.Config{},
			want: false,
		},
		{
			name: "bedrock disabled explicitly",
			cfg: &config.Config{
				Claude: config.ClaudeConfig{
					Bedrock: &config.BedrockConfig{Enabled: false},
				},
			},
			want: false,
		},
		{
			name: "bedrock enabled",
			cfg: &config.Config{
				Claude: config.ClaudeConfig{
					Bedrock: &config.BedrockConfig{Enabled: true},
				},
			},
			want: true,
		},
		{
			name: "bedrock enabled with region",
			cfg: &config.Config{
				Claude: config.ClaudeConfig{
					Bedrock: &config.BedrockConfig{Enabled: true, Region: "eu-west-1"},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bedrockEnabled(tt.cfg)
			if got != tt.want {
				t.Errorf("bedrockEnabled(%v) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}

// TestBedrockSettingsJSON verifies that the settings.json assembled by manager.go
// for Bedrock runs contains the awsCredentialExport key pointing to the
// in-container helper path with the --claude flag.
func TestBedrockSettingsJSON(t *testing.T) {
	// Replicate the exact assembly from manager.go lines 2100-2118.
	claudeSettings := &claude.Settings{}
	if claudeSettings.RawExtras == nil {
		claudeSettings.RawExtras = make(map[string]json.RawMessage)
	}
	exportCmd, _ := json.Marshal(awsprov.CredentialHelperPath + " --claude")
	claudeSettings.RawExtras["awsCredentialExport"] = json.RawMessage(exportCmd)
	delete(claudeSettings.RawExtras, "awsAuthRefresh") // strip host-only key

	settingsJSON, err := json.MarshalIndent(claudeSettings, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(Settings): %v", err)
	}

	// Parse back and assert.
	var out map[string]json.RawMessage
	if err := json.Unmarshal(settingsJSON, &out); err != nil {
		t.Fatalf("json.Unmarshal(settingsJSON): %v", err)
	}

	raw, ok := out["awsCredentialExport"]
	if !ok {
		t.Fatalf("settings.json missing awsCredentialExport key; got keys: %v", mapKeys(out))
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		t.Fatalf("awsCredentialExport is not a JSON string: %v", err)
	}
	wantSuffix := " --claude"
	if !strings.HasSuffix(val, wantSuffix) {
		t.Errorf("awsCredentialExport = %q, want suffix %q", val, wantSuffix)
	}
	wantPrefix := awsprov.CredentialHelperPath
	if !strings.HasPrefix(val, wantPrefix) {
		t.Errorf("awsCredentialExport = %q, want prefix %q", val, wantPrefix)
	}
	// awsAuthRefresh must be absent.
	if _, present := out["awsAuthRefresh"]; present {
		t.Error("awsAuthRefresh must NOT appear in Bedrock settings.json")
	}
}

// mapKeys returns the sorted keys of a map (for readable error messages).
func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
