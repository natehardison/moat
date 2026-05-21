package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShow_DefaultBehavior(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants:
  - aws
claude:
  base_url: https://default.example
`
	project := `claude:
  base_url: https://project.example
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, false /*source*/, false /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "agent: claude") {
		t.Errorf("output missing 'agent: claude':\n%s", s)
	}
	if !strings.Contains(s, "https://project.example") {
		t.Errorf("output missing 'https://project.example':\n%s", s)
	}
}

func TestConfigShow_NoDefaultsFlag(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants: [aws]
`
	project := `agent: codex
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, false /*source*/, true /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "agent: codex") {
		t.Errorf("output should be project-only (codex):\n%s", s)
	}
	if strings.Contains(s, "aws") {
		t.Errorf("--no-defaults should suppress defaults; output contained 'aws':\n%s", s)
	}
}

func TestConfigShow_SourceFlag(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants: [aws]
claude:
  base_url: https://default.example
`
	project := `claude:
  base_url: https://project.example
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, true /*source*/, false /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "# defaults") {
		t.Errorf("--source output missing 'defaults' annotations:\n%s", s)
	}
	if !strings.Contains(s, "# project") {
		t.Errorf("--source output missing 'project' annotations:\n%s", s)
	}
}
