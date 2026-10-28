// Package polkit is lys-polkit's side of polkitd's D-Bus API: the
// Authority it registers with and the AuthenticationAgent interface it
// exports, kept registered for the session across polkitd restarts.
package polkit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/logind"
)

// D-Bus identity of polkitd's Authority, and of the agent lys-polkit
// exports.
const (
	AuthorityName  = "org.freedesktop.PolicyKit1"
	AuthorityPath  = dbus.ObjectPath("/org/freedesktop/PolicyKit1/Authority")
	AuthorityIface = "org.freedesktop.PolicyKit1.Authority"
	AgentIface     = "org.freedesktop.PolicyKit1.AuthenticationAgent"
	AgentPath      = dbus.ObjectPath("/dev/stubbe/lys/PolicyKit1/AuthenticationAgent")
)

// Error names polkitd understands from an agent (polkiterror.c).
const (
	ErrFailed    = "org.freedesktop.PolicyKit1.Error.Failed"
	ErrCancelled = "org.freedesktop.PolicyKit1.Error.Cancelled"
)

// callTimeout bounds each call to polkitd. A registration is not cut
// short by the end of Serve's context, so an agent told to stop while
// registering still unregisters what polkitd registered.
const callTimeout = 5 * time.Second

// Subject is a polkit subject, (sa{sv}): its kind and its details.
type Subject struct {
	Kind    string
	Details map[string]dbus.Variant
}

// SessionSubject is the unix-session subject for a logind session.
func SessionSubject(id logind.SessionID) Subject {
	return Subject{Kind: "unix-session", Details: map[string]dbus.Variant{"session-id": dbus.MakeVariant(string(id))}}
}

func (s Subject) String() string {
	if id, ok := s.Details["session-id"]; ok {
		return fmt.Sprintf("%s %v", s.Kind, id.Value())
	}
	return s.Kind
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
	Subject Subject
	Locale  string
}

// Serve exports agent (an object with polkit's BeginAuthentication and
// CancelAuthentication methods) at AgentPath and registers it for
// r.Subject, again each time org.freedesktop.PolicyKit1 gains a new
// owner, until ctx ends; then it unregisters. Any refusal, such as
// another agent already registered for the subject, ends Serve with
// polkitd's error.
func Serve(ctx context.Context, conn *dbus.Conn, r Registration, agent any) error {
	unexport, err := dbusx.Export(conn, AgentPath, dbusx.Interface{Name: AgentIface, Methods: agent})
	if err != nil {
		return fmt.Errorf("export polkit agent at %s: %w", AgentPath, err)
	}
	defer unexport()

	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	match := dbusx.OwnerMatch(AuthorityName)
	if err := conn.AddMatchSignalContext(ctx, match...); err != nil {
		return fmt.Errorf("watch %s: %w", AuthorityName, err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()

	calls := context.WithoutCancel(ctx)
	registered, err := register(calls, conn, r)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return unregister(calls, conn, r.Subject)
		case sig, ok := <-signals:
			if !ok {
				return errors.New("system bus connection closed")
			}
			if _, owner, isAuthority := dbusx.NameOwnerChange(sig, AuthorityName); !isAuthority || owner == "" || sig.Sequence <= registered {
				continue
			}
			if registered, err = register(calls, conn, r); err != nil {
				return err
			}
		}
	}
}

// register registers the agent and returns the sequence of polkitd's
// reply: an owner change sequenced before it happened before the
// registration (polkitd's own activation by it), one after it is a
// polkitd that has not seen the agent.
func register(ctx context.Context, conn *dbus.Conn, r Registration) (dbus.Sequence, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	call := authority(conn).CallWithContext(ctx, AuthorityIface+".RegisterAuthenticationAgent", 0, r.Subject, r.Locale, string(AgentPath))
	if call.Err != nil {
		return dbus.NoSequence, fmt.Errorf("register polkit agent for %s: %w", r.Subject, call.Err)
	}
	return call.ResponseSequence, nil
}

func unregister(ctx context.Context, conn *dbus.Conn, subject Subject) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := authority(conn).CallWithContext(ctx, AuthorityIface+".UnregisterAuthenticationAgent", 0, subject, string(AgentPath)).Err; err != nil {
		return fmt.Errorf("unregister polkit agent for %s: %w", subject, err)
	}
	return nil
}

func authority(conn *dbus.Conn) dbus.BusObject {
	return conn.Object(AuthorityName, AuthorityPath)
}
