package aws

import (
	"strings"
	"testing"
)

func TestCredentialHelperClaudeArg(t *testing.T) {
	s := CredentialHelperScript
	// The script must append ?format=claude to the URL when invoked with --claude.
	if !strings.Contains(s, "--claude") {
		t.Error("helper script does not handle --claude argument")
	}
	if !strings.Contains(s, "format=claude") {
		t.Error("helper script does not append format=claude query param")
	}
}
