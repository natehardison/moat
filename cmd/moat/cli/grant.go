// cmd/moat/cli/grant.go
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
	"github.com/majorcontext/moat/internal/providers/aws"
	"github.com/majorcontext/moat/internal/providers/configprovider"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// AWS grant flags - these need to be passed to the AWS provider
var (
	awsRole            string
	awsRegion          string
	awsSessionDuration string
	awsExternalID      string
	awsProfile         string
)

var grantHost string

var grantCmd = &cobra.Command{
	Use:   "grant <provider>",
	Short: "Grant a credential for use in runs",
	Long: `Grant a credential that can be used by agent runs.

Credentials are stored securely and injected into agent containers when
requested via the --grant flag on 'moat run'.

Use --profile (or MOAT_PROFILE env var) to store credentials in a named profile.
Profile-scoped credentials are isolated from the default store.

Run 'moat grant providers' to list all available providers.

Subcommands:
  providers   List all available credential providers
  ssh         Grant SSH access for a specific host
  mcp         Grant credentials for an MCP server

Examples:
  moat grant claude                              # Grant Claude OAuth token (for moat claude)
  moat grant anthropic                           # Grant Anthropic API key (for any agent)
  moat grant github                              # Grant GitHub access
  moat grant aws --role=arn:aws:...              # Grant AWS access via IAM role
  moat grant github --profile myproject          # Grant GitHub access in a profile
  moat grant providers                           # List all available providers
  moat run my-agent . --grant github             # Use credential in a run
  moat run --grant github --profile myproject    # Use profile-scoped credential`,
	Args: cobra.MinimumNArgs(1),
	RunE: runGrant,
}

func init() {
	rootCmd.AddCommand(grantCmd)
	grantCmd.Flags().StringVar(&awsRole, "role", "", "IAM role ARN to assume (required for aws)")
	grantCmd.Flags().StringVar(&awsRegion, "region", "", "AWS region (default: us-east-1)")
	grantCmd.Flags().StringVar(&awsSessionDuration, "session-duration", "", "Session duration (default: 15m, max: 12h)")
	grantCmd.Flags().StringVar(&awsExternalID, "external-id", "", "External ID for role assumption")
	grantCmd.Flags().StringVar(&awsProfile, "aws-profile", "", "AWS shared config profile for role assumption (falls back to AWS_PROFILE env var if not set)")
	grantCmd.Flags().StringVar(&grantHost, "host", "", "Custom host for YAML-defined providers (e.g., gitlab.acme.com for self-hosted GitLab)")
}

// saveCredential stores a credential and returns the file path.
func saveCredential(cred credential.Credential) (string, error) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("getting encryption key: %w", err)
	}
	storeDir := credential.DefaultStoreDir()
	store, err := credential.NewFileStore(storeDir, key)
	if err != nil {
		return "", fmt.Errorf("opening credential store: %w", err)
	}
	if err := store.Save(cred); err != nil {
		return "", fmt.Errorf("saving credential: %w", err)
	}
	return filepath.Join(storeDir, string(cred.Provider)+".enc"), nil
}

func runGrant(cmd *cobra.Command, args []string) error {
	providerName := args[0]

	// Map CLI names to provider names
	// "openai" is the CLI name, but the provider is registered as "codex"
	// "google" is an alias for "gemini"
	// "anthropic" and "claude" are separate registered providers; no remapping needed
	switch providerName {
	case "openai":
		providerName = "codex"
	case "google":
		providerName = "gemini"
	}

	if grantHost != "" {
		overridden, err := runHostOverride(providerName, grantHost)
		if err != nil {
			return err
		}
		return grantWithOverride(cmd.Context(), overridden)
	}

	// Look up provider in registry
	prov := provider.Get(providerName)
	if prov == nil {
		return fmt.Errorf("unknown provider: %s\n\nRun 'moat grant providers' to list all available providers",
			args[0])
	}

	// For AWS, validate required flags before calling Grant
	if providerName == "aws" && awsRole == "" {
		return fmt.Errorf(`--role is required for AWS grant

Usage: moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE_NAME

Options:
  --role             IAM role ARN to assume (required)
  --region           AWS region (default: us-east-1)
  --session-duration Session duration (default: 15m, max: 12h)
  --external-id      External ID for role assumption
  --aws-profile      AWS shared config profile (falls back to AWS_PROFILE env var)`)
	}

	// Call the provider's Grant method
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// For AWS, pass the CLI flags via context
	if providerName == "aws" {
		ctx = aws.WithGrantOptions(ctx, awsRole, awsRegion, awsSessionDuration, awsExternalID, awsProfile)
	}

	provCred, err := prov.Grant(ctx)
	if err != nil {
		return err
	}

	// Convert to credential.Credential for storage
	cred := credential.Credential{
		Provider:  credential.Provider(provCred.Provider),
		Token:     provCred.Token,
		Scopes:    provCred.Scopes,
		ExpiresAt: provCred.ExpiresAt,
		CreatedAt: provCred.CreatedAt,
		Metadata:  provCred.Metadata,
	}

	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}

	if credential.ActiveProfile != "" {
		fmt.Printf("Credential saved to %s (profile: %s)\n", credPath, credential.ActiveProfile)
	} else {
		fmt.Printf("Credential saved to %s\n", credPath)
	}
	return nil
}

