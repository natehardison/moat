package aws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	moatconfig "github.com/majorcontext/moat/internal/config"
)

// credentialRefreshBuffer is the time before expiration when credentials should be refreshed.
const credentialRefreshBuffer = 5 * time.Minute

// Credentials holds temporary AWS credentials.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// STSAssumeRoler interface for STS AssumeRole operation (enables testing).
type STSAssumeRoler interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// CredentialProviderConfig holds the configuration needed to create a CredentialProvider.
type CredentialProviderConfig struct {
	Source          string // "role" (default) or "profile"
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
	Profile         string // AWS shared config profile
}

// CredentialProvider manages AWS credential fetching and caching for proxy use.
// It creates an http.Handler that serves credentials in ECS container format.
type CredentialProvider struct {
	source          string // "role" or "profile"
	roleARN         string
	region          string
	sessionDuration time.Duration
	externalID      string
	sessionName     string
	authToken       string // Auth token for credential endpoint

	mu         sync.RWMutex
	cached     *Credentials
	expiration time.Time

	// stsClient is used in role mode.
	stsClient STSAssumeRoler
	// profileCreds is used in profile mode (set by NewCredentialProvider from
	// the loaded awsCfg.Credentials, which the SDK wraps in a CredentialsCache
	// that natively handles credential_process / SSO refresh).
	profileCreds awssdk.CredentialsProvider
}

// NewCredentialProvider creates a new AWS credential provider.
// If cfg.Profile is non-empty, it is used as the AWS shared config profile
// (equivalent to AWS_PROFILE) so the correct source identity is used
// for AssumeRole regardless of which process creates the provider.
func NewCredentialProvider(ctx context.Context, cfg CredentialProviderConfig, sessionName string) (*CredentialProvider, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	source := cfg.Source
	if source == "" {
		source = "role"
	}
	slog.Debug("AWS credential provider created",
		"source", source, "role_arn", cfg.RoleARN, "profile", cfg.Profile)
	return &CredentialProvider{
		source:          source,
		roleARN:         cfg.RoleARN,
		region:          cfg.Region,
		sessionDuration: cfg.SessionDuration,
		externalID:      cfg.ExternalID,
		sessionName:     sessionName,
		stsClient:       sts.NewFromConfig(awsCfg),
		profileCreds:    awsCfg.Credentials,
	}, nil
}

// SetAuthToken sets the required auth token for the credential endpoint.
func (p *CredentialProvider) SetAuthToken(token string) {
	p.authToken = token
}

// Handler returns an HTTP handler for serving credentials.
func (p *CredentialProvider) Handler() http.Handler {
	return &credentialProviderHandler{
		getCredentials: p.GetCredentials,
		authToken:      p.authToken,
		roleARN:        p.roleARN,
		source:         p.source,
	}
}

// Region returns the configured AWS region.
func (p *CredentialProvider) Region() string {
	return p.region
}

// RoleARN returns the configured IAM role ARN.
func (p *CredentialProvider) RoleARN() string {
	return p.roleARN
}

// profileCacheDefault bounds how long we cache profile-mode credentials
// when the underlying source declares CanExpire=false. Avoids both
// hot-pathing the credential_process and pinning forever.
const profileCacheDefault = 15 * time.Minute

// GetCredentials returns cached credentials or fetches new ones.
func (p *CredentialProvider) GetCredentials(ctx context.Context) (*Credentials, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	p.mu.RLock()
	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		creds := p.cached
		p.mu.RUnlock()
		return creds, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		return p.cached, nil
	}

	switch p.source {
	case "profile":
		return p.fetchFromProfile(ctx)
	default: // "role"
		return p.fetchViaAssumeRole(ctx)
	}
}

func (p *CredentialProvider) fetchViaAssumeRole(ctx context.Context) (*Credentials, error) {
	input := &sts.AssumeRoleInput{
		RoleArn:         awssdk.String(p.roleARN),
		RoleSessionName: awssdk.String(p.sessionName),
		DurationSeconds: awssdk.Int32(int32(p.sessionDuration.Seconds())),
	}
	if p.externalID != "" {
		input.ExternalId = awssdk.String(p.externalID)
	}

	result, err := p.stsClient.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assuming role %s: %w", p.roleARN, err)
	}

	if result.Credentials == nil {
		return nil, fmt.Errorf("AWS returned empty credentials for role %s", p.roleARN)
	}

	p.cached = &Credentials{
		AccessKeyID:     awssdk.ToString(result.Credentials.AccessKeyId),
		SecretAccessKey: awssdk.ToString(result.Credentials.SecretAccessKey),
		SessionToken:    awssdk.ToString(result.Credentials.SessionToken),
		Expiration:      awssdk.ToTime(result.Credentials.Expiration),
	}
	p.expiration = awssdk.ToTime(result.Credentials.Expiration)
	return p.cached, nil
}

