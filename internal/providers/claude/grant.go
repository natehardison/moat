package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// Grant acquires a Claude Code OAuth token interactively.
// Offers OAuth-specific options: setup-token, paste existing token, or import
// from local Claude Code installation.
func (p *OAuthProvider) Grant(ctx context.Context) (*provider.Credential, error) {
	reader := bufio.NewReader(os.Stdin)

	claudeAvailable := isClaudeAvailable()
	hasExistingCreds := hasClaudeCodeCredentials()

	for {
		fmt.Println("Choose authentication method:")
		fmt.Println()

		optNum := 1

		setupTokenOpt := 0
		if claudeAvailable {
			setupTokenOpt = optNum
			fmt.Printf("  %d. Claude subscription (OAuth token)\n", optNum)
			fmt.Println("     Runs 'claude setup-token' to get a long-lived token.")
			fmt.Println()
			optNum++
		}

		existingTokenOpt := optNum
		fmt.Printf("  %d. Existing OAuth token\n", optNum)
		fmt.Println("     Paste a token from a previous 'claude setup-token' run.")
		fmt.Println()
		optNum++

		importCredsOpt := 0
		if hasExistingCreds {
			importCredsOpt = optNum
			fmt.Printf("  %d. Import existing Claude Code credentials\n", optNum)
			fmt.Println("     Import OAuth tokens from your local Claude Code installation.")
			fmt.Println("     Note: Imported tokens are short-lived and will not auto-refresh.")
			fmt.Println()
			optNum++
		}

		maxOpt := optNum - 1
		fmt.Printf("Enter choice [1-%d]: ", maxOpt)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		if response == "" {
			response = "1"
		}

		switch response {
		case fmt.Sprint(setupTokenOpt):
			if setupTokenOpt == 0 {
				fmt.Printf("Invalid choice: %s\n", response)
				continue
			}
			return grantViaSetupToken(ctx)

		case fmt.Sprint(existingTokenOpt):
			return grantViaExistingOAuthToken(ctx)

		case fmt.Sprint(importCredsOpt):
			if importCredsOpt == 0 {
				fmt.Printf("Invalid choice: %s\n", response)
				continue
			}
			return grantViaExistingCreds(ctx)

		default:
			fmt.Printf("Invalid choice: %s\n", response)
			continue
		}
	}
}

// Grant acquires an Anthropic API key interactively.
func (p *AnthropicProvider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grantViaAPIKey(ctx)
}

// isClaudeAvailable checks if the claude CLI is installed.
func isClaudeAvailable() bool {
	cmd := exec.Command("claude", "--version")
	return cmd.Run() == nil
}

