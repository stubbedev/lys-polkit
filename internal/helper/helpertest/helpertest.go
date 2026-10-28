// Package helpertest is a fake polkit-agent-helper-1 for tests: the
// test binary itself, run under a name that picks a scenario, speaking
// the helper's protocol on stdin and stdout as the setuid program or,
// accepted on a socket, as the socket-activated service.
package helpertest

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/sockdir"

	"github.com/stubbedev/lys-polkit/internal/helper"
)

// The conversation the fake expects.
const (
	User     = "alice"
	Cookie   = "cookie-1"
	Password = "hunter2"
)

// Scenario is one conversation the fake holds.
type Scenario string

// The fake's scenarios.
const (
	// Answer prompts for the password and reports SUCCESS for Password,
	// FAILURE for anything else. Every scenario's verdict is FAILURE for
	// a user or cookie other than User and Cookie.
	Answer Scenario = "answer"
	// TwoPrompts asks for the user name, echoed, then the password.
	TwoPrompts Scenario = "two-prompts"
	// InfoOnly says to touch a fingerprint reader, then SUCCESS.
	InfoOnly Scenario = "info-only"
	// HangUp prompts, reads the answer and exits with status 3 without
	// a verdict.
	HangUp Scenario = "hang-up"
	// Wait prompts and then waits until it is stopped.
	Wait Scenario = "wait"
	// InfoWait says to touch a fingerprint reader and waits until it is
	// stopped.
	InfoWait Scenario = "info-wait"
)

// The texts the fake shows.
const (
	PasswordPrompt = "Password: "
	UserPrompt     = "Username: "
	FingerInfo     = "Place your finger on the \"reader\"\n\tor tap — ✋"
	SorryError     = "Sorry, try again."
	HangUpStatus   = 3
)

const (
	namePrefix     = "fake-polkit-agent-helper-"
	socketArg      = "--socket-activated"
	pidSuffix      = ".pid"
	reapTimeout    = 5 * time.Second
	reapPollPeriod = 10 * time.Millisecond
)

// Fake is one scenario's helper, as a program and as a socket.
type Fake struct {
	dir     string
	program string
}

// New prepares a fake for the scenario: a link to the test binary
// under the scenario's name, in a directory that also records each
// helper process started.
func New(t testing.TB, s Scenario) *Fake {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("helpertest: test binary: %v", err)
	}
	program := filepath.Join(dir, namePrefix+string(s))
	if err := os.Symlink(self, program); err != nil {
		t.Fatalf("helpertest: %v", err)
	}
	return &Fake{dir: dir, program: program}
}

// Setuid is the transport that runs the fake as the setuid helper: no
// socket exists.
func (f *Fake) Setuid() helper.Transport {
	return helper.Transport{Socket: filepath.Join(f.dir, "no-socket"), Program: f.program}
}

// Socket is the transport that reaches the fake on a socket, which
// runs one fake per connection with the connection as its stdin,
// stdout and stderr, as polkit 127's Accept=yes socket unit does. The program is one that
// cannot run, so a fall back to it fails.
func (f *Fake) Socket(t testing.TB) helper.Transport {
	t.Helper()
	path := filepath.Join(sockdir.Dir(t), "helper")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("helpertest: listen %s: %v", path, err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = l.Close()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			unix, ok := conn.(*net.UnixConn)
			if !ok {
				t.Errorf("helpertest: accepted a %T on a unix socket", conn)
				_ = conn.Close()
				continue
			}
			wg.Go(func() { f.serve(t, unix) })
		}
	})
	return helper.Transport{Socket: path, Program: filepath.Join(f.dir, "no-program")}
}

func (f *Fake) serve(t testing.TB, conn *net.UnixConn) {
	file, err := conn.File()
	_ = conn.Close()
	if err != nil {
		t.Errorf("helpertest: socket connection: %v", err)
		return
	}
	defer file.Close()
	cmd := exec.Command(f.program, socketArg) //nolint:gosec // the test binary under the fake's name
	cmd.Stdin, cmd.Stdout, cmd.Stderr = file, file, file
	if err := cmd.Start(); err != nil {
		t.Errorf("helpertest: start socket helper: %v", err)
		return
	}
	_ = file.Close()
	_ = cmd.Wait()
}

// Started is how many helper processes the fake ran.
func (f *Fake) Started(t testing.TB) int {
	t.Helper()
	return len(f.pids(t))
}

