package devcontainer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ProbeUserEnv runs the user's login shell inside the container and
// returns the resulting environment. This is needed because lifecycle
// hooks must run with PATH, locale, etc. set by /etc/profile, conda
// init, nvm, etc. — exec-style invocations don't get that for free.
//
// Probe strategy: print a UUID marker, dump /proc/self/environ
// (null-separated) between markers, print the marker again. Fall back
// to `printenv` (newline-separated) when /proc fails.
func ProbeUserEnv(ctx context.Context, rt ExecRuntime, containerID, user string) (map[string]string, error) {
	return probeUserEnvWithMark(ctx, rt, containerID, user, newMark())
}

func probeUserEnvWithMark(ctx context.Context, rt ExecRuntime, containerID, user, mark string) (map[string]string, error) {
	env, err := probeWith(ctx, rt, containerID, user, mark, "cat /proc/self/environ", "\x00")
	if err == nil && env != nil {
		return finishEnv(env), nil
	}
	env, err = probeWith(ctx, rt, containerID, user, mark, "printenv", "\n")
	if err != nil {
		return nil, err
	}
	if env == nil {
		return map[string]string{}, nil
	}
	return finishEnv(env), nil
}

func probeWith(ctx context.Context, rt ExecRuntime, containerID, user, mark, cmd, sep string) (map[string]string, error) {
	inner := fmt.Sprintf("echo -n %s; %s; echo -n %s", mark, cmd, mark)
	args := []string{"/bin/sh", "-lc", inner}
	var out bytes.Buffer
	if err := rt.Exec(ctx, containerID, args, nil, &out, io.Discard); err != nil {
		return nil, nil // try fallback
	}
	raw := out.String()
	start := strings.Index(raw, mark)
	end := strings.LastIndex(raw, mark)
	if start == -1 || end == -1 || end == start {
		return nil, nil
	}
	body := raw[start+len(mark) : end]
	if body == "" {
		return nil, nil
	}
	env := map[string]string{}
	for _, entry := range strings.Split(body, sep) {
		if i := strings.Index(entry, "="); i != -1 {
			env[entry[:i]] = entry[i+1:]
		}
	}
	return env, nil
}

func finishEnv(env map[string]string) map[string]string {
	delete(env, "PWD")
	delete(env, "SHLVL")
	delete(env, "_")
	if p, ok := env["PATH"]; ok {
		env["PATH"] = dedupPath(p)
	}
	return env
}

func dedupPath(p string) string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, part := range strings.Split(p, ":") {
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return strings.Join(out, ":")
}

func newMark() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
