package passcli

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an executable shell script standing in for pass-cli and
// returns its path.
func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pass-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatalf("writing fake pass-cli: %v", err)
	}
	return path
}

// newRunner returns a Runner on a fake pass-cli running script, with a
// dedicated HOME and a timeout generous enough for slow CI machines.
func newRunner(t *testing.T, script string) *Runner {
	t.Helper()
	return &Runner{
		Bin:     fakeCLI(t, script),
		Home:    t.TempDir(),
		Timeout: 5 * time.Second,
	}
}

// lookPath returns the absolute path of an external command. The runner
// clears PATH, so fake scripts must call external commands by absolute path.
func lookPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found: %v", name, err)
	}
	return path
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
	out, err := exec.Command(fakeCLI(t, `echo hello`)).Output()
	if err != nil {
		t.Fatalf("running fake pass-cli: %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("output = %q, want %q", out, "hello\n")
	}
}

func TestViewOK(t *testing.T) {
	want := `{"ok":true}`
	r := newRunner(t, `printf '`+want+`'`)

	got, err := r.View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if string(got) != want {
		t.Errorf("View = %q, want %q", got, want)
	}
}

func TestViewPassesExactArgs(t *testing.T) {
	r := newRunner(t, `printf '%s\n' "$@" > "$HOME/args"; printf '{}'`)
	uri := "pass://share/item/password"

	if _, err := r.View(t.Context(), uri); err != nil {
		t.Fatalf("View: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(r.Home, "args"))
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	want := "item\nview\n" + uri + "\n--output\njson\n"
	if string(got) != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestViewRejectsInvalidURIWithoutExec(t *testing.T) {
	r := newRunner(t, `: > "$HOME/ran"`)
	uri := "pass://share/item;rm -rf ~"

	if _, err := r.View(t.Context(), uri); err == nil {
		t.Fatalf("View(%q) returned nil error, want error", uri)
	}
	if _, err := os.Stat(filepath.Join(r.Home, "ran")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pass-cli was executed for invalid URI %q", uri)
	}
}

func TestViewNonZeroExit(t *testing.T) {
	r := newRunner(t, `exit 1`)

	_, err := r.View(t.Context(), "pass://share/item")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("View error = %v, want an *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
	}
}

func TestViewInvalidJSON(t *testing.T) {
	r := newRunner(t, `printf 'not json'`)

	_, err := r.View(t.Context(), "pass://share/item")
	if err == nil {
		t.Fatal("View returned nil error, want error")
	}
	// pass-cli itself succeeded: the error must come from JSON validation,
	// not from the process or the context.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("View error = %v, want a JSON validation error", err)
	}
}

func TestViewIsolatesEnvironment(t *testing.T) {
	t.Setenv("PPS_LEAK", "secret-from-parent")
	r := newRunner(t, `printf '{"leak":"%s","home":"%s","keyProvider":"%s","noUpdateCheck":"%s"}' `+
		`"$PPS_LEAK" "$HOME" "$PROTON_PASS_KEY_PROVIDER" "$PROTON_PASS_NO_UPDATE_CHECK"`)

	out, err := r.View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	var got struct{ Leak, Home, KeyProvider, NoUpdateCheck string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding fake pass-cli output: %v", err)
	}
	if got.Leak != "" {
		t.Errorf("parent env leaked into pass-cli: PPS_LEAK=%q", got.Leak)
	}
	if got.Home != r.Home {
		t.Errorf("HOME = %q, want %q", got.Home, r.Home)
	}
	if got.KeyProvider != "fs" {
		t.Errorf("PROTON_PASS_KEY_PROVIDER = %q, want %q", got.KeyProvider, "fs")
	}
	if got.NoUpdateCheck != "1" {
		t.Errorf("PROTON_PASS_NO_UPDATE_CHECK = %q, want %q", got.NoUpdateCheck, "1")
	}
}

func TestViewTimeout(t *testing.T) {
	sleep := lookPath(t, "sleep")
	// The trailing echo keeps sh alive as the parent of sleep, so sleep
	// inherits stdout and keeps it open after sh is killed.
	r := newRunner(t, sleep+" 10; echo done")
	r.Timeout = 200 * time.Millisecond

	start := time.Now()
	_, err := r.View(t.Context(), "pass://share/item")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("View error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("View returned after %v, want < 3s", elapsed)
	}
}

func TestViewCallerCancel(t *testing.T) {
	sleep := lookPath(t, "sleep")
	r := newRunner(t, sleep+" 10; echo done")

	// The caller's context expires before the runner's own timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.View(ctx, "pass://share/item")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("View error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("View returned after %v, want < 3s", elapsed)
	}
}

func TestViewOutputLimit(t *testing.T) {
	head := lookPath(t, "head")
	r := newRunner(t, head+" -c 2000000 /dev/zero")
	r.MaxOutput = 1024

	start := time.Now()
	_, err := r.View(t.Context(), "pass://share/item")
	elapsed := time.Since(start)

	// Zero bytes are not JSON either: checking err != nil alone would pass
	// even if the limit were never enforced.
	if !errors.Is(err, errOutputTooLarge) {
		t.Errorf("View error = %v, want errOutputTooLarge", err)
	}
	// Exceeding the limit must stop pass-cli, not wait for the timeout.
	if elapsed > time.Second {
		t.Errorf("View returned after %v, want < 1s", elapsed)
	}
}

func TestViewDefaultOutputLimit(t *testing.T) {
	head := lookPath(t, "head")
	size := strconv.FormatInt(int64(DefaultMaxOutput)+1, 10)
	r := newRunner(t, head+" -c "+size+" /dev/zero")
	r.MaxOutput = 0

	_, err := r.View(t.Context(), "pass://share/item")
	if !errors.Is(err, errOutputTooLarge) {
		t.Errorf("View error = %v, want errOutputTooLarge", err)
	}
}

func TestViewAtOutputLimit(t *testing.T) {
	want := `{}`
	r := newRunner(t, `printf '`+want+`'`)
	r.MaxOutput = int64(len(want))

	got, err := r.View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View with output of exactly MaxOutput bytes: %v", err)
	}
	if string(got) != want {
		t.Errorf("View = %q, want %q", got, want)
	}
}