// AssertNoneLeft fails the test when a helper process, zombies
// included, is still there once the socket's helpers had time to be
// reaped.
func (f *Fake) AssertNoneLeft(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(reapTimeout)
	for {
		var live []int
		for _, pid := range f.pids(t) {
			if syscall.Kill(pid, 0) == nil {
				live = append(live, pid)
			}
		}
		if len(live) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("helpertest: helper processes %v still exist", live)
		}
		time.Sleep(reapPollPeriod)
	}
}

func (f *Fake) pids(t testing.TB) []int {
	t.Helper()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatalf("helpertest: %v", err)
	}
	var pids []int
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), pidSuffix)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			t.Fatalf("helpertest: pid file %s: %v", e.Name(), err)
		}
		pids = append(pids, pid)
	}
	return pids
}

// MaybeRun runs the fake helper and exits when the test binary was
// started as one; TestMain calls it first.
func MaybeRun() {
	name := filepath.Base(os.Args[0])
	scenario, ok := strings.CutPrefix(name, namePrefix)
	if !ok {
		return
	}
	if err := run(Scenario(scenario)); err != nil {
		if exit, ok := errors.AsType[exitStatus](err); ok {
			os.Exit(int(exit))
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }

func run(s Scenario) error {
	pidFile := filepath.Join(filepath.Dir(os.Args[0]), strconv.Itoa(os.Getpid())+pidSuffix)
	if err := os.WriteFile(pidFile, nil, 0o600); err != nil { //nolint:gosec // beside the fake's own link, in the test's directory
		return fmt.Errorf("record pid: %w", err)
	}
	in := bufio.NewReader(os.Stdin)
	user, err := userArg(in)
	if err != nil {
		return err
	}
	cookie, err := readLine(in)
	if err != nil {
		return fmt.Errorf("read cookie: %w", err)
	}
	known := user == User && cookie == Cookie
	switch s {
	case Answer:
		ok, err := ask(in, "PAM_PROMPT_ECHO_OFF", PasswordPrompt, Password)
		return verdict(known && ok, err)
	case TwoPrompts:
		name, err := ask(in, "PAM_PROMPT_ECHO_ON", UserPrompt, User)
		if err != nil || !name {
			return verdict(false, err)
		}
		ok, err := ask(in, "PAM_PROMPT_ECHO_OFF", PasswordPrompt, Password)
		return verdict(known && ok, err)
	case InfoOnly:
		if err := say("PAM_TEXT_INFO " + Escape(FingerInfo)); err != nil {
			return err
		}
		return verdict(known, nil)
	case HangUp:
		if _, err := ask(in, "PAM_PROMPT_ECHO_OFF", PasswordPrompt, Password); err != nil {
			return err
		}
		return exitStatus(HangUpStatus)
	case Wait:
		if err := say("PAM_PROMPT_ECHO_OFF " + Escape(PasswordPrompt)); err != nil {
			return err
		}
		_, _ = readLine(in)
		return nil
	case InfoWait:
		if err := say("PAM_TEXT_INFO " + Escape(FingerInfo)); err != nil {
			return err
		}
		_, _ = readLine(in)
		return nil
	default:
		return fmt.Errorf("unknown scenario %q", s)
	}
}

func userArg(in *bufio.Reader) (string, error) {
	if len(os.Args) != 2 {
		return "", fmt.Errorf("want one argument, got %q", os.Args[1:])
	}
	if os.Args[1] != socketArg {
		return os.Args[1], nil
	}
	user, err := readLine(in)
	if err != nil {
		return "", fmt.Errorf("read user: %w", err)
	}
	return user, nil
}

func ask(in *bufio.Reader, kind, prompt, want string) (bool, error) {
	if err := say(kind + " " + Escape(prompt)); err != nil {
		return false, err
	}
	got, err := readLine(in)
	if err != nil {
		return false, fmt.Errorf("read answer: %w", err)
	}
	return got == want, nil
}

func verdict(ok bool, err error) error {
	if err != nil {
		return err
	}
	if !ok {
		if err := say("PAM_ERROR_MSG " + Escape(SorryError)); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "polkit-agent-helper-1: pam_authenticate failed: Authentication failure")
		return say("FAILURE")
	}
	return say("SUCCESS")
}

func say(line string) error {
	if _, err := os.Stdout.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("write %q: %w", line, err)
	}
	return nil
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}

// Escape is g_strescape with no exceptions: \b \f \n \r \t \v \\ \"
// by name, other bytes below 0x20 and from 0x7f as three octal digits.
func Escape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; c {
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
