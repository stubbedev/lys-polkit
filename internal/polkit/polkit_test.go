package polkit_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/logind"
	"github.com/stubbedev/lys-polkit/internal/logind/logindtest"
	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/polkit/polkittest"
)

const eventTimeout = 5 * time.Second

type agent struct{}

func (agent) BeginAuthentication(string, string, string, map[string]string, string, []polkit.Subject) *dbus.Error {
	return nil
}

func (agent) CancelAuthentication(string) *dbus.Error { return nil }

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

type served struct {
	cancel context.CancelFunc
	done   chan error
}

func serve(t *testing.T, conn *dbus.Conn, r polkit.Registration) served {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := served{cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- polkit.Serve(ctx, conn, r, agent{}) }()
	t.Cleanup(cancel)
	return s
}

func (s served) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(eventTimeout):
		t.Fatal("Serve did not return within " + eventTimeout.String())
		return nil
	}
}

func TestServeRegistersTheResolvedSession(t *testing.T) {
	t.Parallel()

	tty := logind.SessionRef{ID: "c2", Path: "/org/freedesktop/login1/session/c2"}
	display := logind.SessionRef{ID: "3", Path: "/org/freedesktop/login1/session/_33"}
	tests := []struct {
		name        string
		logind      logindtest.Logind
		env         map[string]string
		wantSubject string
		wantLocale  string
	}{
		{
			name:        "inside a session",
			logind:      logindtest.Logind{Own: tty, Display: display},
			env:         map[string]string{"LC_MESSAGES": "da_DK.UTF-8", "LANG": "en_US.UTF-8"},
			wantSubject: "unix-session c2",
			wantLocale:  "da_DK.UTF-8",
		},
		{
			name:        "outside any session",
			logind:      logindtest.Logind{Display: display},
			env:         map[string]string{"LANG": "en_GB.UTF-8"},
			wantSubject: "unix-session 3",
			wantLocale:  "en_GB.UTF-8",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := dbustest.Start(t)
			tc.logind.Serve(t, bus.Conn(t))
			auth := polkittest.New()
			auth.Serve(t, bus.Conn(t))
			conn := bus.Conn(t)

			id, err := logind.Session(t.Context(), conn, 42, 1000)
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			r := polkit.Registration{Subject: polkit.SessionSubject(id), Locale: polkit.Locale(func(k string) string { return tc.env[k] })}
			s := serve(t, conn, r)

			want := polkittest.Event{Register: true, Sender: conn.Names()[0], Subject: tc.wantSubject, Locale: tc.wantLocale, Path: string(polkit.AgentPath)}
			if got := next(t, auth); got != want {
				t.Errorf("registered %+v, want %+v", got, want)
			}
			s.cancel()
			if err := s.wait(t); err != nil {
				t.Fatalf("Serve: %v", err)
			}
			if got := next(t, auth); got.Register || got.Subject != tc.wantSubject {
				t.Errorf("after cancel %+v, want %s unregistered", got, tc.wantSubject)
			}
		})
	}
}

func TestServeRefusedForASecondAgent(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	auth := polkittest.New()
	subject := polkit.SessionSubject("c2")
	auth.Preregister(subject)
	auth.Serve(t, bus.Conn(t))

	err := polkit.Serve(t.Context(), bus.Conn(t), polkit.Registration{Subject: subject, Locale: "C"}, agent{})
	var derr dbus.Error
	if !errors.As(err, &derr) || derr.Name != polkit.ErrFailed || !strings.Contains(err.Error(), polkittest.AlreadyExists) {
		t.Fatalf("Serve = %v, want polkit's %q", err, polkittest.AlreadyExists)
	}
}

func TestServeReregistersWithANewAuthority(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	first := polkittest.New()
	release := first.Serve(t, bus.Conn(t))
	conn := bus.Conn(t)
	r := polkit.Registration{Subject: polkit.SessionSubject("c2"), Locale: "C"}
	s := serve(t, conn, r)
	if got := next(t, first); !got.Register {
		t.Fatalf("first Authority saw %+v, want a registration", got)
	}

	release()
	second := polkittest.New()
	second.Serve(t, bus.Conn(t))
	if got := next(t, second); !got.Register || got.Subject != "unix-session c2" || got.Sender != conn.Names()[0] {
		t.Fatalf("second Authority saw %+v, want the agent registered again", got)
	}
	select {
	case e := <-first.Events:
		t.Errorf("first Authority saw %+v after losing the name", e)
	default:
	}
	s.cancel()
	if err := s.wait(t); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestLocale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "LC_ALL wins", env: map[string]string{"LC_ALL": "de_DE.UTF-8", "LC_MESSAGES": "da_DK.UTF-8", "LANG": "en_US.UTF-8"}, want: "de_DE.UTF-8"},
		{name: "LC_MESSAGES before LANG", env: map[string]string{"LC_MESSAGES": "da_DK.UTF-8", "LANG": "en_US.UTF-8"}, want: "da_DK.UTF-8"},
		{name: "LANG alone", env: map[string]string{"LANG": "en_US.UTF-8"}, want: "en_US.UTF-8"},
		{name: "empty values are unset", env: map[string]string{"LC_ALL": "", "LANG": "en_US.UTF-8"}, want: "en_US.UTF-8"},
		{name: "none set is POSIX", env: map[string]string{}, want: "C"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := polkit.Locale(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("Locale = %q, want %q", got, tc.want)
			}
		})
	}
}

const processEnv = "LYS_POLKIT_TEST_SERVE_PROCESS"

func TestServeProcess(t *testing.T) {
	if os.Getenv(processEnv) == "" {
		t.Skip("the agent process the TestServeProcess* tests start")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := polkit.Serve(ctx, conn, polkit.Registration{Subject: polkit.SessionSubject("c2"), Locale: "C"}, agent{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func startProcess(t *testing.T, bus *dbustest.Bus) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeProcess$")
	cmd.Env = append(os.Environ(), processEnv+"=1", "DBUS_SYSTEM_BUS_ADDRESS="+bus.Address)
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

func TestServeProcessUnregistersOnSignal(t *testing.T) {
	t.Parallel()

	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()

			bus := dbustest.Start(t)
			auth := polkittest.New()
			auth.Serve(t, bus.Conn(t))
			cmd, stderr := startProcess(t, bus)
			registered := next(t, auth)
			if !registered.Register {
				t.Fatalf("saw %+v, want a registration", registered)
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

func TestServeProcessExitsNonZeroWhenRefused(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	auth := polkittest.New()
	auth.Preregister(polkit.SessionSubject("c2"))
	auth.Serve(t, bus.Conn(t))
	cmd, stderr := startProcess(t, bus)
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("agent exited with %v, want a non-zero status", err)
	}
	if !strings.Contains(stderr.String(), polkittest.AlreadyExists) {
		t.Errorf("stderr %q lacks polkit's %q", stderr, polkittest.AlreadyExists)
	}
}
