package cli

import (
	"testing"
)

func TestRun_NoDevcontainerFlag(t *testing.T) {
	if runCmd.Flags().Lookup("no-devcontainer") == nil {
		t.Fatal("--no-devcontainer flag missing")
	}
}
