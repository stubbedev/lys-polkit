package tracker_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/polkit"
	"github.com/stubbedev/lys-polkit/internal/polkit/polkittest"
	"github.com/stubbedev/lys-polkit/internal/tracker"
	"github.com/stubbedev/lys-polkit/retained"
)

const timeout = 5 * time.Second

var epoch = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
	armed  chan time.Duration
}

type timer struct {
	c       chan time.Time
	stopped bool
}

func (t *timer) C() <-chan time.Time { return t.c }

func (t *timer) Stop() bool {
	t.stopped = true
	return true
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) NewTimer(d time.Duration) tracker.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{c: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	c.armed <- d
	return t
}

func (c *clock) fire(t *testing.T, at time.Time) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
	last := c.timers[len(c.timers)-1]
	if last.stopped {
		t.Fatal("the armed timer was stopped")
	}
	last.c <- at
}

func (c *clock) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type agent struct{}

func (agent) BeginAuthentication(string, string, string, map[string]string, string, []polkit.Identity) *dbus.Error {
	return nil
}

func (agent) CancelAuthentication(string) *dbus.Error { return nil }

type env struct {
	t         *testing.T
	bus       *dbustest.Bus
	authority *polkittest.Authority
	release   func()
	tracker   *tracker.Tracker
	clock     *clock
	published chan []retained.Authorization
	client    *retained.Client
	spare     *dbus.Conn
}

var subject = polkit.SessionSubject("c2")

func start(t *testing.T) *env {
	t.Helper()
	bus := dbustest.Start(t)
	auth := polkittest.New()
	release := auth.Serve(t, bus.Conn(t))
	system := bus.Conn(t)
	session := bus.Conn(t)
	spare := bus.Conn(t)
	temporary := polkit.NewTemporary(system, subject)
	server, unserve, err := retained.Serve(session, temporary)
	if err != nil {
		t.Fatalf("serve retained: %v", err)
	}
	t.Cleanup(unserve)
	e := &env{t: t, bus: bus, authority: auth, release: release, clock: &clock{now: epoch, armed: make(chan time.Duration, 16)}, published: make(chan []retained.Authorization, 16), client: retained.NewClient(bus.Conn(t)), spare: spare}
	e.tracker = tracker.New(tracker.Config{
		List: temporary.List,
		Publish: func(list []retained.Authorization) error {
			err := server.Publish(list)
			e.published <- list
			return err
		},
		Clock: e.clock,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { e.tracker.Run(ctx) })
	served := make(chan error, 1)
	go func() {
		served <- polkit.Serve(ctx, system, polkit.Registration{Subject: subject, Locale: "C"}, agent{}, e.tracker.Refresh)
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(timeout):
			t.Error("Serve did not return within " + timeout.String())
		}
	})
	return e
}

func (e *env) next() []retained.Authorization {
	e.t.Helper()
	select {
	case list := <-e.published:
		return list
	case <-time.After(timeout):
		e.t.Fatal("nothing published within " + timeout.String())
		return nil
	}
}

func (e *env) armed() time.Duration {
	e.t.Helper()
	select {
	case d := <-e.clock.armed:
		return d
	case <-time.After(timeout):
		e.t.Fatal("no timer armed within " + timeout.String())
		return 0
	}
}

func keep(id string, obtained, expires time.Time) polkit.TemporaryAuthorization {
	return polkit.TemporaryAuthorization{ID: id, ActionID: "org.example.keep", Subject: subject, Obtained: uint64(obtained.Unix()), Expires: uint64(expires.Unix())}
}

func want(id string, obtained, expires time.Time) retained.Authorization {
	return retained.Authorization{ID: id, ActionID: "org.example.keep", Obtained: obtained, Expires: expires}
}

func TestNoAuthorizationsArmNoTimerAndCallOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	e := start(t)
	if list := e.next(); len(list) != 0 {
		t.Fatalf("published %+v, want none", list)
	}
	if n := e.authority.Calls("EnumerateTemporaryAuthorizations"); n != 1 {
		t.Errorf("%d enumerations after the registration, want 1", n)
	}
	for range 3 {
		e.tracker.Refresh()
		e.next()
	}
	if n := e.authority.Calls("EnumerateTemporaryAuthorizations"); n != 4 {
		t.Errorf("%d enumerations for the registration and 3 refreshes, want 4", n)
	}
	if n := e.clock.count(); n != 0 {
		t.Errorf("%d timers armed with no authorization, want none", n)
	}
	if list, err := e.client.Authorizations(t.Context()); err != nil || len(list) != 0 {
		t.Errorf("Authorizations = %+v, %v; want none", list, err)
	}
}

