package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-polkit/internal/logind"
	"github.com/stubbedev/lys-polkit/internal/logind/logindtest"
	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/polkit/polkittest"
)

const (
	processEnv   = "LYS_POLKIT_TEST_RUN_MAIN"
	eventTimeout = 5 * time.Second
)

var display = logind.SessionRef{ID: "3", Path: "/org/freedesktop/login1/session/_33"}

func TestMain(m *testing.M) {
	if os.Getenv(processEnv) != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func next(t *testing.T, a *polkittest.Authority) polkittest.Event {
	t.Helper()
	select {
	case e := <-a.Events:
		return e
	case <-time.After(eventTimeout):
		t.Fatal("no registration change within " + eventTimeout.String())
		return polkittest.Event{}
	}
}

// startAgent runs lys-polkit's main in a process of its own, with a
// fake logind and the given Authority on its system bus.
func startAgent(t *testing.T, auth *polkittest.Authority) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	system := dbustest.Start(t)
	session := dbustest.Start(t)
	logindtest.Logind{Display: display}.Serve(t, system.Conn(t))
	auth.Serve(t, system.Conn(t))
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), processEnv+"=1",
		"DBUS_SYSTEM_BUS_ADDRESS="+system.Address, "DBUS_SESSION_BUS_ADDRESS="+session.Address, "LC_ALL=", "LC_MESSAGES=", "LANG=da_DK.UTF-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, &stderr
}

func TestUnregistersOnSignal(t *testing.T) {
	t.Parallel()

	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()

			auth := polkittest.New()
			cmd, stderr := startAgent(t, auth)
			registered := next(t, auth)
			want := polkittest.Event{Register: true, Sender: registered.Sender, Subject: "unix-session 3", Locale: "da_DK.UTF-8", Path: string(polkit.AgentPath)}
			if registered != want {
				t.Fatalf("registered %+v, want %+v", registered, want)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatalf("signal agent: %v", err)
			}
			if got := next(t, auth); got.Register || got.Sender != registered.Sender {
				t.Errorf("after %v saw %+v, want the agent unregistered", sig, got)
			}
			if err := cmd.Wait(); err != nil {
				t.Errorf("agent exited with %v after %v: %s", err, sig, stderr)
			}
		})
	}
}

func TestExitsNonZeroWhenRefused(t *testing.T) {
	t.Parallel()

	auth := polkittest.New()
	auth.Preregister(polkit.SessionSubject(display.ID))
	cmd, stderr := startAgent(t, auth)
	err := cmd.Wait()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() == 0 {
		t.Fatalf("agent exited with %v, want a non-zero status", err)
	}
	if !strings.Contains(stderr.String(), polkittest.AlreadyExists) {
		t.Errorf("stderr %q lacks polkit's %q", stderr, polkittest.AlreadyExists)
	}
}

func TestUserName(t *testing.T) {
	t.Parallel()

	if name, err := userName(0); err != nil || name != "root" {
		t.Errorf("userName(0) = %q, %v; want root", name, err)
	}
	if _, err := userName(4294967294); err == nil {
		t.Error("userName found a user for uid 4294967294")
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want command
	}{
		{args: nil, want: serve},
		{args: []string{"--version"}, want: printVersion},
		{args: []string{"-version"}, want: printVersion},
		{args: []string{"--help"}, want: printHelp},
		{args: []string{"-h"}, want: printHelp},
	}
	for _, tc := range tests {
		got, err := parseArgs(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("parseArgs(%q) = %v, %v; want %v", tc.args, got, err, tc.want)
		}
	}
	for _, args := range [][]string{{"--verbose"}, {"serve"}, {"--version", "--help"}, {"--version=maybe"}} {
		if got, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) = %v, want an error", args, got)
		}
	}
}

func TestBuildVersion(t *testing.T) {
	t.Parallel()

	if got := buildVersion(); got == "" {
		t.Error("buildVersion is empty")
	}
}
