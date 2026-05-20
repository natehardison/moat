package deps

import "testing"

func TestNeedsCustomImage_RemapUser(t *testing.T) {
	s := &ImageSpec{RemapUser: "vscode", RemapUID: 1000, RemapGID: 1000}
	if !s.NeedsCustomImage(false) {
		t.Error("RemapUser should trigger NeedsCustomImage")
	}
}
