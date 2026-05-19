# Robust OAuth token extraction from `claude setup-token` (VT emulation)

Date: 2026-05-19
Status: accepted

## Problem

`moat grant claude` → setup-token option fails 100% of the time with "could not
find OAuth token in claude setup-token output". Users fall back to running
`claude setup-token` manually and pasting.

## Root cause (evidence-based)

Claude CLI v2.1.144 ignores moat's `TERM=dumb`/`CI=1`/`NO_COLOR` overrides and
renders `setup-token` as a full Ink TUI: synchronized-output frames
(`\x1b[?2026h/l`), absolute cursor positioning (`\x1b[NG`), spinner, ASCII-art
logo.

The token is painted with absolute column moves, not linear text. Captured PTY
bytes for the token line look like:

```
\r\x1b[1C\x1b[2Bsk-ant-\x1b[10Gat01-<body>\r\r
```

So the literal substring `sk-ant-oat01-` is **not in the byte stream**:
- `sk-ant-` and `at01-` are separated by `\x1b[10G` (jump to column 10).
- The `o` of `oat01` is never a byte at all — column 9 is painted in a
  different render frame; it exists only as a screen position.

`extractOAuthToken`'s strategy (`strings.Index(output, "sk-ant-oat01-")` then
strip ANSI) cannot work in principle against TUI output. This is a wrong
approach, not a tweakable bug. The function has no test coverage.

`claude setup-token` exposes no non-TUI flag, and the PTY cannot be dropped
(grant.go documents Node stdout buffering loses the token without it). So
"eliminate the TUI" is not available.

## Decision

Option 4: VT emulation primary + graceful fallback.

1. Replace `extractOAuthToken`'s scrape with: feed captured PTY bytes through
   `github.com/charmbracelet/x/vt` (already a direct dependency), render to a
   virtual screen, scan rendered rows for `sk-ant-oat01-`, collect the
   contiguous token-char run.
2. Emulate with generous rows (Ink uses only relative vertical moves + `\x1b[K`,
   no absolute row addressing) so the token never scrolls into lost scrollback.
3. Keep the trivial substring scrape as a fallback (older Claude that prints
   plainly).
4. Improve the failure message at grant.go:186 to direct users to the
   already-working paste / import-existing-creds options.
5. Existing API validation (grant.go:210) remains the correctness backstop.

## Testing

TDD against a real captured byte stream. The capture contains a live token, so
the committed fixture is produced by a local sanitizer that replaces the secret
body with deterministic synthetic characters while preserving exact byte layout,
all escape sequences, and the public `…oat01-` format prefix. The synthetic
token's value is irrelevant; the regression assertion is: old code returns ""
on this fixture, new code returns a well-formed `sk-ant-oat01-` token.

## Follow-up (not blocking)

Verify whether `claude setup-token` persists to `~/.claude/.credentials.json` /
keychain. If it does, reading that post-run (like `grantViaExistingCreds`) would
make extraction structurally immune to TUI rendering — a future simplification.
