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

func TestViewPassesNoExtraEnv(t *testing.T) {
	// View has no secret to pass: the token variable must not exist.
	r := newRunner(t, `printf '{"pat":"%s"}' "$PROTON_PASS_PERSONAL_ACCESS_TOKEN"`)

	out, err := r.View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if string(out) != `{"pat":""}` {
		t.Errorf("View output = %s, want no token in pass-cli env", out)
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

func TestLoginPassesTokenThroughEnv(t *testing.T) {
	r := newRunner(t, `printf '%s\n' "$@" > "$HOME/args"; `+
		`printf '%s' "$PROTON_PASS_PERSONAL_ACCESS_TOKEN" > "$HOME/pat"; `+
		`printf '%s' "$PROTON_PASS_KEY_PROVIDER" > "$HOME/keyprovider"`)
	pat := "pst_abc::def"

	if err := r.Login(t.Context(), pat); err != nil {
		t.Fatalf("Login: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(r.Home, "args"))
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	if string(args) != "login\n" {
		t.Errorf("args = %q, want only %q: the token must not be an argument", args, "login\n")
	}
	got, err := os.ReadFile(filepath.Join(r.Home, "pat"))
	if err != nil {
		t.Fatalf("reading recorded token: %v", err)
	}
	if string(got) != pat {
		t.Errorf("token in env = %q, want %q", got, pat)
	}
	// Login shares run with View: the base environment must apply too.
	keyProvider, err := os.ReadFile(filepath.Join(r.Home, "keyprovider"))
	if err != nil {
		t.Fatalf("reading recorded key provider: %v", err)
	}
	if string(keyProvider) != "fs" {
		t.Errorf("PROTON_PASS_KEY_PROVIDER = %q, want %q", keyProvider, "fs")
	}
}

func TestLoginSucceedsWithNonJSONOutput(t *testing.T) {
	// pass-cli login prints human-readable text, not JSON.
	r := newRunner(t, `echo 'Logged in successfully'`)

	if err := r.Login(t.Context(), "pst_abc::def"); err != nil {
		t.Errorf("Login: %v", err)
	}
}

func TestLoginSucceedsWithEmptyOutput(t *testing.T) {
	r := newRunner(t, `exit 0`)

	if err := r.Login(t.Context(), "pst_abc::def"); err != nil {
		t.Errorf("Login: %v", err)
	}
}

func TestLoginRejectsEmptyTokenWithoutExec(t *testing.T) {
	r := newRunner(t, `: > "$HOME/ran"`)

	if err := r.Login(t.Context(), ""); !errors.Is(err, errEmptyPAT) {
		t.Errorf("Login error = %v, want errEmptyPAT", err)
	}
	if _, err := os.Stat(filepath.Join(r.Home, "ran")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("pass-cli was executed without a token")
	}
}

func TestLoginRedactsTokenFromError(t *testing.T) {
	pat := "pst_secret::key"
	r := newRunner(t, `echo "bad token $PROTON_PASS_PERSONAL_ACCESS_TOKEN" >&2; exit 1`)

	err := r.Login(t.Context(), pat)
	if err == nil {
		t.Fatal("Login returned nil error on non-zero exit")
	}
	if strings.Contains(err.Error(), pat) {
		t.Errorf("error leaks the token: %v", err)
	}
	if !strings.Contains(err.Error(), "bad token [REDACTED]") {
		t.Errorf("error = %q, want stderr kept with the token redacted", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("Login error = %v, want it to wrap the *exec.ExitError", err)
	}
}

func TestLoginTimeout(t *testing.T) {
	sleep := lookPath(t, "sleep")
	r := newRunner(t, sleep+" 10; echo done")
	r.Timeout = 200 * time.Millisecond

	if err := r.Login(t.Context(), "pst_abc::def"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Login error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		secretEnv []string
		want      string
	}{
		{"no secret", "bad token", nil, "bad token"},
		{"one secret", "bad token abc", []string{"T=abc"}, "bad token [REDACTED]"},
		{"repeated", "abc and abc", []string{"T=abc"}, "[REDACTED] and [REDACTED]"},
		{"several secrets", "abc xyz", []string{"A=abc", "B=xyz"}, "[REDACTED] [REDACTED]"},
		{"value with =", "a=b leaked", []string{"T=a=b"}, "[REDACTED] leaked"},
		{"empty value", "unchanged", []string{"T="}, "unchanged"},
		{"no = sign", "unchanged", []string{"T"}, "unchanged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact(tt.s, tt.secretEnv); got != tt.want {
				t.Errorf("redact(%q, %q) = %q, want %q", tt.s, tt.secretEnv, got, tt.want)
			}
		})
	}
}

func TestInfoPassesExactArgs(t *testing.T) {
	r := newRunner(t, `printf '%s\n' "$@" > "$HOME/args"; printf '{}'`)

	if err := r.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(r.Home, "args"))
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	if want := "info\n--output\njson\n"; string(got) != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestInfoNoSession(t *testing.T) {
	r := newRunner(t, `echo 'Error: This operation requires an authenticated client' >&2; exit 1`)

	err := r.Info(t.Context())
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Info error = %v, want an *exec.ExitError", err)
	}
	if !strings.Contains(err.Error(), "authenticated client") {
		t.Errorf("Info error = %q, want pass-cli stderr in it", err)
	}
}

func TestViewFieldWrapsRawValue(t *testing.T) {
	// with a field, the real pass-cli prints the raw value and a newline,
	// even with --output json
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"password", `tatata\n`, "tatata"},
		{"no trailing newline", `tatata`, "tatata"},
		{"multi-line note keeps inner newlines", `line1\nline2\n`, "line1\nline2"},
		{"only the last newline is removed", `value\n\n`, "value\n"},
		{"looks like a number", `123\n`, "123"},
		{"looks like json", `{"a":1}\n`, `{"a":1}`},
		{"quotes and backslashes", `a"b\\c\n`, `a"b\c`},
		{"empty field", `\n`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRunner(t, `printf '`+tt.output+`'`)

			out, err := r.View(t.Context(), "pass://share/item/password")
			if err != nil {
				t.Fatalf("View: %v", err)
			}
			var got struct{ Value *string }
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("View output %q is not a JSON object: %v", out, err)
			}
			if got.Value == nil {
				t.Fatalf("View output %q has no value key", out)
			}
			if *got.Value != tt.want {
				t.Errorf("value = %q, want %q", *got.Value, tt.want)
			}
		})
	}
}