func TestViewErrorIncludesStderrNotStdout(t *testing.T) {
	secret := "s3cret-value"
	r := newRunner(t, `echo 'item not found' >&2; printf '`+secret+`'; exit 1`)

	_, err := r.View(t.Context(), "pass://share/item")
	if err == nil {
		t.Fatal("View returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "item not found") {
		t.Errorf("error %q does not include pass-cli stderr", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error %q leaks pass-cli stdout", err)
	}
}

func TestViewLongStderrIsTruncated(t *testing.T) {
	// A single write larger than the limit: the start must be kept, the rest
	// dropped, and pass-cli must still be allowed to exit on its own.
	long := "start-of-message " + strings.Repeat("x", 10000)
	r := newRunner(t, `printf '`+long+`' >&2; exit 1`)

	_, err := r.View(t.Context(), "pass://share/item")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("View error = %v, want exit status 1", err)
	}
	if !strings.Contains(err.Error(), "start-of-message") {
		t.Errorf("error lost the start of stderr: %.80q", err)
	}
	if len(err.Error()) > 1024 {
		t.Errorf("error is %d bytes long, want stderr truncated", len(err.Error()))
	}
}

func TestViewStderrSeveralWrites(t *testing.T) {
	// Many small writes past the limit: no panic, output stays bounded.
	r := newRunner(t, `i=0; while [ $i -lt 200 ]; do echo "line $i" >&2; i=$((i+1)); done; exit 1`)

	_, err := r.View(t.Context(), "pass://share/item")
	if err == nil {
		t.Fatal("View returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "line 0") {
		t.Errorf("error lost the start of stderr: %.80q", err)
	}
	if len(err.Error()) > 1024 {
		t.Errorf("error is %d bytes long, want stderr truncated", len(err.Error()))
	}
}
