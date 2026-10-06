package passcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hugz6/protonpass-server/internal/protocol"
)

// DefaultMaxOutput for any pass-cli secret's is 1Mio
const (
	DefaultMaxOutput int64 = 1 << 20
	errorMaxOutput   int64 = 512
)

// errOutputTooLarge is raised when exceeding MaxOutput
var errOutputTooLarge = errors.New("pass-cli output too large")

var errEmptyPAT = errors.New("pat can't be empty")

// baseEnv is passed to pass-cli on every call, besides HOME.
var baseEnv = []string{
	// No keyring in a container: keep the session key in a file next to
	// the session database.
	"PROTON_PASS_KEY_PROVIDER=fs",
	// pass-cli is updated by rebuilding the image, never by itself.
	"PROTON_PASS_NO_UPDATE_CHECK=1",
}

// Runner struct
type Runner struct {
	Bin       string        // absolute path to pass-cli
	Home      string        // home path
	Timeout   time.Duration // timeout for pass-cli exec
	MaxOutput int64         // maximum pass-cli output
}

type stdoutBuffer struct {
	buf       bytes.Buffer
	maxOutput int64
	exceeded  bool
}

func (buff *stdoutBuffer) Write(p []byte) (n int, err error) {
	if int64(buff.buf.Len())+int64(len(p)) > buff.maxOutput {
		buff.exceeded = true
		return 0, errOutputTooLarge
	}
	return buff.buf.Write(p)
}

type stderrBuffer struct {
	buf      bytes.Buffer
	exceeded bool
}

func (buff *stderrBuffer) Write(p []byte) (n int, err error) {
	if buff.exceeded || int64(buff.buf.Len())+int64(len(p)) > errorMaxOutput {
		buff.exceeded = true
		buff.buf.Write(p[:int(errorMaxOutput)-buff.buf.Len()])
		return len(p), nil
	}
	return buff.buf.Write(p)
}

// redact replaces in s the value of each "KEY=value" entry of secretEnv with [REDACTED]
// a value cut in half by the stderr size limit is not matched
func redact(s string, secretEnv []string) string {
	for _, kv := range secretEnv {
		// An empty value would make ReplaceAll insert [REDACTED] between
		// every character.
		if _, v, _ := strings.Cut(kv, "="); v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

// run launch pass-cli with args, extraEnv is added to base env
func (r *Runner) run(ctx context.Context, extraEnv []string, args ...string) ([]byte, error) {
	// handle maxOutput
	maxOutput := r.MaxOutput
	if maxOutput == 0 {
		maxOutput = DefaultMaxOutput
	}

	// create a context with a timeout
	viewCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	// setup pass-cli exec with ctx and shell-less
	cmd := exec.CommandContext(viewCtx, r.Bin, args...)
	cmd.Env = []string{"HOME=" + r.Home}
	cmd.Env = append(cmd.Env, baseEnv...)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.WaitDelay = time.Millisecond * 500

	// create the stdout reader
	buff := &stdoutBuffer{maxOutput: maxOutput}
	cmd.Stdout = buff

	// create the stderr buffer
	buffErr := &stderrBuffer{}
	cmd.Stderr = buffErr

	// run and check for error
	err := cmd.Run()

	// check if output exceeded
	if buff.exceeded {
		return nil, fmt.Errorf("error while running pass-cli: %w", errOutputTooLarge)
	}
	if err != nil {
		// first check for WaitDelay exceeds
		ctxErr := viewCtx.Err()
		if ctxErr != nil {
			return nil, fmt.Errorf("error while running pass-cli: %w", ctxErr)
		}
		// if it's not a WaitDelay err, just return
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("error while running pass-cli: %w: %s", err, redact(buffErr.buf.String(), extraEnv))
		}
		return nil, fmt.Errorf("error while running pass-cli: %w", err)
	}

	return buff.buf.Bytes(), nil
}

func (r *Runner) View(ctx context.Context, uri string) (json.RawMessage, error) {
	// check URI
	if err := protocol.ValidateURI(uri); err != nil {
		return nil, err
	}

	out, err := r.run(ctx, nil, "item", "view", uri, "--output", "json")
	if err != nil {
		return nil, err
	}

	// with a field, pass-cli ignores --output json and prints the raw value
	// followed by a newline: wrap it so callers always get JSON. Decide on the
	// uri, not on json.Valid: a password like 123 or true is valid JSON too
	if hasField(uri) {
		value := strings.TrimSuffix(string(out), "\n")
		return json.Marshal(map[string]string{"value": value})
	}

	// without a field, the whole item comes as JSON
	if !json.Valid(out) {
		return nil, fmt.Errorf("error while validating pass-cli json output")
	}
	return out, nil
}

// hasField reports whether a valid uri is pass://SHARE/ITEM/FIELD.
func hasField(uri string) bool {
	return strings.Count(strings.TrimPrefix(uri, "pass://"), "/") == 2
}

func (r *Runner) Login(ctx context.Context, pat string) error {
	if pat == "" {
		return errEmptyPAT
	}

	_, err := r.run(ctx, []string{fmt.Sprintf("PROTON_PASS_PERSONAL_ACCESS_TOKEN=%s", pat)}, "login")
	if err != nil {
		return fmt.Errorf("error while trying to login: %w", err)
	}
	return nil
}

// Info checks that the pass-cli session is still valid.
func (r *Runner) Info(ctx context.Context) error {
	// pass-cli info fails when there is no authenticated session
	_, err := r.run(ctx, nil, "info", "--output", "json")
	if err != nil {
		return fmt.Errorf("error while checking pass-cli session: %w", err)
	}
	return nil
}

// EnsureSession reuses a valid pass-cli session, or logs in with pat. It
// reports whether the session was reused.
//
// The session lives in HOME, which outlives the container when it restarts in
// the same Pod, and pass-cli refuses a second login ("Already authenticated").
func (r *Runner) EnsureSession(ctx context.Context, pat string) (bool, error) {
	// a valid session left by a previous container: keep it
	if err := r.Info(ctx); err == nil {
		return true, nil
	}

	// an expired or broken session would block the login: drop it first. It
	// fails when there is no session at all, which is fine
	r.run(ctx, nil, "logout", "--force")

	if err := r.Login(ctx, pat); err != nil {
		return false, err
	}
	return false, nil
}