func TestViewWholeItemIsReturnedAsIs(t *testing.T) {
	want := `{"item":{"id":"abc"}}`
	r := newRunner(t, `printf '`+want+`'`)

	got, err := r.View(t.Context(), "pass://share/item")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if string(got) != want {
		t.Errorf("View = %q, want %q", got, want)
	}
}

// sessionCLI returns a fake pass-cli that keeps its session in $HOME/session,
// and refuses a second login like the real one. $HOME/broken makes info fail.
// rm is external: the runner clears PATH, so it needs an absolute path.
func sessionCLI(t *testing.T) string {
	return strings.ReplaceAll(sessionScript, "rm ", lookPath(t, "rm")+" ")
}

const sessionScript = `echo "$1" >> "$HOME/calls"
case "$1" in
  login)
    if [ -f "$HOME/session" ]; then echo 'Error: Already authenticated' >&2; exit 1; fi
    : > "$HOME/session" ;;
  info)
    [ -f "$HOME/session" ] && [ ! -f "$HOME/broken" ] || exit 1 ;;
  logout)
    rm -f "$HOME/session" "$HOME/broken" ;;
esac`

// calls returns the pass-cli commands run so far, one per line.
func calls(t *testing.T, r *Runner) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Home, "calls"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSession(t *testing.T) {
	tests := []struct {
		name       string
		session    bool // a session is already in HOME
		broken     bool // and it is no longer valid
		wantReused bool
		wantCalls  string
	}{
		{"no session", false, false, false, "info\nlogout\nlogin\n"},
		{"valid session after a container restart", true, false, true, "info\n"},
		{"expired session", true, true, false, "info\nlogout\nlogin\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRunner(t, sessionCLI(t))
			if tt.session {
				touch(t, filepath.Join(r.Home, "session"))
			}
			if tt.broken {
				touch(t, filepath.Join(r.Home, "broken"))
			}

			reused, err := r.EnsureSession(t.Context(), "pst_abc::def")
			if err != nil {
				t.Fatalf("EnsureSession: %v", err)
			}
			if reused != tt.wantReused {
				t.Errorf("reused = %v, want %v", reused, tt.wantReused)
			}
			if got := calls(t, r); got != tt.wantCalls {
				t.Errorf("pass-cli calls = %q, want %q", got, tt.wantCalls)
			}
			// whatever the start, the session ends up valid
			if err := r.Info(t.Context()); err != nil {
				t.Errorf("session not valid after EnsureSession: %v", err)
			}
		})
	}
}

func TestEnsureSessionTwice(t *testing.T) {
	// two broker starts in a row on the same HOME, as after a container restart
	r := newRunner(t, sessionCLI(t))

	if _, err := r.EnsureSession(t.Context(), "pst_abc::def"); err != nil {
		t.Fatalf("first EnsureSession: %v", err)
	}
	reused, err := r.EnsureSession(t.Context(), "pst_abc::def")
	if err != nil {
		t.Fatalf("second EnsureSession: %v", err)
	}
	if !reused {
		t.Error("second EnsureSession logged in again instead of reusing the session")
	}
}

func TestEnsureSessionLoginFails(t *testing.T) {
	r := newRunner(t, `case "$1" in login) echo 'bad token' >&2; exit 1 ;; info) exit 1 ;; esac`)

	if _, err := r.EnsureSession(t.Context(), "pst_abc::def"); err == nil {
		t.Error("EnsureSession returned nil error although login failed")
	}
}
