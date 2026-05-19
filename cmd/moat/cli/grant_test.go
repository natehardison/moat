package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/providers/configprovider"
)

func TestGrantMCP(t *testing.T) {
	// Use isolated test keyring to avoid interfering with user's real credentials
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

	// Save stdin/stdout
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	// Mock stdin with API key
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("test-api-key-123\n"))
		w.Close()
	}()

	// Redirect stdout to silence prompts
	os.Stdout, _ = os.Open(os.DevNull)

	// Set up temporary credential store
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("MOAT_HOME", "")

	// Run grant command
	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "mcp", "context7"})
	err := cmd.Execute()

	if err != nil {
		t.Fatalf("grant mcp context7 failed: %v", err)
	}

	// Verify credential was saved
	key, _ := credential.DefaultEncryptionKey()
	store, _ := credential.NewFileStore(credential.DefaultStoreDir(), key)
	cred, err := store.Get(credential.Provider("mcp-context7"))

	if err != nil {
		t.Fatalf("failed to retrieve credential: %v", err)
	}

	if cred.Provider != "mcp-context7" {
		t.Errorf("expected provider 'mcp-context7', got %q", cred.Provider)
	}

	if cred.Token != "test-api-key-123" {
		t.Errorf("expected token 'test-api-key-123', got %q", cred.Token)
	}
}

// The --host flow has two branches that are not covered at the CLI level:
// (1) existing override file + different host + stdin "y" → overwrites and grants;
// (2) existing override file + different host + non-TTY stdin → aborts with errOverrideAborted.
// Both would require either a terminal mock or an HTTP mock for the grant validate call.
// The underlying logic is covered by unit tests in configprovider/override_test.go and by
// manual smoke tests; if you change writeOverrideIfChanged, exercise these paths by hand.

func TestGrantHost_UnsupportedProvider(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "github", "--host", "github.acme.com"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --host on github (Go provider)")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want it to contain \"not supported\"", err)
	}
	if !strings.Contains(err.Error(), "gitlab") {
		t.Errorf("error = %v, want it to list eligible providers including gitlab", err)
	}
}

func TestGrantHost_InvalidHostname(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "gitlab", "--host", "https://gitlab.acme.com"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --host with scheme")
	}
	if !strings.Contains(err.Error(), "bare hostname") {
		t.Errorf("error = %v, want it to mention bare hostname", err)
	}
	// No file should be written.
	path := filepath.Join(tmp, "providers", "gitlab.yaml")
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("override file written despite invalid hostname: %s", path)
	}
}

func TestGrantHost_IdenticalFileNoOp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)
	t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

	// Pre-create an override matching what --host gitlab.acme.com would write.
	overrideDir := filepath.Join(tmp, "providers")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	overridePath := filepath.Join(overrideDir, "gitlab.yaml")

	// Build the expected contents by going through the public helpers.
	// This duplicates the production path intentionally — if these helpers
	// drift apart, this test will fail.
	def, err := configprovider.LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef: %v", err)
	}
	overridden, err := configprovider.ApplyHostOverride(def, "gitlab.acme.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	if err := configprovider.WriteUserOverride("gitlab", overridden); err != nil {
		t.Fatalf("WriteUserOverride: %v", err)
	}

	before, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("read pre-existing override: %v", err)
	}

	// Run the command. With identical existing content, we expect the
	// "no changes needed" path. The grant call after that will fail because
	// we have no token in env and stdin isn't wired — that's acceptable: the
	// test only asserts the file is unchanged.
	cmd := rootCmd
	cmd.SetArgs([]string{"grant", "gitlab", "--host", "gitlab.acme.com"})
	_ = cmd.Execute() // grant prompt will error; we don't care here

	after, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("read post-execution override: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("override file changed unexpectedly:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
