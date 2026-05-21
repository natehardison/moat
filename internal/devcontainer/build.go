package devcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/container"
)

// ContentHash returns a stable hex SHA-256 over every file under
// <workspace>/.devcontainer/. The hash depends only on relative paths and
// file contents, so identical configs at different workspace paths share
// the same hash (and thus the same cached image tag).
func ContentHash(workspace string) (string, error) {
	dcDir := filepath.Join(workspace, ".devcontainer")
	h := sha256.New()
	h.Write([]byte("DevcontainerBase"))
	var files []string
	if err := filepath.Walk(dcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, p)
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk %s: %w", dcDir, err)
	}
	sort.Strings(files)
	for _, p := range files {
		rel, _ := filepath.Rel(dcDir, p)
		h.Write([]byte(rel))
		h.Write([]byte{0})
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// envOverlayDockerfile bakes containerEnv keys into the image as ENV lines.
func envOverlayDockerfile(baseTag string, env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", baseTag)
	for _, k := range keys {
		fmt.Fprintf(&b, "ENV %s=%q\n", k, env[k])
	}
	return b.String()
}

// BuildOptions configures BuildBase.
type BuildOptions struct {
	NoCache bool // force rebuild
}

// BuildBase resolves the devcontainer's base image (Stage A). It returns
// a deterministic, content-addressed tag like
// "moat-devcontainer-<basename>:base-<sha[:12]>".
//
// If the tag already exists locally and NoCache is false, BuildBase is a
// no-op. Otherwise it builds the image via the runtime's BuildManager.
// The image: case writes a one-line "FROM <image>" Dockerfile so the same
// BuildManager interface handles both pulls and Dockerfile builds.
func BuildBase(ctx context.Context, bm container.BuildManager, workspace string, cfg *Config, opts BuildOptions) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("devcontainer config is nil")
	}
	hash, err := ContentHash(workspace)
	if err != nil {
		return "", err
	}
	tag := fmt.Sprintf("moat-devcontainer-%s:base-%s", filepath.Base(workspace), hash[:12])

	if !opts.NoCache {
		var exists bool
		exists, err = bm.ImageExists(ctx, tag)
		if err != nil {
			return "", fmt.Errorf("checking %s: %w", tag, err)
		}
		if exists {
			return tag, nil
		}
	}

	if cfg.Build == nil {
		if cfg.Image == "" {
			return "", fmt.Errorf("devcontainer has no image or build.dockerfile")
		}
		df := fmt.Sprintf("FROM %s\n", cfg.Image)
		err = bm.BuildImage(ctx, df, tag, container.BuildOptions{NoCache: opts.NoCache})
		if err != nil {
			return "", fmt.Errorf("staging %s: %w", cfg.Image, err)
		}
		if len(cfg.ContainerEnv) > 0 {
			overlay := envOverlayDockerfile(tag, cfg.ContainerEnv)
			err = bm.BuildImage(ctx, overlay, tag, container.BuildOptions{NoCache: opts.NoCache})
			if err != nil {
				return "", fmt.Errorf("baking containerEnv: %w", err)
			}
		}
		return tag, nil
	}

	dcDir := filepath.Join(workspace, ".devcontainer")
	dfPath := filepath.Join(dcDir, cfg.Build.Dockerfile)
	dfBytes, err := os.ReadFile(dfPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dfPath, err)
	}
	bopts := container.BuildOptions{
		NoCache:   opts.NoCache,
		Target:    cfg.Build.Target,
		BuildArgs: cfg.Build.Args,
		// Always include the Dockerfile in the context so BuildImage can locate
		// it without depending on the host filesystem layout.
		ContextFiles: map[string][]byte{
			"Dockerfile": dfBytes,
		},
	}
	if err := bm.BuildImage(ctx, string(dfBytes), tag, bopts); err != nil {
		return "", fmt.Errorf("building devcontainer Dockerfile: %w", err)
	}
	if len(cfg.ContainerEnv) > 0 {
		overlay := envOverlayDockerfile(tag, cfg.ContainerEnv)
		if err := bm.BuildImage(ctx, overlay, tag, container.BuildOptions{NoCache: opts.NoCache}); err != nil {
			return "", fmt.Errorf("baking containerEnv: %w", err)
		}
	}
	return tag, nil
}