// grantViaSetupToken uses `claude setup-token` to get an OAuth token.
func grantViaSetupToken(ctx context.Context) (*provider.Credential, error) {
	fmt.Println()
	fmt.Println("Running 'claude setup-token' to obtain authentication token...")
	fmt.Println("This may open a browser for authentication.")
	fmt.Println()

	// Use a dedicated timeout for the setup-token command only.
	// This context is NOT reused for validation — see below.
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cmdCancel()

	cmd := exec.CommandContext(cmdCtx, "claude", "setup-token")
	cmd.Stdin = os.Stdin

	// Build a clean environment that forces dumb terminal mode.
	// We filter out keys we override to avoid duplicates — on Linux (glibc),
	// the first occurrence of a duplicate env var wins, so appending to
	// os.Environ() without filtering means our overrides get ignored.
	overrides := map[string]string{
		"TERM":     "dumb",
		"NO_COLOR": "1",
		"CI":       "1",
		"COLUMNS":  "10000",
	}
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if _, override := overrides[key]; !override {
			env = append(env, e)
		}
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Spawn with a PTY so Node.js uses synchronous stdout writes.
	// Without a PTY, stdout is a pipe and Node buffers writes asynchronously —
	// if the process exits (or calls process.exit()) before the buffer is
	// flushed, the token is lost. With a PTY, writes are synchronous on POSIX.
	var output strings.Builder
	ptmx, err := pty.StartWithAttrs(cmd, &pty.Winsize{Rows: 24, Cols: 10000}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting claude setup-token: %w", err)
	}

	// Copy PTY output in a goroutine; it returns when the child exits and
	// the PTY slave side closes.
	ioDone := make(chan struct{})
	go func() {
		defer close(ioDone)
		_, _ = io.Copy(&output, ptmx)
	}()

	cmdErr := cmd.Wait()
	_ = ptmx.Close()
	<-ioDone // wait for all output to be read

	log.Debug("claude setup-token completed",
		"subsystem", "grant",
		"exit_error", cmdErr,
		"output_len", output.Len(),
	)
	if log.Verbose() {
		log.Debug("claude setup-token raw output",
			"subsystem", "grant",
			"raw_output", output.String(),
		)
	}

	// Try to extract the token even if the command exited non-zero.
	// The CLI may have printed the token but then failed during cleanup
	// (e.g., writing to its own credential store).
	token := extractOAuthToken(output.String())

	if token == "" {
		const hint = "\n\nWorkaround: run 'claude setup-token' yourself, then " +
			"'moat grant claude' and choose the paste-existing-token option " +
			"(or import existing credentials)."
		if cmdErr != nil {
			return nil, fmt.Errorf("claude setup-token failed: %w%s", cmdErr, hint)
		}
		return nil, fmt.Errorf("could not extract OAuth token from claude setup-token output%s", hint)
	}

	if cmdErr != nil {
		log.Debug("claude setup-token exited non-zero but token was extracted",
			"subsystem", "grant",
			"exit_error", cmdErr,
			"token_len", len(token),
		)
	}

	log.Debug("extracted OAuth token",
		"subsystem", "grant",
		"token_len", len(token),
	)

	// Validate the token to catch corruption from ANSI parsing.
	// Use a fresh context — the command timeout context may be nearly
	// exhausted if the user took a while to authenticate in the browser.
	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token); err != nil {
		log.Error("OAuth token validation failed after extraction",
			"subsystem", "grant",
			"error", err,
			"token_len", len(token),
		)
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

	cred := &provider.Credential{
		Provider:  "claude",
		Token:     token,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude credential acquired via setup-token.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	return cred, nil
}

// grantViaExistingOAuthToken prompts the user to paste an OAuth token they
// already obtained via `claude setup-token`.
func grantViaExistingOAuthToken(ctx context.Context) (*provider.Credential, error) {
	fmt.Println()
	fmt.Println("Paste the OAuth token from a previous 'claude setup-token' run.")
	fmt.Println("The token starts with sk-ant-oat01-")
	fmt.Print("\nOAuth Token: ")

	reader := bufio.NewReader(os.Stdin)
	token, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	token = strings.TrimSpace(token)

	if token == "" {
		return nil, fmt.Errorf("OAuth token cannot be empty")
	}

	if !strings.HasPrefix(token, "sk-ant-oat") {
		return nil, fmt.Errorf("invalid token format: expected an OAuth token starting with \"sk-ant-oat\"")
	}

	// Validate the token against the API
	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token); err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

	cred := &provider.Credential{
		Provider:  "claude",
		Token:     token,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude credential acquired.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	return cred, nil
}

// oauthTokenPrefix is the public format prefix of a Claude Code OAuth token.
const oauthTokenPrefix = "sk-ant-oat01-"

// extractOAuthToken extracts the OAuth token from `claude setup-token` output.
//
// The token format is: sk-ant-oat01-<body>.
//
// The Claude CLI renders setup-token as an Ink TUI: the token is painted with
// absolute cursor-column moves inside synchronized-output frames, so the
// literal token string never appears contiguously in the captured byte stream
// (a substring/ANSI-strip approach cannot work — the prefix is split by
// \x1b[NG cursor moves and some glyphs exist only as screen positions).
//
// Instead we replay the captured PTY bytes through a virtual terminal
// emulator and read the token off the rendered screen — exactly what the user
// sees. This also handles plain, non-TUI output: a token printed as a normal
// line renders verbatim on the emulated screen.
func extractOAuthToken(output string) string {
	// Width must exceed the largest cursor column the CLI addresses (the token
	// line ends near column ~120). Height is generous so the CLI's
	// relative-cursor redraws never scroll content into lost scrollback — it
	// uses only relative vertical moves, no absolute row addressing.
	const width, height = 1024, 256

	em := vt.NewEmulator(width, height)

	// The CLI probes the terminal (Primary DA "\x1b[c", DSR, DECRQM, ...). The
	// emulator answers by writing to its input pipe, which has no reader and
	// blocks WriteString forever. We don't need the replies — this is a
	// one-shot scrape of already-captured output — so suppress every query
	// handler. Registered after NewEmulator, these run before the library
	// defaults (CSI handlers dispatch last-registered-first) and short-circuit
	// them, keeping extraction single-goroutine (no drain, no data race).
	suppress := func(ansi.Params) bool { return true }
	for _, cmd := range []int{
		'c',                         // Primary Device Attributes
		'n',                         // Device Status Report
		ansi.Command('>', 0, 'c'),   // Secondary Device Attributes
		ansi.Command('?', 0, 'n'),   // Extended Cursor Position Report
		ansi.Command(0, '$', 'p'),   // Request Mode (DECRQM, ANSI)
		ansi.Command('?', '$', 'p'), // Request Mode (DECRQM, DEC)
	} {
		em.RegisterCsiHandler(cmd, suppress)
	}

	_, _ = em.WriteString(output)
	_ = em.Close()

	screen := em.String()

	startIdx := strings.Index(screen, oauthTokenPrefix)
	if startIdx == -1 {
		log.Debug("no token prefix on rendered screen",
			"subsystem", "grant",
			"action", "extract_token",
			"screen_len", len(screen),
		)
		return ""
	}

	var token strings.Builder
	for i := startIdx; i < len(screen) && isTokenChar(screen[i]); i++ {
		token.WriteByte(screen[i])
	}
	result := token.String()

	log.Debug("token extracted from rendered screen",
		"subsystem", "grant",
		"action", "extract_token",
		"result_len", len(result),
	)

	// Validate the token looks reasonable.
	if len(result) < 60 {
		log.Debug("token too short, rejecting",
			"subsystem", "grant",
			"action", "extract_token",
			"result_len", len(result),
			"min_len", 60,
		)
		return ""
	}

	return result
}

// isTokenChar returns true if c is a valid OAuth token character.
func isTokenChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '-'
}

