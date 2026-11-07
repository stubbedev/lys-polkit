package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/agent"
	"github.com/stubbedev/lys-polkit/internal/helper/helpertest"
	"github.com/stubbedev/lys-polkit/internal/locale"
	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/polkit/polkittest"
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

func userName(_ context.Context, uid polkit.UID) (string, error) {
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
	t          *testing.T
	agentName  string
	caller     *dbus.Conn
	host       *host
	emitter    *portaldialogs.Emitter
	fake       *helpertest.Fake
	authority  *polkittest.Authority
	release    func()
	bus        *dbustest.Bus
	changes    chan struct{}
	authorized *atomic.Int32
}

type setup struct {
	scenario    helpertest.Scenario
	sessionUser polkit.UID
	noDialogs   bool
	userName    func(context.Context, polkit.UID) (string, error)
	failActions bool
	catalog     locale.Catalog
}

const callerPID = 4242

func processName(pid polkit.PID) (string, error) {
	if pid == callerPID {
		return "pkexec", nil
	}
	return "", fmt.Errorf("process %d: %w", pid, fs.ErrNotExist)
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
	auth := polkittest.New()
	auth.Actions = []polkit.ActionDescription{polkittest.ExecAction}
	auth.FailActions = s.failActions
	release := auth.Serve(t, bus.Conn(t))
	fake := helpertest.New(t, s.scenario)
	agentConn := bus.Conn(t)
	lookup := s.userName
	if lookup == nil {
		lookup = userName
	}
	actions := polkit.NewActions(agentConn, "C")
	var authorized atomic.Int32
	a := agent.New(agent.Config{
		Dialogs:     portaldialogs.NewClient(agentConn),
		Transport:   fake.Setuid(),
		UserName:    lookup,
		SessionUser: s.sessionUser,
		Actions:     actions,
		ProcessName: processName,
		Catalog:     s.catalog,
		Authorized:  func() { authorized.Add(1) },
	})
	t.Cleanup(a.Close)
	changes := make(chan struct{}, 16)
	changed := func() {
		actions.Forget()
		changes <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- polkit.Serve(ctx, agentConn, polkit.Registration{Subject: polkit.SessionSubject("c2"), Locale: "C"}, a, changed)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(timeout):
			t.Error("Serve did not return within " + timeout.String())
		}
	})
	e := &env{t: t, agentName: agentConn.Names()[0], caller: bus.Conn(t), host: h, emitter: portaldialogs.NewEmitter(hostConn), fake: fake, authority: auth, release: release, bus: bus, changes: changes, authorized: &authorized}
	e.registered(auth)
	e.changed()
	return e
}

func (e *env) registered(auth *polkittest.Authority) {
	e.t.Helper()
	select {
	case ev := <-auth.Events:
		if !ev.Register {
			e.t.Fatalf("Authority saw %+v, want the agent registered", ev)
		}
	case <-time.After(timeout):
		e.t.Fatal("the agent did not register within " + timeout.String())
	}
}

func unixUser(uid polkit.UID) polkit.Identity {
	return polkit.Identity{Kind: "unix-user", Details: map[string]dbus.Variant{"uid": dbus.MakeVariant(uint32(uid))}}
}

var details = map[string]string{"program": "/usr/bin/true", "command_line": "/usr/bin/true --now", "polkit.caller-pid": strconv.Itoa(callerPID), "user": "root"}

