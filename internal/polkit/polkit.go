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
func Serve(ctx context.Context, conn *dbus.Conn, r Registration, agent any, changed func()) error {
	unexport, err := dbusx.Export(conn, AgentPath, dbusx.Interface{Name: libpolkit.AgentInterface, Methods: agent})
	if err != nil {
		return fmt.Errorf("export polkit agent at %s: %w", AgentPath, err)
	}
	defer unexport()

	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	for _, match := range [][]dbus.MatchOption{dbusx.OwnerMatch(libpolkit.BusName), libpolkit.ChangedMatch()} {
		if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
			return fmt.Errorf("watch %s: %w", libpolkit.BusName, err)
		}
		defer func() { _ = conn.RemoveMatchSignal(match...) }()
	}

	authority := libpolkit.NewAuthority(conn)
	changedSignal := libpolkit.Changed.Name()
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
		case sig, ok := <-signals:
			if !ok {
				return errors.New("system bus connection closed")
			}
			if sig.Path == libpolkit.AuthorityPath && sig.Name == changedSignal {
				changed()
				continue
			}
			if _, owner, isAuthority := dbusx.NameOwnerChange(sig, libpolkit.BusName); !isAuthority || owner == "" || sig.Sequence <= registered {
				continue
			}
			if registered, err = authority.RegisterAuthenticationAgent(calls, r.Subject, r.Locale, AgentPath); err != nil {
				return err
			}
			changed()
		}
	}
}
