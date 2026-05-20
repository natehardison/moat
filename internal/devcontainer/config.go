// Package devcontainer parses and acts on devcontainer.json (the VS Code
// Dev Containers spec) so moat can use a workspace's devcontainer as the
// source of truth for image, user, mounts, env, and lifecycle hooks.
package devcontainer

import (
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

// parse is the testable core of Detect.
func parse(path, workspace string, raw []byte) (*Config, error) {
	// Stub — wired up in Task 1.3.
	_ = workspace
	_ = raw
	return &Config{
		Image:               "ubuntu:24.04",
		User:                "root",
		Home:                "/root",
		UpdateRemoteUserUID: true,
		SourcePath:          path,
	}, nil
}
