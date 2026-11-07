// Package polkittest is a fake polkitd Authority for tests: it records
// agent registrations and refuses a second agent for a subject, as
// polkit 127 does, describes the actions it is given, and counts the
// calls it answers.
package polkittest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/polkit"
)

// AlreadyExists is polkitd's refusal of a second agent for a subject
// (polkitbackendinteractiveauthority.c).
const AlreadyExists = "An authentication agent already exists for the given subject"

var ExecAction = polkit.ActionDescription{
	ActionID:         "org.freedesktop.policykit.exec",
	Description:      "Run a program as another user",
	Message:          "Authentication is required to run a program as another user",
	VendorName:       "The polkit project",
	VendorURL:        "https://github.com/polkit-org/polkit/",
	IconName:         "",
	ImplicitAny:      2,
	ImplicitInactive: 2,
	ImplicitActive:   2,
	Annotations:      map[string]string{},
}

// Event is one registration change the Authority saw.
type Event struct {
	Register bool
	Sender   string
	Subject  string
	Locale   string
	Path     string
}

// Authority is the fake. Events receives every successful Register and
// Unregister, in order. Actions are what EnumerateActions describes,
// unless FailActions makes it fail; both are set before Serve.
type Authority struct {
	Events      chan Event
	Actions     []polkit.ActionDescription
	FailActions bool

	mu     sync.Mutex
	conn   *dbus.Conn
	agents map[string]string
	calls  map[string]int
}

// New makes an Authority whose Events holds up to 16 unread events.
func New() *Authority {
	return &Authority{Events: make(chan Event, 16), agents: map[string]string{}, calls: map[string]int{}}
}

// Preregister records an agent for subject, as if another process had
// registered it first.
func (a *Authority) Preregister(subject polkit.Subject) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.agents[subject.String()] = "another agent"
}

func (a *Authority) Calls(method string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[method]
}

func (a *Authority) count(method string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[method]++
}

// Serve exports the Authority on conn and takes org.freedesktop.PolicyKit1;
// the returned release drops the name.
func (a *Authority) Serve(t testing.TB, conn *dbus.Conn) (release func()) {
	t.Helper()
	release, err := dbusx.Serve(conn, dbusx.Service{Name: polkit.AuthorityName, Path: polkit.AuthorityPath, Interface: polkit.AuthorityIface, Methods: authority{a}})
	if err != nil {
		t.Fatalf("polkittest: %v", err)
	}
	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	return release
}

// authority holds the exported methods, so Authority's own methods stay
// off the bus.
type authority struct{ a *Authority }

func (x authority) RegisterAuthenticationAgent(sender dbus.Sender, subject polkit.Subject, locale, path string) *dbus.Error {
	x.a.count("RegisterAuthenticationAgent")
	key := subject.String()
	x.a.mu.Lock()
	defer x.a.mu.Unlock()
	if _, ok := x.a.agents[key]; ok {
		return dbus.NewError(polkit.ErrFailed, []any{AlreadyExists})
	}
	x.a.agents[key] = string(sender)
	x.a.Events <- Event{Register: true, Sender: string(sender), Subject: key, Locale: locale, Path: path}
	return nil
}

func (x authority) UnregisterAuthenticationAgent(sender dbus.Sender, subject polkit.Subject, path string) *dbus.Error {
	x.a.count("UnregisterAuthenticationAgent")
	key := subject.String()
	x.a.mu.Lock()
	defer x.a.mu.Unlock()
	if owner, ok := x.a.agents[key]; !ok || owner != string(sender) {
		return dbus.NewError(polkit.ErrFailed, []any{fmt.Sprintf("No such agent registered: %s from %s", key, sender)})
	}
	delete(x.a.agents, key)
	x.a.Events <- Event{Sender: string(sender), Subject: key, Path: path}
	return nil
}

func (x authority) EnumerateActions(string) ([]polkit.ActionDescription, *dbus.Error) {
	x.a.count("EnumerateActions")
	if x.a.FailActions {
		return nil, dbus.NewError(polkit.ErrFailed, []any{"the fake polkitd refuses to enumerate actions"})
	}
	return x.a.Actions, nil
}

func (a *Authority) EmitChanged() error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	return conn.Emit(polkit.AuthorityPath, polkit.AuthorityIface+".Changed")
}
