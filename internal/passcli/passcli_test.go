package passcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Helper that creates a fake cli script with the provided content.
func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	tmpDir := t.TempDir()
	tmpCliFile := filepath.Join(tmpDir, "cli.sh")
	content := []byte("#!/bin/sh\n" + script)
	// write file with exec perm
	err := os.WriteFile(tmpCliFile, content, 0o700)
	if err != nil {
		t.Fatalf("couldn't write fake cli file: %s", err.Error())
	}

	return tmpCliFile
}

func TestValidateURI(t *testing.T) {
	valid := []string{
		"pass://abc123==/def456",
		"pass://a-b_c/d-e_f==",
		"pass://share/item/username",
		"pass://share/item/My Field",
		"pass://share/item/Mot de passe éphémère",
		"pass://share/item/🔑",
	}
	invalid := []string{
		"",
		"http://share/item",
		"pass://share",
		"pass:///item",
		"pass://share//field",
		"pass://share/item/",
		"pass://share/item/   ",
		"pass://share/item/field/extra",
		"pass://sh are/item",
		"pass://../item",
		"pass://share/it;em",
		"pass://share/item/fi\nld",
		"pass://share/item/fi\x00ld",
	}

	for _, uri := range valid {
		if err := ValidateURI(uri); err != nil {
			t.Errorf("ValidateURI(%q) = %v, want nil", uri, err)
		}
	}
	for _, uri := range invalid {
		if err := ValidateURI(uri); err == nil {
			t.Errorf("ValidateURI(%q) = nil, want error", uri)
		}
	}
}

func TestFakeCLI(t *testing.T) {
	script := `echo hello`
	bin := fakeCLI(t, script)
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("error while running fakeCLI: %s", err.Error())
	}
	if string(out) != "hello\n" {
		t.Errorf("fakeCLI(%q) = %q, want %q", script, out, "hello\n")
	}
}
