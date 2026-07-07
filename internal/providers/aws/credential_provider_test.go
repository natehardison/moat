package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
)

func TestCredentialProviderHandler_ServeHTTP(t *testing.T) {
	t.Run("returns credentials in credential_process format", func(t *testing.T) {
		expiration := time.Now().Add(15 * time.Minute)
		handler := &credentialProviderHandler{
			getCredentials: func(ctx context.Context) (*Credentials, error) {
				return &Credentials{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
					SessionToken:    "FwoGZXIvYXdzEBY...",
					Expiration:      expiration,
				}, nil
			},
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}

		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp["Version"] != float64(1) {
			t.Errorf("Version = %v, want 1", resp["Version"])
		}
		if resp["AccessKeyId"] != "AKIAIOSFODNN7EXAMPLE" {
			t.Errorf("AccessKeyId = %v, want AKIAIOSFODNN7EXAMPLE", resp["AccessKeyId"])
		}
		if resp["SecretAccessKey"] != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
			t.Errorf("SecretAccessKey missing or wrong")
		}
		if resp["SessionToken"] != "FwoGZXIvYXdzEBY..." {
			t.Errorf("SessionToken = %v, want FwoGZXIvYXdzEBY...", resp["SessionToken"])
		}
		if _, ok := resp["Expiration"]; !ok {
			t.Error("Expiration missing from response")
		}
	})

	t.Run("returns 500 on provider error", func(t *testing.T) {
		handler := &credentialProviderHandler{
			getCredentials: func(ctx context.Context) (*Credentials, error) {
				return nil, context.DeadlineExceeded
			},
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})

	t.Run("returns 401 when auth token required but missing", func(t *testing.T) {
		handler := &credentialProviderHandler{
			getCredentials: func(ctx context.Context) (*Credentials, error) {
				return &Credentials{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "secret",
					SessionToken:    "token",
					Expiration:      time.Now().Add(15 * time.Minute),
				}, nil
			},
			authToken: "secret-token",
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("returns 401 when auth token is invalid", func(t *testing.T) {
		handler := &credentialProviderHandler{
			getCredentials: func(ctx context.Context) (*Credentials, error) {
				return &Credentials{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "secret",
					SessionToken:    "token",
					Expiration:      time.Now().Add(15 * time.Minute),
				}, nil
			},
			authToken: "secret-token",
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("returns credentials when auth token is valid", func(t *testing.T) {
		handler := &credentialProviderHandler{
			getCredentials: func(ctx context.Context) (*Credentials, error) {
				return &Credentials{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "secret",
					SessionToken:    "token",
					Expiration:      time.Now().Add(15 * time.Minute),
				}, nil
			},
			authToken: "secret-token",
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}

		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp["AccessKeyId"] != "AKIAIOSFODNN7EXAMPLE" {
			t.Errorf("AccessKeyId = %v, want AKIAIOSFODNN7EXAMPLE", resp["AccessKeyId"])
		}
	})
}

func TestCredentialProvider_Caching(t *testing.T) {
	callCount := 0
	expiration := time.Now().Add(15 * time.Minute)

	mockSTS := &mockSTSClient{
		assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			callCount++
			return &sts.AssumeRoleOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     awssdk.String("AKIA" + fmt.Sprintf("%d", callCount)),
					SecretAccessKey: awssdk.String("secret"),
					SessionToken:    awssdk.String("token"),
					Expiration:      awssdk.Time(expiration),
				},
			}, nil
		},
	}

	provider := &CredentialProvider{
		roleARN:         "arn:aws:iam::123456789012:role/Test",
		region:          "us-east-1",
		sessionDuration: 15 * time.Minute,
		sessionName:     "test",
		stsClient:       mockSTS,
	}

	// First call should hit STS
	creds1, err := provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should use cache
	creds2, err := provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (cached)", callCount)
	}

	// Should return same credentials
	if creds1.AccessKeyID != creds2.AccessKeyID {
		t.Errorf("cached credentials should match")
	}
}

func TestCredentialProvider_RefreshesExpiredCredentials(t *testing.T) {
	callCount := 0

	mockSTS := &mockSTSClient{
		assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			callCount++
			// Return credentials that expire soon (within 5 min buffer)
			expiration := time.Now().Add(3 * time.Minute)
			return &sts.AssumeRoleOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     awssdk.String("AKIA" + fmt.Sprintf("%d", callCount)),
					SecretAccessKey: awssdk.String("secret"),
					SessionToken:    awssdk.String("token"),
					Expiration:      awssdk.Time(expiration),
				},
			}, nil
		},
	}

	provider := &CredentialProvider{
		roleARN:         "arn:aws:iam::123456789012:role/Test",
		region:          "us-east-1",
		sessionDuration: 15 * time.Minute,
		sessionName:     "test",
		stsClient:       mockSTS,
	}

	// First call
	_, err := provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should refresh because credentials expire within 5 min
	_, err = provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (should refresh near-expiry credentials)", callCount)
	}
}

