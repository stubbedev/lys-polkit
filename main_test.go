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
	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"
	libpolkit "github.com/stubbedev/lys-lib/polkit"
	"github.com/stubbedev/lys-lib/polkit/polkittest"

	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/retained"
)

const (
	processEnv   = "LYS_POLKIT_TEST_RUN_MAIN"
	eventTimeout = 5 * time.Second
)

const display logind.SessionID = "3"

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
func startAgent(t *testing.T, auth *polkittest.Authority) (*exec.Cmd, *bytes.Buffer, *dbustest.Bus) {
	t.Helper()
	system := dbustest.Start(t)
	session := dbustest.Start(t)
	logindtest.Serve(t, system, logindtest.Config{Display: display})
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
	return cmd, &stderr, session
}

func TestUnregistersOnSignal(t *testing.T) {
	t.Parallel()

	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()

			auth := polkittest.New()
			cmd, stderr, _ := startAgent(t, auth)
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
	auth.Preregister(libpolkit.UnixSession{ID: display})
	cmd, stderr, _ := startAgent(t, auth)
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

	if name, err := userName(t.Context(), 0); err != nil || name != "root" {
		t.Errorf("userName(0) = %q, %v; want root", name, err)
	}
	if _, err := userName(t.Context(), 4294967294); err == nil {
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
		{args: []string{"revoke"}, want: revoke},
	}
	for _, tc := range tests {
		got, err := parseArgs(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("parseArgs(%q) = %v, %v; want %v", tc.args, got, err, tc.want)
		}
	}
	for _, args := range [][]string{{"--verbose"}, {"serve"}, {"--version", "--help"}, {"--version=maybe"}, {"revoke", "now"}, {"--version", "revoke"}} {
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

func TestRevokeGivesUpTheRetainedAuthorizations(t *testing.T) {
	t.Parallel()

	auth := polkittest.New()
	auth.SetTemporary([]libpolkit.TemporaryAuthorization{{ID: "tmpauthz0", ActionID: "org.example.keep", Subject: polkittest.Wire(t, libpolkit.UnixSession{ID: display}), Obtained: 1, Expires: 301}})
	_, _, session := startAgent(t, auth)
	next(t, auth)
	conn := session.Conn(t)
	deadline := time.Now().Add(eventTimeout)
	for {
		list, err := retained.NewClient(conn).Authorizations(t.Context())
		if err == nil && len(list) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Authorizations = %+v, %v; want the one polkitd holds", list, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cmd := exec.Command(os.Args[0], "revoke")
	cmd.Env = append(os.Environ(), processEnv+"=1", "DBUS_SESSION_BUS_ADDRESS="+session.Address)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lys-polkit revoke: %v: %s", err, out)
	}
	if n := auth.Calls("RevokeTemporaryAuthorizations"); n != 1 {
		t.Errorf("%d RevokeTemporaryAuthorizations, want 1", n)
	}
}

func TestRevokeWithoutAnAgentFails(t *testing.T) {
	t.Parallel()

	session := dbustest.Start(t)
	cmd := exec.Command(os.Args[0], "revoke")
	cmd.Env = append(os.Environ(), processEnv+"=1", "DBUS_SESSION_BUS_ADDRESS="+session.Address)
	out, err := cmd.CombinedOutput()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 || !strings.Contains(string(out), retained.ServiceName) {
		t.Fatalf("lys-polkit revoke with no agent: %v: %s; want exit 1 naming %s", err, out, retained.ServiceName)
	}
}
