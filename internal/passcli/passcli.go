package passcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// DefaultMaxOutput for any pass-cli secret's is 1Mio
const (
	DefaultMaxOutput int64 = 1 << 20
	errorMaxOutput   int64 = 512
)

// errOutputTooLarge is raised when exceeding MaxOutput
var errOutputTooLarge = errors.New("pass-cli output too large")

// regex for SHARE and ITEM
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]+$`)

// Runner struct
type Runner struct {
	Bin       string        // absolute path to pass-cli
	Home      string        // home path
	Timeout   time.Duration // timeout for pass-cli exec
	MaxOutput int64         // maximum pass-cli output
}

func ValidateURI(uri string) error {
	// check if prefix is valid
	uri, validPrefix := strings.CutPrefix(uri, "pass://")
	if !validPrefix {
		return errors.New("prefix should start with pass://")
	}

	// splits parts with /
	parts := strings.Split(uri, "/")
	partsCount := len(parts)
	if partsCount <= 1 || partsCount > 3 {
		return errors.New("uri must be pass://SHARE/ITEM[/FIELD]")
	}
	share := parts[0]
	item := parts[1]

	// ensure field contains only printable character and is not empty
	if partsCount > 2 {
		if strings.TrimSpace(parts[2]) == "" {
			return errors.New("field cannot be empty")
		}
		if strings.ContainsFunc(parts[2], func(r rune) bool { return !unicode.IsPrint(r) }) {
			return errors.New("field must contains only printable char")
		}
	}

	// ensure share and item are matching the proton regexp
	if !idPattern.MatchString(share) {
		return errors.New("share doesn't match the proton regex")
	}
	if !idPattern.MatchString(item) {
		return errors.New("item doesn't match the proton regex")
	}

	return nil
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

func (r *Runner) View(ctx context.Context, uri string) (json.RawMessage, error) {
	// handle maxOutput
	maxOutput := r.MaxOutput
	if maxOutput == 0 {
		maxOutput = DefaultMaxOutput
	}

	// create a context with a timeout
	viewCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	// check URI
	if err := ValidateURI(uri); err != nil {
		return nil, err
	}

	// setup pass-cli exec with ctx and shell-less
	cmd := exec.CommandContext(viewCtx, r.Bin, "item", "view", uri, "--output", "json")
	cmd.Env = []string{"HOME=" + r.Home}
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
			return nil, fmt.Errorf("error while running pass-cli: %w: %s", err, buffErr.buf.String())
		}
		return nil, fmt.Errorf("error while running pass-cli: %w", err)
	}

	// read buffer into out
	out := buff.buf.Bytes()

	// check if output is valid json
	if isValid := json.Valid(out); !isValid {
		return nil, fmt.Errorf("error while validating pass-cli json output")
	}
	return out, nil
}