// readPassword reads a password from stdin without echoing.
// This is used by grant subcommands that need to prompt for secrets.
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return term.ReadPassword(fd)
	}
	// Not a terminal, read normally (for piped input)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	return []byte(strings.TrimSuffix(line, "\n")), err
}

// errOverrideAborted signals that the user declined to overwrite an existing
// user override. Returned by runHostOverride so runGrant can exit non-zero
// without printing a generic error.
var errOverrideAborted = errors.New("aborted: existing override not overwritten")

// runHostOverride validates the host, loads the embedded provider def, applies
// the override, optionally prompts before overwriting an existing user file,
// writes the file, and returns the in-memory overridden def.
func runHostOverride(providerName, host string) (configprovider.ProviderDef, error) {
	if err := configprovider.ValidateHostname(host); err != nil {
		return configprovider.ProviderDef{}, err
	}

	def, err := configprovider.LoadEmbeddedDef(providerName)
	if err != nil {
		return configprovider.ProviderDef{}, fmt.Errorf(
			"--host is not supported for %q (built-in provider with a fixed host)\nSupported providers: %s",
			providerName, strings.Join(configprovider.EmbeddedProviderNames(), ", "),
		)
	}

	overridden, err := configprovider.ApplyHostOverride(def, host)
	if err != nil {
		return configprovider.ProviderDef{}, err
	}

	path := configprovider.UserOverridePath(providerName)
	if err := writeOverrideIfChanged(path, providerName, overridden, host); err != nil {
		return configprovider.ProviderDef{}, err
	}

	return overridden, nil
}

// writeOverrideIfChanged inspects an existing override file (if any) and
// either skips, prompts, or writes the new override.
func writeOverrideIfChanged(path, providerName string, overridden configprovider.ProviderDef, host string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading existing override %s: %w", path, err)
	}
	if err == nil {
		existingDef, parseErr := configprovider.ParseProviderDef(existing)
		if parseErr != nil {
			return fmt.Errorf("existing override at %s is invalid YAML; remove or fix it before re-running: %w", path, parseErr)
		}
		if overridesMatch(existingDef, overridden) {
			fmt.Printf("Override at %s already set to %s — no changes needed\n", path, host)
			return nil
		}
		fmt.Printf("Existing override at %s: hosts=%v\n", path, existingDef.Hosts)
		fmt.Printf("New override:           hosts=%v\n", overridden.Hosts)
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintf(os.Stderr, "Non-interactive session: re-run interactively to confirm, or remove %s to accept the new override\n", path)
			return errOverrideAborted
		}
		ok, err := util.Confirm("Overwrite?")
		if err != nil {
			return err
		}
		if !ok {
			return errOverrideAborted
		}
		if err := configprovider.WriteUserOverride(providerName, overridden); err != nil {
			return err
		}
		fmt.Printf("Updated provider override at %s\n", path)
		return nil
	}
	if err := configprovider.WriteUserOverride(providerName, overridden); err != nil {
		return err
	}
	fmt.Printf("Wrote provider override to %s\n", path)
	return nil
}

// overridesMatch compares only the fields that ApplyHostOverride modifies
// (Hosts and Validate.URL). Other fields come from the immutable embedded
// def and cannot differ between two overrides for the same provider.
func overridesMatch(a, b configprovider.ProviderDef) bool {
	if len(a.Hosts) != len(b.Hosts) {
		return false
	}
	for i := range a.Hosts {
		if a.Hosts[i] != b.Hosts[i] {
			return false
		}
	}
	aURL, bURL := "", ""
	if a.Validate != nil {
		aURL = a.Validate.URL
	}
	if b.Validate != nil {
		bURL = b.Validate.URL
	}
	return aURL == bURL
}

// grantWithOverride constructs a one-off ConfigProvider from the overridden
// def, runs its Grant flow, and saves the resulting credential. Bypasses the
// global registry so token validation hits the user's host.
func grantWithOverride(ctx context.Context, def configprovider.ProviderDef) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cp := configprovider.NewConfigProvider(def, "custom")
	provCred, err := cp.Grant(ctx)
	if err != nil {
		return err
	}
	cred := credential.Credential{
		Provider:  credential.Provider(provCred.Provider),
		Token:     provCred.Token,
		Scopes:    provCred.Scopes,
		ExpiresAt: provCred.ExpiresAt,
		CreatedAt: provCred.CreatedAt,
		Metadata:  provCred.Metadata,
	}
	credPath, err := saveCredential(cred)
	if err != nil {
		return err
	}
	if credential.ActiveProfile != "" {
		fmt.Printf("Credential saved to %s (profile: %s)\n", credPath, credential.ActiveProfile)
	} else {
		fmt.Printf("Credential saved to %s\n", credPath)
	}
	return nil
}