// grantViaAPIKey prompts for an API key.
func grantViaAPIKey(ctx context.Context) (*provider.Credential, error) {
	auth := &anthropicAuth{}

	// Get API key from environment variable or interactive prompt
	var apiKey string
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from ANTHROPIC_API_KEY environment variable")
	} else {
		var err error
		apiKey, err = auth.PromptForAPIKey()
		if err != nil {
			return nil, fmt.Errorf("reading API key: %w", err)
		}
	}

	// Validate the key
	fmt.Println("\nValidating API key...")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := auth.ValidateKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")

	cred := &provider.Credential{
		Provider:  "anthropic",
		Token:     apiKey,
		CreatedAt: time.Now(),
	}

	return cred, nil
}

// grantViaExistingCreds imports existing Claude Code credentials.
func grantViaExistingCreds(ctx context.Context) (*provider.Credential, error) {
	log.Debug("importing existing Claude Code credentials",
		"subsystem", "grant",
		"os", runtime.GOOS,
	)

	token, err := getClaudeCodeCredentials()
	if err != nil {
		log.Debug("failed to get Claude Code credentials",
			"subsystem", "grant",
			"error", err,
		)
		return nil, err
	}

	log.Debug("found Claude Code credentials",
		"subsystem", "grant",
		"access_token_len", len(token.AccessToken),
		"has_refresh_token", token.RefreshToken != "",
		"expires_at_ms", token.ExpiresAt,
		"scopes", strings.Join(token.Scopes, ","),
		"subscription_type", token.SubscriptionType,
	)

	fmt.Println()
	fmt.Println("Found Claude Code credentials.")
	fmt.Println()
	fmt.Println("  Warning: This imports your current authorization token only.")
	fmt.Println("  It is short-lived and will not be refreshed automatically.")
	fmt.Println("  For a longer-lived session, use the Claude subscription (OAuth)")
	fmt.Println("  or existing OAuth token options instead.")
	if token.SubscriptionType != "" {
		fmt.Printf("  Subscription: %s\n", token.SubscriptionType)
	}
	expiresAt := time.UnixMilli(token.ExpiresAt)
	if !expiresAt.IsZero() {
		if time.Now().After(expiresAt) {
			fmt.Printf("  Status: Expired (was valid until %s)\n", expiresAt.Format(time.RFC3339))
			fmt.Println("\nWarning: Token has expired. You may need to re-authenticate in Claude Code.")
			fmt.Println("Run 'claude' to refresh your credentials, then try again.")
			return nil, fmt.Errorf("Claude Code token has expired")
		}
		fmt.Printf("  Expires: %s\n", expiresAt.Format(time.RFC3339))
	}

	// Validate the token against the API
	fmt.Println("\nValidating OAuth token...")
	auth := &anthropicAuth{}
	validateCtx, validateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer validateCancel()

	if err := auth.ValidateOAuthToken(validateCtx, token.AccessToken); err != nil {
		log.Error("OAuth token validation failed for imported credentials",
			"subsystem", "grant",
			"error", err,
			"token_len", len(token.AccessToken),
		)
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	fmt.Println("OAuth token is valid.")

	cred := &provider.Credential{
		Provider:  "claude",
		Token:     token.AccessToken,
		Scopes:    token.Scopes,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	fmt.Println("\nClaude Code credentials imported.")
	fmt.Println("You can now run 'moat claude' to start Claude Code.")
	if !expiresAt.IsZero() {
		remaining := time.Until(expiresAt)
		if remaining > 24*time.Hour {
			fmt.Printf("Token expires in %d days. Re-run 'moat grant claude' if it expires.\n", int(remaining.Hours()/24))
		} else {
			fmt.Printf("Token expires in %.0f hours. Re-run 'moat grant claude' if it expires.\n", remaining.Hours())
		}
	}
	return cred, nil
}

// hasClaudeCodeCredentials checks if Claude Code credentials are available.
func hasClaudeCodeCredentials() bool {
	_, err := getClaudeCodeCredentials()
	return err == nil
}

// getClaudeCodeCredentials attempts to retrieve Claude Code OAuth credentials.
// It tries the following sources in order:
// 1. macOS Keychain (if on macOS)
// 2. ~/.claude/.credentials.json file
func getClaudeCodeCredentials() (*oauthToken, error) {
	// Try keychain first on macOS
	if runtime.GOOS == "darwin" {
		if token, err := getFromKeychain(); err == nil {
			log.Debug("credentials found in keychain",
				"subsystem", "grant",
				"token_len", len(token.AccessToken),
			)
			return token, nil
		}
		// Fall through to file-based lookup if keychain fails
	}

	// Try credentials file
	return getFromFile()
}

// getFromKeychain retrieves Claude Code credentials from macOS Keychain.
func getFromKeychain() (*oauthToken, error) {
	// Use the security command to retrieve the password
	cmd := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials",
		"-w", // Output only the password
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}

	// Parse the JSON credentials
	var creds oauthCredentials
	if err := json.Unmarshal(output, &creds); err != nil {
		return nil, fmt.Errorf("parsing keychain credentials: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in keychain")
	}

	return creds.ClaudeAiOauth, nil
}

// getFromFile retrieves Claude Code credentials from ~/.claude/.credentials.json.
func getFromFile() (*oauthToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Claude Code credentials not found at %s\n"+
				"  Have you logged into Claude Code? Run 'claude' to authenticate first", credPath)
		}
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	log.Debug("read credentials file",
		"subsystem", "grant",
		"path", credPath,
		"file_size", len(data),
	)
	if log.Verbose() {
		log.Debug("credentials file content",
			"subsystem", "grant",
			"raw_json", string(data),
		)
	}

	var creds oauthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials file: %w", err)
	}

	if creds.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("no OAuth credentials found in %s", credPath)
	}

	return creds.ClaudeAiOauth, nil
}
