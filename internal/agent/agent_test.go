package agent_test

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/agent"
	"github.com/stubbedev/lys-polkit/internal/helper/helpertest"
	"github.com/stubbedev/lys-polkit/internal/polkit"
)

func TestMain(m *testing.M) {
	helpertest.MaybeRun()
	os.Exit(m.Run())
}

const (
	timeout = 5 * time.Second
	bob     = polkit.UID(1000)
	alice   = polkit.UID(1001)
)

var names = map[polkit.UID]string{bob: "bob", alice: helpertest.User}

func userName(uid polkit.UID) (string, error) {
	if name, ok := names[uid]; ok {
		return name, nil
	}
	return "", fmt.Errorf("no user %d", uid)
}

// call is one call the fake dialogs host received.
type call struct {
	method string
	caller portaldialogs.Caller
	req    portaldialogs.AuthRequest
}

// host is the shell's dialogs: it reports each authentication call and
// answers Authenticate with what the test sends on replies.
type host struct {
	portaldialogs.Host
	calls   chan call
	replies chan portaldialogs.AuthReply
	stop    chan struct{}
}

func (h *host) Authenticate(c portaldialogs.Caller, r portaldialogs.AuthRequest) portaldialogs.AuthReply {
	h.calls <- call{"Authenticate", c, r}
	select {
	case reply := <-h.replies:
		return reply
	case <-h.stop:
		return portaldialogs.AuthCancel{}
	}
}

func (h *host) ShowAuthentication(c portaldialogs.Caller, r portaldialogs.AuthRequest) {
	h.calls <- call{"ShowAuthentication", c, r}
}

func (h *host) Dismiss(handle string) {
	h.calls <- call{method: "Dismiss", caller: portaldialogs.Caller{Handle: handle}}
}

type env struct {
	t         *testing.T
	agentName string
	caller    *dbus.Conn
	host      *host
	emitter   *portaldialogs.Emitter
	fake      *helpertest.Fake
}

type setup struct {
	scenario    helpertest.Scenario
	sessionUser polkit.UID
	noDialogs   bool
}

func start(t *testing.T, s setup) *env {
	t.Helper()
	bus := dbustest.Start(t)
	h := &host{calls: make(chan call, 64), replies: make(chan portaldialogs.AuthReply), stop: make(chan struct{})}
	t.Cleanup(func() { close(h.stop) })
	hostConn := bus.Conn(t)
	if !s.noDialogs {
		if _, err := portaldialogs.NewDaemon(h).Export(hostConn); err != nil {
			t.Fatalf("export dialogs: %v", err)
		}
	}
	fake := helpertest.New(t, s.scenario)
	agentConn := bus.Conn(t)
	a := agent.New(agent.Config{
		Dialogs:     portaldialogs.NewClient(agentConn),
		Transport:   fake.Setuid(),
		UserName:    userName,
		SessionUser: s.sessionUser,
	})
	t.Cleanup(a.Close)
	if _, err := dbusx.Export(agentConn, polkit.AgentPath, dbusx.Interface{Name: polkit.AgentIface, Methods: a}); err != nil {
		t.Fatalf("export agent: %v", err)
	}
	return &env{t: t, agentName: agentConn.Names()[0], caller: bus.Conn(t), host: h, emitter: portaldialogs.NewEmitter(hostConn), fake: fake}
}

func unixUser(uid polkit.UID) polkit.Identity {
	return polkit.Identity{Kind: "unix-user", Details: map[string]dbus.Variant{"uid": dbus.MakeVariant(uint32(uid))}}
}

var details = map[string]string{"program": "/usr/bin/true", "command_line": "/usr/bin/true --now", "polkit.caller-pid": "4242", "user": "root"}

// begin calls BeginAuthentication over the bus; the returned channel
// receives its result.
func (e *env) begin(cookie string, ids ...polkit.Identity) <-chan error {
	e.t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- e.caller.Object(e.agentName, polkit.AgentPath).Call(polkit.AgentIface+".BeginAuthentication", 0,
			"org.example.test", "Authenticate to test", "dialog-password", details, cookie, ids).Err
	}()
	return done
}

func (e *env) cancel(cookie string) error {
	return e.caller.Object(e.agentName, polkit.AgentPath).Call(polkit.AgentIface+".CancelAuthentication", 0, cookie).Err
}

