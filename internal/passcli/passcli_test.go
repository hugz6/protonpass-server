package passcli

import (
	"errors"
	"fmt"
	"io/fs"
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

func TestViewOK(t *testing.T) {
	jsonRawMsg := []byte(`{"ok":true}`)
	script := fmt.Sprintf("printf %q", jsonRawMsg)
	fakePath := fakeCLI(t, script)
	r := Runner{Bin: fakePath}
	uri := "pass://a/b"
	result, err := r.View(t.Context(), uri)
	if err != nil {
		t.Fatalf("error while calling View(): %s", err.Error())
	}
	if result.String() != string(jsonRawMsg) {
		t.Fatalf("View(%q) = %q, want %q", uri, result, jsonRawMsg)
	}
}

func TestViewPassesExactArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeCLI(t, `printf '%s\n' "$@" > '`+argsFile+`'; printf '{}'`)
	r := Runner{Bin: bin}
	uri := "pass://share/item/password"
	_, err := r.View(t.Context(), uri)
	if err != nil {
		t.Fatalf("error while calling View(): %s", err.Error())
	}
	result, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("couldn't open argsfile: %s", err.Error())
	}
	expected := "item\nview\npass://share/item/password\n--output\njson\n"
	if expected != string(result) {
		t.Fatalf("argsFile = %q, want %q", result, expected)
	}
}

func TestViewRejectsInvalidURIWithoutExec(t *testing.T) {
	witness := filepath.Join(t.TempDir(), "ran")
	bin := fakeCLI(t, `: > '`+witness+`'`)
	r := Runner{Bin: bin}
	uri := "pass://invalid/uri/bla/bla"
	_, err := r.View(t.Context(), uri)
	if err == nil {
		t.Fatalf("View(%q) dit not throw an error, we expected one", uri)
	}
	_, err = os.Stat(witness)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("View(%q) ran and wrote the witness file, we didn't expect one", uri)
	}
}

func TestViewNonZeroExit(t *testing.T) {
	bin := fakeCLI(t, `exit 1`)
	r := Runner{Bin: bin}
	uri := "pass://share/item/password"
	_, err := r.View(t.Context(), uri)
	if err == nil {
		t.Fatalf("View(%q) dit not throw an error, we expected one", uri)
	}
}

func TestViewInvalidJson(t *testing.T) {
	bin := fakeCLI(t, `printf "not json"`)
	r := Runner{Bin: bin}
	uri := "pass://share/item/password"
	_, err := r.View(t.Context(), uri)
	if err == nil {
		t.Fatalf("View(%q) dit not throw an error, we expected one", uri)
	}
}
