package aws

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCredentialProcessFlatFormat(t *testing.T) {
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	cmd := `printf '{"Version":1,"AccessKeyId":"AKIAPROC01","SecretAccessKey":"sk","SessionToken":"st","Expiration":"` + exp + `"}'`

	creds, err := runCredentialProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCredentialProcess: %v", err)
	}
	if creds.AccessKeyID != "AKIAPROC01" || creds.SecretAccessKey != "sk" || creds.SessionToken != "st" {
		t.Errorf("credentials not parsed: %+v", creds)
	}
	if creds.Expiration.IsZero() {
		t.Error("Expiration not parsed")
	}
}

func TestRunCredentialProcessClaudeEnvelope(t *testing.T) {
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	cmd := `printf '{"Credentials":{"AccessKeyId":"AKIAPROC02","SecretAccessKey":"sk2","SessionToken":"st2","Expiration":"` + exp + `"}}'`

	creds, err := runCredentialProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCredentialProcess: %v", err)
	}
	if creds.AccessKeyID != "AKIAPROC02" || creds.SecretAccessKey != "sk2" || creds.SessionToken != "st2" {
		t.Errorf("claude envelope not normalized: %+v", creds)
	}
}

func TestRunCredentialProcessNoExpiration(t *testing.T) {
	cmd := `printf '{"Version":1,"AccessKeyId":"AKIAPROC03","SecretAccessKey":"sk"}'`

	creds, err := runCredentialProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCredentialProcess: %v", err)
	}
	if !creds.Expiration.IsZero() {
		t.Errorf("Expiration = %v, want zero for output without expiry", creds.Expiration)
	}
}

func TestRunCredentialProcessExpired(t *testing.T) {
	exp := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	cmd := `printf '{"Version":1,"AccessKeyId":"AKIAPROC04","SecretAccessKey":"sk","Expiration":"` + exp + `"}'`

	_, err := runCredentialProcess(context.Background(), cmd)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("want expired-credentials error, got: %v", err)
	}
}

func TestRunCredentialProcessMissingAccessKey(t *testing.T) {
	cmd := `printf '{"Version":1,"SecretAccessKey":"sk"}'`

	_, err := runCredentialProcess(context.Background(), cmd)
	if err == nil || !strings.Contains(err.Error(), "AccessKeyId") {
		t.Fatalf("want missing-AccessKeyId error, got: %v", err)
	}
}

func TestRunCredentialProcessBadJSON(t *testing.T) {
	_, err := runCredentialProcess(context.Background(), `printf 'not json'`)
	if err == nil {
		t.Fatal("want parse error for non-JSON output, got nil")
	}
}

func TestRunCredentialProcessEmptyOutput(t *testing.T) {
	_, err := runCredentialProcess(context.Background(), "true")
	if err == nil {
		t.Fatal("want error for empty output, got nil")
	}
}

func TestRunCredentialProcessCommandFails(t *testing.T) {
	_, err := runCredentialProcess(context.Background(), `echo 'broker session expired' >&2; exit 3`)
	if err == nil || !strings.Contains(err.Error(), "broker session expired") {
		t.Fatalf("error should carry command stderr, got: %v", err)
	}
}

func TestRunCredentialProcessEnvAllowlist(t *testing.T) {
	t.Setenv("MOAT_TEST_LEAK", "leaked")
	cmd := `printf '{"Version":1,"AccessKeyId":"k-%s","SecretAccessKey":"sk"}' "${MOAT_TEST_LEAK:-none}"`

	creds, err := runCredentialProcess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runCredentialProcess: %v", err)
	}
	if creds.AccessKeyID != "k-none" {
		t.Errorf("AccessKeyID = %q: daemon env must not leak into the credential command", creds.AccessKeyID)
	}
}

func TestRunCredentialProcessContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runCredentialProcess(ctx, "sleep 5; printf '{}'")
	if err == nil {
		t.Fatal("want error on context expiry, got nil")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("took %v, should abort promptly on context expiry", elapsed)
	}
}