func (e *env) next() call {
	e.t.Helper()
	select {
	case c := <-e.host.calls:
		return c
	case <-time.After(timeout):
		e.t.Fatal("no dialog call within " + timeout.String())
		return call{}
	}
}

func (e *env) expect(method string) call {
	e.t.Helper()
	c := e.next()
	if c.method != method {
		e.t.Fatalf("dialog call %s %+v, want %s", c.method, c.req, method)
	}
	return c
}

func (e *env) reply(r portaldialogs.AuthReply) {
	e.t.Helper()
	select {
	case e.host.replies <- r:
	case <-time.After(timeout):
		e.t.Fatal("no Authenticate waiting for a reply")
	}
}

func result(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		t.Fatal("BeginAuthentication did not return within " + timeout.String())
		return nil
	}
}

func wantError(t *testing.T, err error, name, text string) {
	t.Helper()
	var derr dbus.Error
	if !errors.As(err, &derr) || derr.Name != name || !strings.Contains(err.Error(), text) {
		t.Fatalf("BeginAuthentication = %v, want %s with %q", err, name, text)
	}
}

func answer(password string) portaldialogs.AuthReply {
	return portaldialogs.AuthAnswer{Response: portaldialogs.NewResponse(password)}
}

func TestSuccess(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(bob), unixUser(alice))
	c := e.expect("Authenticate")
	want := portaldialogs.AuthRequest{
		Message: "Authenticate to test",
		Icon:    "dialog-password",
		Details: []portaldialogs.AuthDetail{
			{Label: "Program", Value: "/usr/bin/true"},
			{Label: "Command line", Value: "/usr/bin/true --now"},
			{Label: "Caller process", Value: "4242"},
		},
		Identities: []portaldialogs.AuthIdentity{{UID: uint32(bob), Name: "bob"}, {UID: uint32(alice), Name: helpertest.User}},
		Selected:   uint32(alice),
		Notes:      []portaldialogs.AuthNote{},
		Prompt:     helpertest.PasswordPrompt,
	}
	if !reflect.DeepEqual(c.req, want) {
		t.Errorf("Authenticate %+v, want %+v", c.req, want)
	}
	if !strings.HasPrefix(c.caller.Handle, string(polkit.AgentPath)+"/request/") || !c.caller.Modal {
		t.Errorf("caller %+v, want a modal request under the agent", c.caller)
	}
	e.reply(answer(helpertest.Password))
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	if d := e.expect("Dismiss"); d.caller.Handle != c.caller.Handle {
		t.Errorf("dismissed %s, want %s", d.caller.Handle, c.caller.Handle)
	}
	e.fake.AssertNoneLeft(t)
}

func TestRetryAfterFailure(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(alice))
	e.expect("Authenticate")
	e.reply(answer("wrong"))
	if c := e.expect("ShowAuthentication"); !reflect.DeepEqual(c.req.Notes, []portaldialogs.AuthNote{{Error: true, Text: helpertest.SorryError}}) {
		t.Errorf("notes after a wrong answer %+v", c.req.Notes)
	}
	c := e.expect("Authenticate")
	if !reflect.DeepEqual(c.req.Notes, []portaldialogs.AuthNote{{Error: true, Text: agent.FailedNote}}) || c.req.Selected != uint32(alice) {
		t.Errorf("retry %+v, want the same identity with %q", c.req, agent.FailedNote)
	}
	e.reply(answer(helpertest.Password))
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	if n := e.fake.Started(t); n != 2 {
		t.Errorf("%d helper sessions, want 2", n)
	}
	e.fake.AssertNoneLeft(t)
}

func TestIdentitySwitchMidPrompt(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: bob})
	done := e.begin("cookie-1", unixUser(alice), unixUser(bob))
	if c := e.expect("Authenticate"); c.req.Selected != uint32(bob) {
		t.Fatalf("first prompt for uid %d, want the session user %d", c.req.Selected, bob)
	}
	e.reply(portaldialogs.AuthSwitch{UID: uint32(alice)})
	if c := e.expect("Authenticate"); c.req.Selected != uint32(alice) || len(c.req.Notes) != 0 {
		t.Fatalf("after the switch %+v, want a fresh prompt for uid %d", c.req, alice)
	}
	e.reply(answer(helpertest.Password))
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	if n := e.fake.Started(t); n != 2 {
		t.Errorf("%d helper sessions, want 2", n)
	}
	e.fake.AssertNoneLeft(t)
}

