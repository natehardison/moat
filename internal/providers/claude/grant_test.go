package claude

import (
	"os"
	"strings"
	"testing"
)

// TestExtractOAuthToken_ClaudeTUIFixture is the regression test for the bug
// where `moat grant claude` (setup-token option) failed with "could not find
// OAuth token in claude setup-token output".
//
// The fixture is a sanitized capture of real Claude CLI v2.1.144
// `claude setup-token` PTY output. The CLI renders an Ink TUI: the token is
// painted with absolute cursor-column moves, so the literal substring
// "sk-ant-oat01-" never appears in the byte stream and the old
// strings.Index-based extractor returned "". The token only exists on the
// rendered screen.
func TestExtractOAuthToken_ClaudeTUIFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/setup_token_tui_v2_1_144.bin")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	// The fixture's secret body was replaced with a deterministic synthetic
	// string before it was committed, so this exact value is safe to assert
	// and is non-secret.
	const want = "sk-ant-oat01-vqlgb61wr-hc72xsnid83y-oje94zupkfa50vqlgb61-rmhc72xsnid83ytoje94zupkfa50vqlgb61wrmhc72-snid83yt"

	got := extractOAuthToken(string(raw))

	if got == "" {
		t.Fatalf("extractOAuthToken returned empty; the token is only on the rendered screen, not in the raw byte stream")
	}
	if got != want {
		t.Errorf("extractOAuthToken() =\n  %q\nwant\n  %q", got, want)
	}
	if len(got) != 108 {
		t.Errorf("token length = %d, want 108", len(got))
	}
}

// TestExtractOAuthToken_PlainText ensures the extractor still handles the
// simple case where the token is printed as a plain line (older Claude CLI or
// non-TUI output) — feeding it through the emulator must render it verbatim.
func TestExtractOAuthToken_PlainText(t *testing.T) {
	body := strings.Repeat("a", 95)
	want := "sk-ant-oat01-" + body
	out := "Your OAuth token (valid for 1 year):\r\n" + want + "\r\n\r\nStore this token securely.\r\n"

	got := extractOAuthToken(out)

	if got != want {
		t.Errorf("extractOAuthToken() = %q, want %q", got, want)
	}
}

// TestExtractOAuthToken_NoToken returns empty when no token is present.
func TestExtractOAuthToken_NoToken(t *testing.T) {
	if got := extractOAuthToken("no token here\r\njust some output\r\n"); got != "" {
		t.Errorf("extractOAuthToken() = %q, want empty", got)
	}
}
