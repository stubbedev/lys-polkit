// Package polkit is lys-polkit's side of polkitd's D-Bus API: the
// AuthenticationAgent interface it exports, kept registered with the
// Authority for the session across polkitd restarts.
package polkit

import (
	"context"
	"errors"
	"fmt"

	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/logind"
	libpolkit "github.com/stubbedev/lys-lib/polkit"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// AgentPath is where lys-polkit exports its agent.
const AgentPath = dbus.ObjectPath("/dev/stubbe/lys/PolicyKit1/AuthenticationAgent")

// CallerSubject is the session subject polkitd attributes the calling
// process to, asking logind on system: the process's own session when
// it is in one, else its user's display session. A user service runs
// outside any session, so polkitd resolves it through the display
// session, and an agent registering for any other session is refused.
func CallerSubject(ctx context.Context, system *dbus.Conn) (libpolkit.UnixSession, error) {
	session, err := logind.NewManager(system).GetSession(ctx, logind.AutoSession)
	if err != nil {
		return libpolkit.UnixSession{}, err
	}
	id, err := session.ID(ctx)
	if err != nil {
		return libpolkit.UnixSession{}, err
	}
	return libpolkit.UnixSession{ID: id}, nil
}

// localeVars are the variables naming the message locale, most
// specific first (POSIX setlocale for LC_MESSAGES).
var localeVars = [...]string{"LC_ALL", "LC_MESSAGES", "LANG"}

// posixLocale is the locale POSIX defines when none of localeVars is
// set.
const posixLocale = "C"

// Locale is the message locale polkitd should translate the action
// messages into: the first of LC_ALL, LC_MESSAGES and LANG that is set,
// else the POSIX locale.
func Locale(getenv func(string) string) string {
	for _, name := range localeVars {
		if v := getenv(name); v != "" {
			return v
		}
	}
	return posixLocale
}

// Registration is what an agent registers with polkitd.
type Registration struct {
	Subject libpolkit.Subject
	Locale  string
}

// Serve exports agent (an object with polkit's BeginAuthentication and
// CancelAuthentication methods) at AgentPath and registers it for
// r.Subject, again each time org.freedesktop.PolicyKit1 gains a new
// owner, until ctx ends; then it unregisters. A registration is not
// cut short by the end of ctx, so an agent told to stop while
// registering still unregisters what polkitd registered. Any refusal,
// such as another agent already registered for the subject, ends Serve
// with polkitd's error. changed runs on Serve's goroutine after each
// registration and on each of the Authority's Changed signals.
func Serve(ctx context.Context, conn *dbus.Conn, r Registration, agent any, changed func()) (err error) {
	unexport, err := dbusx.Export(conn, AgentPath, dbusx.Interface{Name: libpolkit.AgentInterface, Methods: agent})
	if err != nil {
		return fmt.Errorf("export polkit agent at %s: %w", AgentPath, err)
	}
	defer unexport()

	watching, stopWatching := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWatching()
	events := make(chan authorityEvent, 16)
	undecoded := make(chan error, 1)
	post := func(e authorityEvent) {
		select {
		case events <- e:
		case <-watching.Done():
		}
	}
	watch, err := dbusx.WatchSignals(watching, conn, dbusx.FromOwner(libpolkit.BusName),
		func(err error) {
			select {
			case undecoded <- err:
			default:
			}
		},
		dbusx.On(libpolkit.Changed, libpolkit.AuthorityPath, func(libpolkit.AuthorityChanged, dbus.Sequence) error {
			post(authorityEvent{})
			return nil
		}),
		dbusx.OnOwnerChange(libpolkit.BusName, func(_, owner string, announced dbus.Sequence) error {
			if owner == "" {
				return nil
			}
			post(authorityEvent{appeared: true, announced: announced})
			return nil
		}))
	if err != nil {
		return err
	}
	defer func() {
		stopWatching()
		if stopErr := watch.Stop(); !errors.Is(stopErr, dbusx.ErrDisconnected) {
			err = errors.Join(err, stopErr)
		}
	}()

	authority := libpolkit.NewAuthority(conn)
	calls := context.WithoutCancel(ctx)
	registered, err := authority.RegisterAuthenticationAgent(calls, r.Subject, r.Locale, AgentPath)
	if err != nil {
		return err
	}
	changed()
	for {
		select {
		case <-ctx.Done():
			return authority.UnregisterAuthenticationAgent(calls, r.Subject, AgentPath)
		case <-watch.Done():
			return errors.New("system bus connection closed")
		case err := <-undecoded:
			return fmt.Errorf("watch %s: %w", libpolkit.BusName, err)
		case e := <-events:
			if !e.appeared {
				changed()
				continue
			}
			if e.announced <= registered {
				continue
			}
			if registered, err = authority.RegisterAuthenticationAgent(calls, r.Subject, r.Locale, AgentPath); err != nil {
				return err
			}
			changed()
		}
	}
}

// authorityEvent is what Serve's watch saw of polkitd, in order: the
// Authority's Changed signal, or its name gaining an owner, announced
// at a sequence of the connection.
type authorityEvent struct {
	appeared  bool
	announced dbus.Sequence
}
