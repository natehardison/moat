package run

// Tests for the AWS grant gate in manager.Create.
//
// Why approach (B) rather than (A):
// manager.Create requires a live Unix socket to the proxy daemon, a real
// container runtime, and several other external dependencies. Constructing a
// fully-stubbed Manager for unit testing would require significant scaffolding
// that does not exist in this package today (as evidenced by the rest of
// manager_test.go, which tests logic extracted into package-level helpers
// rather than calling Create directly).
//
// Instead we test the predicate that gates the AWS credential-provider setup
// block in Create. The gate previously used provider.GetEndpoint(string(credName)),
// which silently returned nil after Task 5 removed EndpointProvider from
// aws.Provider. It has been replaced with a direct constant comparison:
//
//	if credName == credential.ProviderAWS { ... }
//
// The tests below verify the predicate's truth table and act as a canary: if
// credential.ProviderAWS is ever renamed or its value changed, the tests break
// and a developer is forced to update the gate consciously.

import (
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

// isAWSCredential reports whether credName identifies an AWS grant.
// This mirrors the gate condition in manager.Create so it can be tested
// directly without standing up a full manager.
func isAWSCredential(credName credential.Provider) bool {
	return credName == credential.ProviderAWS
}

func TestIsAWSCredential(t *testing.T) {
	tests := []struct {
		name     string
		credName credential.Provider
		want     bool
	}{
		{"aws constant matches", credential.ProviderAWS, true},
		{"anthropic does not match", credential.ProviderAnthropic, false},
		{"claude does not match", credential.ProviderClaude, false},
		{"empty string does not match", "", false},
		{"literal aws string matches constant value", credential.Provider("aws"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAWSCredential(tt.credName)
			if got != tt.want {
				t.Errorf("isAWSCredential(%q) = %v, want %v", tt.credName, got, tt.want)
			}
		})
	}
}

// TestProviderAWSConstantNonEmpty is a smoke test ensuring that credential.ProviderAWS
// is a non-empty string. A future refactor that renames or zeros the constant will
// break this test, forcing a conscious update of the gate in manager.Create.
func TestProviderAWSConstantNonEmpty(t *testing.T) {
	if credential.ProviderAWS == "" {
		t.Fatal("credential.ProviderAWS must be a non-empty string; the AWS grant gate in manager.Create depends on it")
	}
}
