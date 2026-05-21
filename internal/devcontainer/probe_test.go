package devcontainer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type fakeProbeRuntime struct {
	stdouts []string // returned for each Exec call in order
	calls   int
}

func (f *fakeProbeRuntime) Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error {
	idx := f.calls
	f.calls++
	if idx < len(f.stdouts) {
		io.WriteString(stdout, f.stdouts[idx])
	}
	return nil
}

func TestProbeUserEnv_ParsesProcEnviron(t *testing.T) {
	marker := "MARK123"
	body := "PATH=/usr/local/bin:/usr/bin\x00FOO=bar\x00PWD=/should/drop\x00"
	fr := &fakeProbeRuntime{stdouts: []string{marker + body + marker}}
	// Override the mark generator for determinism.
	env, err := probeUserEnvWithMark(context.Background(), fr, "ctr", marker)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if env["PATH"] != "/usr/local/bin:/usr/bin" {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if env["FOO"] != "bar" {
		t.Errorf("FOO = %q", env["FOO"])
	}
	if _, ok := env["PWD"]; ok {
		t.Errorf("PWD should be dropped")
	}
}

func TestProbeUserEnv_DedupsPath(t *testing.T) {
	marker := "M"
	body := "PATH=/a:/b:/a:/c\x00"
	fr := &fakeProbeRuntime{stdouts: []string{marker + body + marker}}
	env, _ := probeUserEnvWithMark(context.Background(), fr, "ctr", marker)
	if env["PATH"] != "/a:/b:/c" {
		t.Errorf("PATH = %q, want /a:/b:/c", env["PATH"])
	}
}

func TestProbeUserEnv_FallsBackToPrintenv(t *testing.T) {
	marker := "M"
	// First call returns no markers (simulating /proc failure).
	// Second call returns valid printenv output with markers.
	fr := &fakeProbeRuntime{stdouts: []string{
		"",
		marker + "PATH=/bin\n" + marker,
	}}
	env, err := probeUserEnvWithMark(context.Background(), fr, "ctr", marker)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if env["PATH"] != "/bin" {
		t.Errorf("PATH = %q, want /bin", env["PATH"])
	}
}

var _ = bytes.NewBuffer // keep import
var _ = strings.Contains
