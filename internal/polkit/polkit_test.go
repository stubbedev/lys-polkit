package polkit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/polkit/polkittest"
)

const eventTimeout = 5 * time.Second

type agent struct{}

func (agent) BeginAuthentication(string, string, string, map[string]string, string, []polkit.Identity) *dbus.Error {
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
	go func() { s.done <- polkit.Serve(ctx, conn, r, agent{}, func() {}) }()
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

	tests := []struct {
		name        string
		logind      logindtest.Config
		env         map[string]string
		wantSubject string
		wantLocale  string
	}{
		{
			name:        "inside a session",
			logind:      logindtest.Config{Own: "c2", Display: "3"},
			env:         map[string]string{"LC_MESSAGES": "da_DK.UTF-8", "LANG": "en_US.UTF-8"},
			wantSubject: "unix-session c2",
			wantLocale:  "da_DK.UTF-8",
		},
		{
			name:        "outside any session",
			logind:      logindtest.Config{Display: "3"},
			env:         map[string]string{"LANG": "en_GB.UTF-8"},
			wantSubject: "unix-session 3",
			wantLocale:  "en_GB.UTF-8",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := dbustest.Start(t)
			logindtest.Serve(t, bus, tc.logind)
			auth := polkittest.New()
			auth.Serve(t, bus.Conn(t))
			conn := bus.Conn(t)

			subject, err := polkit.CallerSubject(t.Context(), conn)
			if err != nil {
				t.Fatalf("CallerSubject: %v", err)
			}
			r := polkit.Registration{Subject: subject, Locale: polkit.Locale(func(k string) string { return tc.env[k] })}
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

func TestCallerSubjectWithoutASessionKeepsLogindsError(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	logindtest.Serve(t, bus, logindtest.Config{})
	_, err := polkit.CallerSubject(t.Context(), bus.Conn(t))
	var derr dbus.Error
	if !errors.As(err, &derr) || derr.Name != logind.ErrNoSuchSession {
		t.Fatalf("CallerSubject = %v, want logind's %s", err, logind.ErrNoSuchSession)
	}
}

func TestServeRefusedForASecondAgent(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	auth := polkittest.New()
	subject := polkit.SessionSubject("c2")
	auth.Preregister(subject)
	auth.Serve(t, bus.Conn(t))

	err := polkit.Serve(t.Context(), bus.Conn(t), polkit.Registration{Subject: subject, Locale: "C"}, agent{}, func() {})
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

func TestServeReportsRegistrationsAndChanges(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	first := polkittest.New()
	release := first.Serve(t, bus.Conn(t))
	authorityConn := bus.Conn(t)
	conn := bus.Conn(t)
	changes := make(chan struct{}, 16)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- polkit.Serve(ctx, conn, polkit.Registration{Subject: polkit.SessionSubject("c2"), Locale: "C"}, agent{}, func() { changes <- struct{}{} })
	}()
	change := func(why string) {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(eventTimeout):
			t.Fatalf("no change reported %s within %s", why, eventTimeout)
		}
	}
	next(t, first)
	change("after the registration")

	if err := authorityConn.Emit(polkit.AuthorityPath, polkit.AuthorityIface+".Changed"); err != nil {
		t.Fatalf("emit Changed from a peer that is not polkitd: %v", err)
	}
	release()
	second := polkittest.New()
	second.Serve(t, bus.Conn(t))
	if err := second.EmitChanged(); err != nil {
		t.Fatalf("emit Changed: %v", err)
	}
	next(t, second)
	change("after the registration with the restarted polkitd")
	change("on polkitd's Changed")
	select {
	case <-changes:
		t.Error("a change reported for a Changed signal polkitd did not send")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(eventTimeout):
		t.Fatal("Serve did not return within " + eventTimeout.String())
	}
}
