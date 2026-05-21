package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_DevcontainerDetected_OmitsBaseImageAndDeps(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatalf("write devcontainer.json: %v", err)
	}

	if err := writeDevcontainerMinimalYAML(dir, "claude"); err != nil {
		t.Fatalf("writeDevcontainerMinimalYAML: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "moat.yaml"))
	if err != nil {
		t.Fatalf("read moat.yaml: %v", err)
	}
	content := string(body)

	if strings.Contains(content, "base_image:") {
		t.Errorf("moat.yaml should NOT have base_image when devcontainer detected:\n%s", content)
	}
	if strings.Contains(content, "dependencies:") {
		t.Errorf("moat.yaml should NOT have dependencies when devcontainer detected:\n%s", content)
	}
	if !strings.Contains(content, "# .devcontainer/devcontainer.json is used as the image source") {
		t.Errorf("moat.yaml should explain the devcontainer is the source of truth:\n%s", content)
	}
	if !strings.Contains(content, "moat run --no-devcontainer") {
		t.Errorf("moat.yaml should mention --no-devcontainer to bypass:\n%s", content)
	}
	if !strings.Contains(content, "agent: claude") {
		t.Errorf("moat.yaml should include agent field:\n%s", content)
	}
}

func TestInit_WriteDevcontainerMinimalYAML_AgentName(t *testing.T) {
	for _, agentName := range []string{"claude", "codex", "gemini"} {
		t.Run(agentName, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeDevcontainerMinimalYAML(dir, agentName); err != nil {
				t.Fatalf("writeDevcontainerMinimalYAML: %v", err)
			}
			body, _ := os.ReadFile(filepath.Join(dir, "moat.yaml"))
			content := string(body)
			if !strings.Contains(content, "agent: "+agentName) {
				t.Errorf("expected agent: %s in moat.yaml:\n%s", agentName, content)
			}
			if strings.Contains(content, "base_image:") {
				t.Errorf("unexpected base_image in moat.yaml:\n%s", content)
			}
			if strings.Contains(content, "dependencies:") {
				t.Errorf("unexpected dependencies in moat.yaml:\n%s", content)
			}
		})
	}
}