func TestListFollowsChangedAndExpiry(t *testing.T) {
	t.Parallel()

	e := start(t)
	e.next()
	watcher := e.bus.Conn(t)
	signals := make(chan *dbus.Signal, 16)
	watcher.Signal(signals)
	if err := watcher.AddMatchSignalContext(t.Context(), retained.ChangedMatch()...); err != nil {
		t.Fatalf("watch: %v", err)
	}

	early, late := epoch.Add(4*time.Minute), epoch.Add(5*time.Minute)
	e.authority.SetTemporary([]polkit.TemporaryAuthorization{keep("tmpauthz1", epoch, late), keep("tmpauthz0", epoch.Add(-time.Minute), early)})
	if err := e.authority.EmitChanged(); err != nil {
		t.Fatalf("emit Changed: %v", err)
	}
	wantList := []retained.Authorization{want("tmpauthz0", epoch.Add(-time.Minute), early), want("tmpauthz1", epoch, late)}
	if got := e.next(); !reflect.DeepEqual(got, wantList) {
		t.Fatalf("published %+v, want %+v", got, wantList)
	}
	if d := e.armed(); d != 4*time.Minute+time.Second {
		t.Errorf("timer armed for %v, want the earliest expiry and a second", d)
	}
	if got := changed(t, signals); !reflect.DeepEqual(got, wantList) {
		t.Errorf("PropertiesChanged carried %+v, want %+v", got, wantList)
	}
	if got, err := e.client.Authorizations(t.Context()); err != nil || !reflect.DeepEqual(got, wantList) {
		t.Errorf("Authorizations = %+v, %v; want %+v", got, err, wantList)
	}

	e.authority.SetTemporary([]polkit.TemporaryAuthorization{keep("tmpauthz1", epoch, late)})
	e.clock.fire(t, early.Add(time.Second))
	if got := e.next(); !reflect.DeepEqual(got, wantList[1:]) {
		t.Fatalf("after the first expiry published %+v, want %+v", got, wantList[1:])
	}
	if got := changed(t, signals); !reflect.DeepEqual(got, wantList[1:]) {
		t.Errorf("PropertiesChanged after the first expiry carried %+v, want %+v", got, wantList[1:])
	}
	if d := e.armed(); d != time.Minute {
		t.Errorf("timer armed for %v, want the next expiry and a second", d)
	}

	e.clock.fire(t, late.Add(time.Second))
	if got := e.next(); !reflect.DeepEqual(got, wantList[1:]) {
		t.Fatalf("published %+v while polkitd still holds it, want %+v", got, wantList[1:])
	}
	if d := e.armed(); d != time.Second {
		t.Errorf("timer re-armed for %v past the reported expiry, want a second", d)
	}
	e.authority.SetTemporary(nil)
	e.clock.fire(t, late.Add(2*time.Second))
	if got := e.next(); len(got) != 0 {
		t.Fatalf("after the last expiry published %+v, want none", got)
	}
	if got := changed(t, signals); len(got) != 0 {
		t.Errorf("PropertiesChanged carried %+v, want the empty list: an unchanged list emits nothing", got)
	}
	if n := e.clock.count(); n != 3 {
		t.Errorf("%d timers armed, want 3: none once the list is empty", n)
	}
}

func TestRestartedPolkitdIsReadAgain(t *testing.T) {
	t.Parallel()

	e := start(t)
	e.next()
	e.release()
	restarted := polkittest.New()
	restarted.SetTemporary([]polkit.TemporaryAuthorization{keep("tmpauthz0", epoch, epoch.Add(time.Minute))})
	restarted.Serve(t, e.spare)
	if got, wantList := e.next(), []retained.Authorization{want("tmpauthz0", epoch, epoch.Add(time.Minute))}; !reflect.DeepEqual(got, wantList) {
		t.Fatalf("after the restart published %+v, want %+v", got, wantList)
	}
	if n := restarted.Calls("EnumerateTemporaryAuthorizations"); n != 1 {
		t.Errorf("restarted polkitd: %d enumerations, want 1", n)
	}
}

func TestRevokeForwardsToPolkitd(t *testing.T) {
	t.Parallel()

	e := start(t)
	e.next()
	e.authority.SetTemporary([]polkit.TemporaryAuthorization{keep("tmpauthz0", epoch, epoch.Add(time.Minute)), keep("tmpauthz1", epoch, epoch.Add(2*time.Minute))})
	e.tracker.Refresh()
	if got := e.next(); len(got) != 2 {
		t.Fatalf("published %+v, want two", got)
	}

	if err := e.client.Revoke(t.Context(), "tmpauthz0"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := e.next(); len(got) != 1 || got[0].ID != "tmpauthz1" {
		t.Fatalf("after Revoke published %+v, want tmpauthz1 alone", got)
	}
	if err := e.client.Revoke(t.Context(), "tmpauthz9"); err == nil {
		t.Error("Revoke of an unknown id succeeded")
	}
	if err := e.client.RevokeAll(t.Context()); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if got := e.next(); len(got) != 0 {
		t.Fatalf("after RevokeAll published %+v, want none", got)
	}
	if n := e.authority.Calls("RevokeTemporaryAuthorizations"); n != 1 {
		t.Errorf("%d RevokeTemporaryAuthorizations, want 1", n)
	}
}

func changed(t *testing.T, signals <-chan *dbus.Signal) []retained.Authorization {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case sig := <-signals:
			list, ok, err := retained.Changed(sig)
			if err != nil {
				t.Fatalf("decode %s: %v", sig.Name, err)
			}
			if ok {
				return list
			}
		case <-deadline:
			t.Fatal("no PropertiesChanged within " + timeout.String())
			return nil
		}
	}
}
