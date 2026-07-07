package cli

import (
	"context"
	"os"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

// TestBedrockNetworkRulesViaRunProvider exercises the REAL Bedrock host-injection
// logic in RunProvider (internal/cli/provider.go lines 170–179) by calling the
// actual function — not a copy — and capturing cfg.Network.Rules via a stubbed
// ExecuteRun.  ExecuteRun is used as the seam because DryRun=true exits before
// ExecuteRun is reached (after the host injection but before we can inspect cfg).
// Stubbing ExecuteRun captures opts.Config at the point RunProvider hands off to
// execution, after all hosts have been appended to cfg.Network.Rules.
func TestBedrockNetworkRulesViaRunProvider(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantHosts []string
		noHosts   []string
	}{
		{
			name: "bedrock enabled default region injects us-east-1 hosts",
			yaml: "agent: claude\ngrants:\n  - aws\nclaude:\n  bedrock:\n    enabled: true\n",
			wantHosts: []string{
				"bedrock-runtime.us-east-1.amazonaws.com",
				"bedrock.us-east-1.amazonaws.com",
			},
		},
		{
			name: "bedrock enabled explicit eu-west-1 region injects eu-west-1 hosts",
			yaml: "agent: claude\ngrants:\n  - aws\nclaude:\n  bedrock:\n    enabled: true\n    region: eu-west-1\n",
			wantHosts: []string{
				"bedrock-runtime.eu-west-1.amazonaws.com",
				"bedrock.eu-west-1.amazonaws.com",
			},
			noHosts: []string{
				"bedrock-runtime.us-east-1.amazonaws.com",
				"bedrock.us-east-1.amazonaws.com",
			},
		},
		{
			name:    "bedrock absent: no bedrock hosts injected",
			yaml:    "agent: claude\n",
			noHosts: []string{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock.us-east-1.amazonaws.com"},
		},
		{
			name:    "bedrock disabled: no bedrock hosts injected",
			yaml:    "agent: claude\nclaude:\n  bedrock:\n    enabled: false\n",
			noHosts: []string{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock.us-east-1.amazonaws.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write moat.yaml to a temp directory so config.Load can find it.
			tmpDir, err := os.MkdirTemp("", "bedrock-cli-test-*")
			if err != nil {
				t.Fatalf("MkdirTemp: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			if err := os.WriteFile(tmpDir+"/moat.yaml", []byte(tt.yaml), 0o644); err != nil {
				t.Fatalf("WriteFile moat.yaml: %v", err)
			}

			// Stub ExecuteRun to capture the cfg handed off by RunProvider.
			// This is the package-level func var declared in internal/cli/globals.go.
			var capturedCfg *config.Config
			oldExecuteRun := ExecuteRun
			ExecuteRun = func(_ context.Context, opts ExecOptions) (*ExecResult, error) {
				capturedCfg = opts.Config
				return &ExecResult{}, nil
			}
			defer func() { ExecuteRun = oldExecuteRun }()

			// Build a cobra command named "claude" so cmd.CalledAs() == "claude"
			// passes the guard at RunProvider line 71.  We call cmd.Execute() so
			// cobra sets commandCalledAs.called=true and commandCalledAs.name="claude".
			var flags ExecFlags
			var runErr error
			cmd := &cobra.Command{
				Use:          "claude",
				SilenceUsage: true,
				RunE: func(cmd *cobra.Command, args []string) error {
					runErr = RunProvider(cmd, args, ProviderRunConfig{
						Name:  "claude",
						Flags: &flags,
						BuildCommand: func(_, _ string) ([]string, error) {
							return []string{"claude"}, nil
						},
					})
					return runErr
				},
			}
			AddExecFlags(cmd, &flags)

			// Pass the temp dir as the workspace argument.
			cmd.SetArgs([]string{tmpDir})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("cmd.Execute(): %v", err)
			}
			if runErr != nil {
				t.Fatalf("RunProvider returned error: %v", runErr)
			}
			if capturedCfg == nil {
				t.Fatal("ExecuteRun stub was never called; capturedCfg is nil")
			}

			// Build a set of hosts present in cfg.Network.Rules.
			hostSet := make(map[string]bool, len(capturedCfg.Network.Rules))
			for _, entry := range capturedCfg.Network.Rules {
				hostSet[entry.Host] = true
			}

			for _, h := range tt.wantHosts {
				if !hostSet[h] {
					t.Errorf("expected host %q in Network.Rules, got rules: %v", h, capturedCfg.Network.Rules)
				}
			}
			for _, h := range tt.noHosts {
				if hostSet[h] {
					t.Errorf("unexpected host %q in Network.Rules", h)
				}
			}
		})
	}
}
