package devcontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
