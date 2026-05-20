// Package devcontainer parses and acts on devcontainer.json (the VS Code
// Dev Containers spec) so moat can use a workspace's devcontainer as the
// source of truth for image, user, mounts, env, and lifecycle hooks.
package devcontainer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is a parsed devcontainer.json, normalized for moat's use.
type Config struct {
	Image               string
	Build               *BuildConfig
	User                string
	Home                string
	WorkspaceFolder     string
	ContainerEnv        map[string]string
	RemoteEnv           map[string]string
	Mounts              []Mount
	InitializeCmd       string
	OnCreateCmd         string
	PostCreateCmd       string
	PostStartCmd        string
	SourcePath          string
	UpdateRemoteUserUID bool
}

// BuildConfig is the "build" subobject from devcontainer.json.
type BuildConfig struct {
	Dockerfile string            // path relative to .devcontainer/
	Context    string            // path relative to .devcontainer/; default "."
	Args       map[string]string // --build-arg key=value
	Target     string            // --target
}

// Mount is a single bind or volume mount declared in devcontainer.json.
type Mount struct {
	Source   string
	Target   string
	Type     string // "bind" or "volume"
	ReadOnly bool
}

// ErrNotFound is returned by Detect when no devcontainer.json exists.
// Callers should not treat this as an error; Detect returns (nil, nil) instead.
var ErrNotFound = errors.New("devcontainer.json not found")

// Detect returns the parsed devcontainer.json from <workspace>/.devcontainer/,
// or (nil, nil) if the file does not exist. A malformed file is a hard error.
func Detect(workspace string) (*Config, error) {
	path := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parse(path, workspace, raw)
}

// stripJSONC removes // line comments, /* block comments */, and trailing
// commas from JSONC, leaving the result valid JSON. String literals are
// preserved verbatim, including escape sequences.
func stripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	i := 0
	inString := false
	for i < len(in) {
		c := in[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(in) {
				out = append(out, in[i+1])
				i += 2
				continue
			}
			if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			i++
			continue
		}
		if c == '/' && i+1 < len(in) {
			if in[i+1] == '/' {
				for i < len(in) && in[i] != '\n' {
					i++
				}
				continue
			}
			if in[i+1] == '*' {
				i += 2
				for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
					i++
				}
				if i+1 < len(in) {
					i += 2
				} else {
					i = len(in) // unterminated comment runs to EOF
				}
				continue
			}
		}
		// Drop a trailing comma before } or ] (skipping whitespace).
		if c == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				i++
				continue
			}
		}
		out = append(out, c)
		i++
	}
	return out
}

// parse is the testable core of Detect.
func parse(path, workspace string, raw []byte) (*Config, error) {
	_ = workspace
	var top map[string]any
	if err := json.Unmarshal(stripJSONC(raw), &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg := &Config{
		SourcePath:          path,
		UpdateRemoteUserUID: true,
		ContainerEnv:        map[string]string{},
		RemoteEnv:           map[string]string{},
	}

	if v, ok := top["image"].(string); ok {
		cfg.Image = v
	}
	if rawBuild, ok := top["build"].(map[string]any); ok {
		cfg.Build = parseBuild(rawBuild)
	}
	if cfg.Image == "" && cfg.Build == nil {
		return nil, fmt.Errorf("%s: must specify either \"image\" or \"build.dockerfile\"", path)
	}

	// User: remoteUser ?? containerUser ?? "root"
	if v, ok := top["remoteUser"].(string); ok && v != "" {
		cfg.User = v
	} else if v, ok := top["containerUser"].(string); ok && v != "" {
		cfg.User = v
	} else {
		cfg.User = "root"
	}
	if cfg.User == "root" {
		cfg.Home = "/root"
	} else {
		cfg.Home = "/home/" + cfg.User
	}

	if v, ok := top["updateRemoteUserUID"].(bool); ok {
		cfg.UpdateRemoteUserUID = v
	}

	return cfg, nil
}

func parseBuild(raw map[string]any) *BuildConfig {
	df, _ := raw["dockerfile"].(string)
	if df == "" {
		return nil
	}
	bc := &BuildConfig{Dockerfile: df, Context: "."}
	if v, ok := raw["context"].(string); ok && v != "" {
		bc.Context = v
	}
	if v, ok := raw["target"].(string); ok {
		bc.Target = v
	}
	if rawArgs, ok := raw["args"].(map[string]any); ok && len(rawArgs) > 0 {
		bc.Args = make(map[string]string, len(rawArgs))
		for k, v := range rawArgs {
			bc.Args[k] = fmt.Sprint(v)
		}
	}
	return bc
}