func TestInfoOnlySuccess(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.InfoOnly, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(alice))
	c := e.expect("ShowAuthentication")
	if !reflect.DeepEqual(c.req.Notes, []portaldialogs.AuthNote{{Text: helpertest.FingerInfo}}) || c.req.Prompt != "" {
		t.Errorf("ShowAuthentication %+v, want the finger note and no prompt", c.req)
	}
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	e.fake.AssertNoneLeft(t)
}

func TestUserCancel(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Wait, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(alice))
	e.expect("Authenticate")
	e.reply(portaldialogs.AuthCancel{})
	wantError(t, result(t, done), polkit.ErrCancelled, "cancelled")
	e.expect("Dismiss")
	e.fake.AssertNoneLeft(t)
}

func TestUserCancelWhileNoPromptWaits(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.InfoWait, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(alice))
	c := e.expect("ShowAuthentication")
	if err := e.emitter.AuthAction(c.caller.Handle, portaldialogs.AuthCancel{}); err != nil {
		t.Fatalf("signal cancel: %v", err)
	}
	wantError(t, result(t, done), polkit.ErrCancelled, "cancelled")
	e.expect("Dismiss")
	e.fake.AssertNoneLeft(t)
}

func TestCancelAuthenticationRunning(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Wait, sessionUser: alice})
	done := e.begin("cookie-1", unixUser(alice))
	c := e.expect("Authenticate")
	if err := e.cancel("cookie-1"); err != nil {
		t.Fatalf("CancelAuthentication: %v", err)
	}
	wantError(t, result(t, done), polkit.ErrCancelled, "cancelled")
	if d := e.expect("Dismiss"); d.caller.Handle != c.caller.Handle {
		t.Errorf("dismissed %s, want %s", d.caller.Handle, c.caller.Handle)
	}
	e.fake.AssertNoneLeft(t)
}

func TestCancelAuthenticationQueued(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice})
	first := e.begin("cookie-1", unixUser(alice))
	running := e.expect("Authenticate")
	queued := e.begin("cookie-2", unixUser(alice))
	third := e.begin("cookie-3", unixUser(alice))
	deadline := time.Now().Add(timeout)
	for {
		err := e.cancel("cookie-2")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CancelAuthentication(cookie-2): %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	wantError(t, result(t, queued), polkit.ErrCancelled, "cancelled")

	e.reply(answer(helpertest.Password))
	if err := result(t, first); err != nil {
		t.Fatalf("first BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	next := e.expect("Authenticate")
	if next.caller.Handle == running.caller.Handle {
		t.Errorf("the third request reused handle %s", next.caller.Handle)
	}
	if err := e.cancel("cookie-3"); err != nil {
		t.Fatalf("CancelAuthentication(cookie-3): %v", err)
	}
	wantError(t, result(t, third), polkit.ErrCancelled, "cancelled")
	e.expect("Dismiss")
	if n := e.fake.Started(t); n != 2 {
		t.Errorf("%d helper sessions, want 2: the cancelled request never ran", n)
	}
	e.fake.AssertNoneLeft(t)
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	group := polkit.Identity{Kind: "unix-group", Details: map[string]dbus.Variant{"gid": dbus.MakeVariant(uint32(10))}}
	tests := []struct {
		name string
		ids  []polkit.Identity
		want string
	}{
		{name: "an unknown identity kind", ids: []polkit.Identity{unixUser(alice), group}, want: `identity kind "unix-group"`},
		{name: "no identity", ids: []polkit.Identity{}, want: "no identity offered"},
		{name: "a uid with no user", ids: []polkit.Identity{unixUser(4711)}, want: "no user 4711"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice})
			wantError(t, result(t, e.begin("cookie-1", tc.ids...)), polkit.ErrFailed, tc.want)
			if n := e.fake.Started(t); n != 0 {
				t.Errorf("%d helper sessions for a refused request", n)
			}
		})
	}
}

func TestDialogsAbsentFails(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice, noDialogs: true})
	wantError(t, result(t, e.begin("cookie-1", unixUser(alice))), polkit.ErrFailed, portaldialogs.ServiceName)
	if n := e.fake.Started(t); n != 0 {
		t.Errorf("%d helper sessions without the dialogs, want none", n)
	}
}