// mockSTSClient is defined in provider_test.go — reused here.

func TestCredentialProviderHandlerClaudeFormat(t *testing.T) {
	handler := &credentialProviderHandler{
		getCredentials: func(ctx context.Context) (*Credentials, error) {
			return &Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				SessionToken:    "FwoGZXIvYXdzEBY...",
				Expiration:      time.Now().Add(time.Hour),
			}, nil
		},
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/_aws/credentials?format=claude", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Credentials struct {
			AccessKeyID     string `json:"AccessKeyId"`
			SecretAccessKey string
			SessionToken    string
			Expiration      string
		}
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Credentials.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" || got.Credentials.SecretAccessKey == "" || got.Credentials.SessionToken == "" {
		t.Errorf("claude envelope missing credentials: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.Credentials.Expiration); err != nil {
		t.Errorf("claude envelope Expiration = %q, want RFC 3339 (drives Claude Code's refresh cadence): %v", got.Credentials.Expiration, err)
	}
}

func TestCredentialProviderHandlerUnknownFormatFallsBack(t *testing.T) {
	// Companion: an unrecognized format value serves the default
	// credential_process shape rather than erroring.
	handler := &credentialProviderHandler{
		getCredentials: func(ctx context.Context) (*Credentials, error) {
			return &Credentials{AccessKeyID: "AKIA", SecretAccessKey: "s", SessionToken: "t", Expiration: time.Now().Add(time.Hour)}, nil
		},
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/_aws/credentials?format=bogus", nil))
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["Version"] != float64(1) || resp["AccessKeyId"] != "AKIA" {
		t.Errorf("default credential_process shape not served for unknown format: %v", resp)
	}
}

func TestCredentialProvider_Region(t *testing.T) {
	p := &CredentialProvider{region: "ap-southeast-2"}
	if got := p.Region(); got != "ap-southeast-2" {
		t.Errorf("Region() = %q, want ap-southeast-2", got)
	}
}

func TestCredentialProvider_RoleARN(t *testing.T) {
	p := &CredentialProvider{roleARN: "arn:aws:iam::123456789012:role/MyRole"}
	if got := p.RoleARN(); got != "arn:aws:iam::123456789012:role/MyRole" {
		t.Errorf("RoleARN() = %q, want the configured ARN", got)
	}
}

func TestCredentialProviderProfileModeSkipsAssumeRole(t *testing.T) {
	// In profile mode, GetCredentials must serve from the AWS SDK credentials
	// provider directly and MUST NOT call sts:AssumeRole.
	failOnAssumeRole := &assumeRoleShouldNotBeCalled{t: t}
	fakeExpires := time.Now().Add(30 * time.Minute)

	p := &CredentialProvider{
		source:          "profile",
		region:          "us-west-2",
		sessionDuration: 15 * time.Minute,
		stsClient:       failOnAssumeRole, // fails the test if invoked
		profileCreds: staticCredentialsProvider{
			creds: awssdk.Credentials{
				AccessKeyID:     "AKIDPROFILE",
				SecretAccessKey: "SECRET",
				SessionToken:    "TOKEN",
				Expires:         fakeExpires,
				CanExpire:       true,
			},
		},
	}

	got, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.AccessKeyID != "AKIDPROFILE" {
		t.Errorf("AccessKeyID = %q, want AKIDPROFILE", got.AccessKeyID)
	}
	if got.SessionToken != "TOKEN" {
		t.Errorf("SessionToken = %q, want TOKEN", got.SessionToken)
	}
	if !got.Expiration.Equal(fakeExpires) {
		t.Errorf("Expiration = %v, want %v", got.Expiration, fakeExpires)
	}
}

func TestCredentialProviderProfileModeHandlesNonExpiringSource(t *testing.T) {
	// If the underlying source returns CanExpire=false (e.g., static keys),
	// the provider must still set a finite cached expiration (defensive
	// refresh window) so it re-Retrieves at a sensible cadence.
	p := &CredentialProvider{
		source:          "profile",
		region:          "us-west-2",
		sessionDuration: 15 * time.Minute,
		stsClient:       &assumeRoleShouldNotBeCalled{t: t},
		profileCreds: staticCredentialsProvider{
			creds: awssdk.Credentials{
				AccessKeyID:     "AKIDSTATIC",
				SecretAccessKey: "SECRET",
				CanExpire:       false, // perpetual
			},
		},
	}
	got, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.AccessKeyID != "AKIDSTATIC" {
		t.Errorf("AccessKeyID = %q, want AKIDSTATIC", got.AccessKeyID)
	}
	// Verify the provider chose a non-zero, finite expiration to drive refresh.
	if got.Expiration.IsZero() || got.Expiration.After(time.Now().Add(time.Hour)) {
		t.Errorf("Expiration = %v, want a finite near-future time (defensive refresh window)", got.Expiration)
	}
}

func TestClassifyAWSError_ProfileMode(t *testing.T) {
	err := fmt.Errorf("AccessDenied: cannot perform operation")
	msg := classifyAWSError(err, "" /*roleARN*/, "profile")
	if strings.Contains(msg, "assuming role") {
		t.Errorf("profile-mode AccessDenied message must not mention role assumption: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "access denied") {
		t.Errorf("profile-mode AccessDenied message should still mention access denied: %s", msg)
	}
}

func TestClassifyAWSError_RoleMode(t *testing.T) {
	err := fmt.Errorf("AccessDenied: cannot perform operation")
	msg := classifyAWSError(err, "arn:aws:iam::123:role/X", "role")
	if !strings.Contains(msg, "arn:aws:iam::123:role/X") {
		t.Errorf("role-mode AccessDenied message must mention the role ARN: %s", msg)
	}
}

func TestClassifyAWSError_NoIMDS_ProfileMode(t *testing.T) {
	err := fmt.Errorf("failed to refresh cached credentials, no EC2 IMDS role found")
	msg := classifyAWSError(err, "", "profile")
	if strings.Contains(msg, "assume role") {
		t.Errorf("profile-mode no-creds message must not mention role assumption: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "profile") {
		t.Errorf("profile-mode no-creds message should mention 'profile': %s", msg)
	}
}

// mockSTSClient implements STSAssumeRoler for testing.
type mockSTSClient struct {
	assumeRoleFn func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

func (m *mockSTSClient) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.assumeRoleFn(ctx, params, optFns...)
}

// assumeRoleShouldNotBeCalled is an STSAssumeRoler that fails the test if invoked.
type assumeRoleShouldNotBeCalled struct{ t *testing.T }

func (a *assumeRoleShouldNotBeCalled) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	a.t.Fatal("AssumeRole must not be called in profile mode")
	return nil, nil
}

// staticCredentialsProvider implements awssdk.CredentialsProvider for tests.
type staticCredentialsProvider struct {
	creds awssdk.Credentials
}

func (s staticCredentialsProvider) Retrieve(_ context.Context) (awssdk.Credentials, error) {
	return s.creds, nil
}

func TestCredentialProviderProcessMode(t *testing.T) {
	// The counter file proves the command runs exactly once across two
	// GetCredentials calls: the second is served from cache.
	counter := filepath.Join(t.TempDir(), "count")
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	cmd := `echo x >> ` + counter + `; printf '{"Version":1,"AccessKeyId":"AKIAPM01","SecretAccessKey":"sk","SessionToken":"st","Expiration":"` + exp + `"}'`

	p, err := NewCredentialProvider(context.Background(), CredentialProviderConfig{
		Source:  "process",
		Command: cmd,
		Region:  "us-west-2",
	}, "moat-test")
	if err != nil {
		t.Fatalf("NewCredentialProvider: %v", err)
	}

	creds, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if creds.AccessKeyID != "AKIAPM01" {
		t.Errorf("AccessKeyID = %q", creds.AccessKeyID)
	}
	if _, err := p.GetCredentials(context.Background()); err != nil {
		t.Fatalf("second GetCredentials: %v", err)
	}
	data, _ := os.ReadFile(counter)
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Errorf("command ran %d times, want 1 (second call must be cached)", got)
	}
}

func TestCredentialProviderProcessModeNoExpiryBounded(t *testing.T) {
	cmd := `printf '{"Version":1,"AccessKeyId":"AKIAPM02","SecretAccessKey":"sk"}'`
	p, err := NewCredentialProvider(context.Background(), CredentialProviderConfig{
		Source: "process", Command: cmd, Region: "us-west-2",
	}, "moat-test")
	if err != nil {
		t.Fatalf("NewCredentialProvider: %v", err)
	}
	creds, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	until := time.Until(creds.Expiration)
	if until <= 0 || until > profileCacheDefault+time.Minute {
		t.Errorf("no-expiry output should be cache-bounded to ~%v, got %v", profileCacheDefault, until)
	}
}

func TestCredentialProviderProcessModeNegativeCache(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "count")
	cmd := `echo x >> ` + counter + `; echo 'broker down' >&2; exit 1`
	p, err := NewCredentialProvider(context.Background(), CredentialProviderConfig{
		Source: "process", Command: cmd, Region: "us-west-2",
	}, "moat-test")
	if err != nil {
		t.Fatalf("NewCredentialProvider: %v", err)
	}

	if _, err := p.GetCredentials(context.Background()); err == nil {
		t.Fatal("want error from failing command")
	}
	if _, err := p.GetCredentials(context.Background()); err == nil {
		t.Fatal("want cached error on immediate retry")
	}
	data, _ := os.ReadFile(counter)
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Errorf("command ran %d times, want 1 (failure must be negative-cached)", got)
	}
}