func (p *CredentialProvider) fetchFromProfile(ctx context.Context) (*Credentials, error) {
	if p.profileCreds == nil {
		return nil, fmt.Errorf("profile credentials provider is nil")
	}
	creds, err := p.profileCreds.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving credentials from profile: %w", err)
	}
	if creds.AccessKeyID == "" {
		return nil, fmt.Errorf("profile returned empty credentials")
	}
	exp := creds.Expires
	if !creds.CanExpire || exp.IsZero() {
		// Source declares no expiration (e.g., static keys). Bound the cache
		// so we still re-Retrieve at a sensible cadence; the SDK
		// CredentialsCache will answer most re-Retrieves from its own cache
		// when the underlying provider is non-expiring.
		exp = time.Now().Add(profileCacheDefault)
	}
	p.cached = &Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expiration:      exp,
	}
	p.expiration = exp
	return p.cached, nil
}

// credentialProviderHandler serves AWS credentials via HTTP in ECS container format.
type credentialProviderHandler struct {
	getCredentials func(ctx context.Context) (*Credentials, error)
	authToken      string // Required auth token (from AWS_CONTAINER_AUTHORIZATION_TOKEN)
	roleARN        string // IAM role ARN used for error classification
	// source is "role" or "profile" — controls error-message phrasing.
	source string
}

// ServeHTTP implements http.Handler, returning credentials in ECS format.
func (h *credentialProviderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Verify auth token if required
	if h.authToken != "" {
		auth := r.Header.Get("Authorization")
		expectedAuth := "Bearer " + h.authToken
		// Use constant-time comparison to prevent timing attacks
		if auth == "" || subtle.ConstantTimeCompare([]byte(auth), []byte(expectedAuth)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	creds, err := h.getCredentials(r.Context())
	if err != nil {
		slog.Error("AWS credential fetch error", "error", err, "role", h.roleARN, "source", h.source)
		msg := classifyAWSError(err, h.roleARN, h.source)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	// AWS credential_process format
	// See: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
	resp := map[string]any{
		"Version":         1,
		"AccessKeyId":     creds.AccessKeyID,
		"SecretAccessKey": creds.SecretAccessKey,
		"SessionToken":    creds.SessionToken,
		"Expiration":      creds.Expiration.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Response already started, can't send HTTP error. Log and continue.
		slog.Warn("Failed to encode AWS credentials response", "error", err)
	}
}

// classifyAWSError returns an actionable, user-visible error message for common
// AWS credential failures. It is used by credentialProviderHandler to produce
// helpful output that the container credential helper script will display.
// source should be "role" or "profile" — it controls error-message phrasing
// for the cases that are mode-specific.
func classifyAWSError(err error, roleARN, source string) string {
	msg := err.Error()
	daemonLog := filepath.Join(moatconfig.GlobalConfigDir(), "debug", "daemon.log")

	switch {
	case strings.Contains(msg, "AccessDenied"):
		if source == "profile" {
			return fmt.Sprintf(`AWS credential error: access denied

The profile credential source rejected the request, or the resolved credentials
lack permission for the API being called.
Check that:
  1. The broker tool (credential_process) successfully issues credentials
  2. The resolved identity has the required permissions for the AWS API call

Check the daemon log: %s`, daemonLog)
		}
		// role mode (or empty for backward compat)
		arn := roleARN
		if arn == "" {
			arn = "<unknown>"
		}
		return fmt.Sprintf(`AWS credential error: access denied assuming role %s

Your host AWS identity does not have permission to assume this role.
Check that:
  1. The role's trust policy allows your AWS identity
  2. Your IAM user/role has sts:AssumeRole permission

Run 'moat grant aws' to reconfigure, or check the daemon log:
  %s`, arn, daemonLog)

	case strings.Contains(msg, "no EC2 IMDS role found") ||
		strings.Contains(msg, "failed to refresh cached credentials"):
		if source == "profile" {
			return `AWS credential error: profile yielded no credentials

The named profile's credential_process (or default chain) failed to produce credentials.
Verify on your host:
  aws --profile <name> sts get-caller-identity
If the profile uses credential_process, ensure the command is on PATH and that
any required session is active (e.g. SSO login).`
		}
		// role mode (or empty for backward compat)
		arn := roleARN
		if arn == "" {
			arn = "<unknown>"
		}
		return fmt.Sprintf(`AWS credential error: no host credentials found

The moat daemon cannot find AWS credentials to assume role %s.
The daemon runs on your host machine, not inside the container.

Ensure one of these is configured on your host:
  - AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY environment variables
  - ~/.aws/credentials file
  - AWS SSO session (run 'aws sso login')

Run 'aws sts get-caller-identity' on your host to verify.`, arn)

	case strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "ExpiredTokenException"):
		return `AWS credential error: host credentials expired

Your host AWS credentials have expired. Refresh them:
  - For SSO: aws sso login
  - For temporary credentials: re-export AWS_SESSION_TOKEN

Then retry — the daemon will pick up the new credentials automatically.`

	case strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context canceled"):
		return "AWS credential error: request canceled or timed out. Retry or check network connectivity."

	default:
		return fmt.Sprintf("AWS credential error: unexpected error fetching credentials.\n\nCheck the daemon log for details: %s", daemonLog)
	}
}
