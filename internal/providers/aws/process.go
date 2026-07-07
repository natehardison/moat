package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// processTimeout bounds each credential-command execution.
	processTimeout = 30 * time.Second
	// processWaitDelay force-closes the stdout pipe after cancellation so a
	// grandchild process holding it cannot block Output() indefinitely.
	processWaitDelay = 3 * time.Second
)

// processEnvAllowlist is the environment passed to the credential command.
// The daemon's own environment (which can hold moat-internal secrets) is
// never inherited.
var processEnvAllowlist = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TZ"}

// processOutput parses both accepted output formats: AWS credential_process
// JSON (flat, with Version/AccessKeyId at top level) and the Claude Code
// awsCredentialExport envelope ({"Credentials": {...}}).
type processOutput struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
	Credentials     *struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	} `json:"Credentials"`
}

// runCredentialProcess executes command with `sh -c` and parses its stdout
// into Credentials. Already-expired output is an error so the caller's retry
// path engages instead of serving credentials the target service will reject.
func runCredentialProcess(ctx context.Context, command string) (*Credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	// command is operator-supplied via `moat grant aws --credential-process`
	// and stored encrypted; it is never sourced from moat.yaml or any
	// repository-controlled input. Running it is the feature.
	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // operator-supplied grant command, stored encrypted, never repo-controlled
	cmd.WaitDelay = processWaitDelay
	cmd.Env = allowlistedEnv()

	out, err := cmd.Output()
	if err != nil {
		// stderr makes broker failures (expired SSO session, missing tool)
		// diagnosable. stdout is never included: it may hold a credential.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("credential command failed: %w: %s", err, truncateForError(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("credential command failed: %w", err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, fmt.Errorf("credential command produced no output")
	}

	var parsed processOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("credential command output is not valid JSON (expected credential_process format or a {\"Credentials\": {...}} envelope): %w", err)
	}
	if parsed.Credentials != nil {
		parsed.AccessKeyID = parsed.Credentials.AccessKeyID
		parsed.SecretAccessKey = parsed.Credentials.SecretAccessKey
		parsed.SessionToken = parsed.Credentials.SessionToken
		parsed.Expiration = parsed.Credentials.Expiration
	}
	if parsed.AccessKeyID == "" {
		return nil, fmt.Errorf("credential command output missing AccessKeyId")
	}
	if parsed.SecretAccessKey == "" {
		return nil, fmt.Errorf("credential command output missing SecretAccessKey")
	}

	creds := &Credentials{
		AccessKeyID:     parsed.AccessKeyID,
		SecretAccessKey: parsed.SecretAccessKey,
		SessionToken:    parsed.SessionToken,
	}
	if parsed.Expiration != "" {
		exp, err := time.Parse(time.RFC3339, parsed.Expiration)
		if err != nil {
			return nil, fmt.Errorf("credential command output has invalid Expiration %q: %w", parsed.Expiration, err)
		}
		if time.Now().After(exp) {
			return nil, fmt.Errorf("credential command returned already-expired credentials (Expiration %s)", parsed.Expiration)
		}
		creds.Expiration = exp
	}
	return creds, nil
}

func allowlistedEnv() []string {
	env := make([]string, 0, len(processEnvAllowlist))
	for _, k := range processEnvAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// truncateForError bounds untrusted command stderr embedded in errors.
func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 256 {
		return s
	}
	return s[:256] + "…"
}