// begin calls BeginAuthentication over the bus; the returned channel
// receives its result.
func (e *env) begin(cookie string, ids ...polkit.Identity) <-chan error {
	e.t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- e.caller.Object(e.agentName, polkit.AgentPath).Call(polkit.AgentIface+".BeginAuthentication", 0,
			polkittest.ExecAction.ActionID, "Authenticate to test", "dialog-password", details, cookie, ids).Err
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
			{Label: "Action", Value: polkittest.ExecAction.ActionID},
			{Label: "Vendor", Value: polkittest.ExecAction.VendorName},
			{Label: "Website", Value: polkittest.ExecAction.VendorURL},
			{Label: "Program", Value: "/usr/bin/true"},
			{Label: "Command line", Value: "/usr/bin/true --now"},
			{Label: "Caller", Value: "pkexec (4242)"},
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
	if !reflect.DeepEqual(c.req.Notes, []portaldialogs.AuthNote{{Error: true, Text: string(locale.AuthenticationFailed)}}) || c.req.Selected != uint32(alice) {
		t.Errorf("retry %+v, want the same identity with %q", c.req, locale.AuthenticationFailed)
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

func TestCancelAuthenticationDuringUserLookup(t *testing.T) {
	t.Parallel()

	looking := make(chan struct{})
	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice, userName: func(ctx context.Context, _ polkit.UID) (string, error) {
		close(looking)
		<-ctx.Done()
		return "", ctx.Err()
	}})
	done := e.begin("cookie-1", unixUser(alice))
	select {
	case <-looking:
	case <-time.After(timeout):
		t.Fatal("the user lookup never started")
	}
	if err := e.cancel("cookie-1"); err != nil {
		t.Fatalf("CancelAuthentication: %v", err)
	}
	wantError(t, result(t, done), polkit.ErrCancelled, "cancelled")
	if n := e.fake.Started(t); n != 0 {
		t.Errorf("%d helpers started, want none", n)
	}
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

func (e *env) changed() {
	e.t.Helper()
	select {
	case <-e.changes:
	case <-time.After(timeout):
		e.t.Fatal("Serve reported no change within " + timeout.String())
	}
}

func (e *env) succeed() portaldialogs.AuthRequest {
	e.t.Helper()
	done := e.begin(helpertest.Cookie, unixUser(alice))
	c := e.expect("ShowAuthentication")
	if err := result(e.t, done); err != nil {
		e.t.Fatalf("BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	return c.req
}

func TestActionsEnumeratedOncePerAuthority(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.InfoOnly, sessionUser: alice})
	for range 5 {
		e.succeed()
	}
	if n := e.authority.Calls("EnumerateActions"); n != 1 {
		t.Errorf("EnumerateActions called %d times for 5 requests, want 1", n)
	}

	e.release()
	restarted := polkittest.New()
	restarted.Actions = []polkit.ActionDescription{polkittest.ExecAction}
	restarted.Serve(t, e.bus.Conn(t))
	e.registered(restarted)
	e.changed()
	for range 3 {
		req := e.succeed()
		if req.Details[1].Value != polkittest.ExecAction.VendorName {
			t.Errorf("details after the restart %+v, want the vendor", req.Details)
		}
	}
	if n := restarted.Calls("EnumerateActions"); n != 1 {
		t.Errorf("restarted polkitd: EnumerateActions called %d times for 3 requests, want 1", n)
	}
}

func TestFailedEnumerationShowsTheActionID(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.InfoOnly, sessionUser: alice, failActions: true})
	want := []portaldialogs.AuthDetail{
		{Label: "Action", Value: polkittest.ExecAction.ActionID},
		{Label: "Program", Value: "/usr/bin/true"},
		{Label: "Command line", Value: "/usr/bin/true --now"},
		{Label: "Caller", Value: "pkexec (4242)"},
	}
	if got := e.succeed().Details; !reflect.DeepEqual(got, want) {
		t.Errorf("details %+v, want %+v", got, want)
	}
}

func TestGoneCallerShowsItsPID(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.InfoOnly, sessionUser: alice})
	done := make(chan error, 1)
	go func() {
		done <- e.caller.Object(e.agentName, polkit.AgentPath).Call(polkit.AgentIface+".BeginAuthentication", 0,
			polkittest.ExecAction.ActionID, "Authenticate to test", "", map[string]string{"polkit.caller-pid": "4711"}, "cookie-1", []polkit.Identity{unixUser(alice)}).Err
	}()
	c := e.expect("ShowAuthentication")
	if last := c.req.Details[len(c.req.Details)-1]; last != (portaldialogs.AuthDetail{Label: "Caller", Value: "4711"}) {
		t.Errorf("caller detail %+v, want the pid alone", last)
	}
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
}

func TestLabelsFollowTheCatalog(t *testing.T) {
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "de_DE.UTF-8")
	po := `msgid ""
msgstr "Plural-Forms: nplurals=2; plural=(n != 1);\n"

msgid "Action"
msgstr "Aktion (Test)"

msgid "Vendor"
msgstr "Anbieter (Test)"

msgid "Website"
msgstr "Webseite (Test)"

msgid "Program"
msgstr "Programm (Test)"

msgid "Command line"
msgstr "Befehlszeile (Test)"

msgid "Caller"
msgstr "Aufrufer (Test)"
`
	catalog, err := locale.Load(fstest.MapFS{"de/LC_MESSAGES/" + locale.Domain + ".po": {Data: []byte(po)}})
	if err != nil {
		t.Fatalf("load the fixture catalog: %v", err)
	}
	e := start(t, setup{scenario: helpertest.InfoOnly, sessionUser: alice, catalog: catalog})
	var labels []string
	for _, d := range e.succeed().Details {
		labels = append(labels, d.Label)
	}
	want := []string{"Aktion (Test)", "Anbieter (Test)", "Webseite (Test)", "Programm (Test)", "Befehlszeile (Test)", "Aufrufer (Test)"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels %q, want %q", labels, want)
	}
}

func TestOnlySuccessReportsAnAuthorization(t *testing.T) {
	t.Parallel()

	e := start(t, setup{scenario: helpertest.Answer, sessionUser: alice})
	done := e.begin(helpertest.Cookie, unixUser(alice))
	e.expect("Authenticate")
	e.reply(portaldialogs.AuthCancel{})
	wantError(t, result(t, done), polkit.ErrCancelled, "cancelled")
	e.expect("Dismiss")
	if n := e.authorized.Load(); n != 0 {
		t.Errorf("%d authorizations reported after a cancel, want none", n)
	}

	done = e.begin(helpertest.Cookie, unixUser(alice))
	e.expect("Authenticate")
	e.reply(answer("wrong"))
	e.expect("ShowAuthentication")
	e.expect("Authenticate")
	e.reply(answer(helpertest.Password))
	if err := result(t, done); err != nil {
		t.Fatalf("BeginAuthentication: %v", err)
	}
	e.expect("Dismiss")
	if n := e.authorized.Load(); n != 1 {
		t.Errorf("%d authorizations reported after one success, want 1", n)
	}
}
