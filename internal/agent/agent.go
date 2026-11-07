// Package agent answers polkitd's BeginAuthentication and
// CancelAuthentication: one request at a time, first in first out, each
// a PAM conversation through polkit's helper shown in the shell's
// authentication dialog.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/helper"
	"github.com/stubbedev/lys-polkit/internal/locale"
	"github.com/stubbedev/lys-polkit/internal/polkit"
)

// dismissTimeout bounds taking the dialog down once a request ended.
const dismissTimeout = 5 * time.Second

// Dialogs is the shell's authentication dialog
// (portaldialogs.Client).
type Dialogs interface {
	Authenticate(context.Context, portaldialogs.Caller, portaldialogs.AuthRequest) (portaldialogs.AuthReply, error)
	ShowAuthentication(context.Context, portaldialogs.Caller, portaldialogs.AuthRequest) error
	WatchAuthentication(ctx context.Context, handle string) (*portaldialogs.AuthWatch, error)
	Dismiss(ctx context.Context, handle string) error
}

// Config is what an Agent needs.
type Config struct {
	Dialogs   Dialogs
	Transport helper.Transport
	// UserName is the login name of a uid, which the helper is given.
	UserName func(context.Context, polkit.UID) (string, error)
	// SessionUser is the uid of the session's user, the identity chosen
	// first when it is offered.
	SessionUser polkit.UID
	Actions     interface {
		Lookup(ctx context.Context, id string) (polkit.ActionDescription, error)
	}
	ProcessName func(polkit.PID) (string, error)
	Catalog     locale.Catalog
	Authorized  func()
}

// Agent is the exported org.freedesktop.PolicyKit1.AuthenticationAgent.
type Agent struct {
	cfg  Config
	base context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	closed  bool
	tail    chan struct{}
	pending map[string]context.CancelFunc
	serial  uint64
	running sync.WaitGroup
}

// New makes an Agent; Close ends its requests.
func New(cfg Config) *Agent {
	base, stop := context.WithCancel(context.Background())
	tail := make(chan struct{})
	close(tail)
	return &Agent{cfg: cfg, base: base, stop: stop, tail: tail, pending: map[string]context.CancelFunc{}}
}

// Close cancels every request, refuses new ones, and returns once each
// has ended and its helper is released.
func (a *Agent) Close() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.stop()
	a.running.Wait()
}

func failed(format string, args ...any) *dbus.Error {
	return dbus.NewError(polkit.ErrFailed, []any{fmt.Sprintf(format, args...)})
}

var cancelled = dbus.NewError(polkit.ErrCancelled, []any{"Authentication was cancelled"})

// BeginAuthentication authenticates one of identities for cookie and
// returns once the helper reported success to polkitd, the user or
// polkitd cancelled, or it failed.
func (a *Agent) BeginAuthentication(actionID, message, iconName string, details map[string]string, cookie string, identities []polkit.Identity) *dbus.Error {
	uids, err := polkit.ParseIdentities(identities)
	if err != nil {
		return failed("%v", err)
	}
	shown, err := polkit.ParseDetails(details)
	if err != nil {
		return failed("%v", err)
	}
	j, derr := a.enqueue(cookie)
	if derr != nil {
		return derr
	}
	defer j.done()
	select {
	case <-j.turn:
	case <-j.ctx.Done():
		return cancelled
	}
	c := conversation{
		cfg:    a.cfg,
		cookie: cookie,
		caller: portaldialogs.Caller{Handle: j.handle, Modal: true},
		base:   portaldialogs.AuthRequest{Message: message, Icon: iconName, Details: a.detailLines(j.ctx, actionID, shown)},
	}
	if err := c.identities(j.ctx, uids); err != nil {
		if j.ctx.Err() != nil {
			return cancelled
		}
		return failed("%v", err)
	}
	if derr := c.run(j.ctx); derr != nil {
		return derr
	}
	a.cfg.Authorized()
	return nil
}

// CancelAuthentication cancels the request for cookie, queued or
// running.
func (a *Agent) CancelAuthentication(cookie string) *dbus.Error {
	a.mu.Lock()
	cancel, ok := a.pending[cookie]
	a.mu.Unlock()
	if !ok {
		return failed("no authentication pending for that cookie")
	}
	cancel()
	return nil
}

// job is one request's place in the queue: turn closes when the
// requests before it ended, handle names its dialog, and done ends it.
type job struct {
	ctx    context.Context
	turn   <-chan struct{}
	handle string
	done   func()
}

// enqueue puts a request for cookie at the end of the queue. A request
// that ends before its turn, cancelled while queued, passes its turn
// on once the requests before it ended.
func (a *Agent) enqueue(cookie string) (job, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return job{}, failed("the agent is shutting down")
	}
	if _, ok := a.pending[cookie]; ok {
		return job{}, failed("an authentication for that cookie is already pending")
	}
	ctx, cancel := context.WithCancel(a.base)
	prev, mine := a.tail, make(chan struct{})
	a.tail = mine
	a.pending[cookie] = cancel
	a.serial++
	a.running.Add(1)
	return job{
		ctx:    ctx,
		turn:   prev,
		handle: string(polkit.AgentPath) + "/request/" + strconv.FormatUint(a.serial, 10),
		done: func() {
			a.mu.Lock()
			delete(a.pending, cookie)
			a.mu.Unlock()
			cancel()
			a.running.Done()
			go func() {
				<-prev
				close(mine)
			}()
		},
	}, nil
}

func (a *Agent) detailLines(ctx context.Context, actionID string, d polkit.Details) []portaldialogs.AuthDetail {
	tr := a.cfg.Catalog.Get
	lines := []portaldialogs.AuthDetail{{Label: tr(locale.Action), Value: actionID}}
	if action, err := a.cfg.Actions.Lookup(ctx, actionID); err != nil {
		slog.Error("describe polkit action", "action", actionID, "err", err)
	} else {
		if action.VendorName != "" {
			lines = append(lines, portaldialogs.AuthDetail{Label: tr(locale.Vendor), Value: action.VendorName})
		}
		if action.VendorURL != "" {
			lines = append(lines, portaldialogs.AuthDetail{Label: tr(locale.Website), Value: action.VendorURL})
		}
	}
	if d.Program != "" {
		lines = append(lines, portaldialogs.AuthDetail{Label: tr(locale.Program), Value: d.Program})
	}
	if d.CommandLine != "" {
		lines = append(lines, portaldialogs.AuthDetail{Label: tr(locale.CommandLine), Value: d.CommandLine})
	}
	if d.CallerPID != 0 {
		lines = append(lines, portaldialogs.AuthDetail{Label: tr(locale.Caller), Value: a.caller(d.CallerPID)})
	}
	return lines
}

func (a *Agent) caller(pid polkit.PID) string {
	id := strconv.FormatUint(uint64(pid), 10)
	name, err := a.cfg.ProcessName(pid)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Error("name the polkit caller", "pid", pid, "err", err)
		}
		return id
	}
	return name + " (" + id + ")"
}

// conversation is one request's dialog and the helper sessions behind
// it.
type conversation struct {
	cfg    Config
	cookie string
	caller portaldialogs.Caller
	base   portaldialogs.AuthRequest
	names  map[polkit.UID]string
}

func (c *conversation) identities(ctx context.Context, uids []polkit.UID) error {
	c.names = make(map[polkit.UID]string, len(uids))
	c.base.Identities = make([]portaldialogs.AuthIdentity, 0, len(uids))
	c.base.Selected = uint32(uids[0])
	for _, uid := range uids {
		name, err := c.cfg.UserName(ctx, uid)
		if err != nil {
			return fmt.Errorf("identity uid %d: %w", uid, err)
		}
		c.names[uid] = name
		c.base.Identities = append(c.base.Identities, portaldialogs.AuthIdentity{UID: uint32(uid), Name: name})
		if uid == c.cfg.SessionUser {
			c.base.Selected = uint32(uid)
		}
	}
	return nil
}

// outcome is how one helper session ended: the request finished, the
// helper said FAILURE, or the user chose another identity.
type outcome int

const (
	finished outcome = iota
	rejected
	switched
)

func (c *conversation) run(ctx context.Context) *dbus.Error {
	watch, err := c.cfg.Dialogs.WatchAuthentication(ctx, c.caller.Handle)
	if err != nil {
		return failed("authentication dialog: %v", err)
	}
	defer c.dismiss(ctx)
	defer func() {
		if err := watch.Close(); err != nil {
			slog.Error("authentication dialog watch", "handle", c.caller.Handle, "err", err)
		}
	}()
	var notes []portaldialogs.AuthNote
	for {
		o, result := c.attempt(ctx, watch, notes)
		switch o {
		case finished:
			return result
		case rejected:
			notes = []portaldialogs.AuthNote{{Error: true, Text: c.cfg.Catalog.Get(locale.AuthenticationFailed)}}
		case switched:
			notes = nil
		}
	}
}

func (c *conversation) dismiss(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dismissTimeout)
	defer cancel()
	if err := c.cfg.Dialogs.Dismiss(ctx, c.caller.Handle); err != nil {
		slog.Error("dismiss authentication dialog", "handle", c.caller.Handle, "err", err)
	}
}

// attempt runs one helper session for the selected identity, showing
// notes first.
func (c *conversation) attempt(ctx context.Context, watch *portaldialogs.AuthWatch, notes []portaldialogs.AuthNote) (outcome, *dbus.Error) {
	uid := polkit.UID(c.base.Selected)
	s, err := helper.Start(ctx, c.cfg.Transport, c.names[uid], c.cookie)
	if err != nil {
		return finished, failed("%v", err)
	}
	defer s.Cancel()
	for {
		select {
		case <-ctx.Done():
			return finished, cancelled
		case action, ok := <-watch.Actions():
			if !ok {
				if ctx.Err() != nil {
					return finished, cancelled
				}
				return finished, failed("authentication dialog: %v", watchEnded(watch))
			}
			return c.act(action)
		case ev, ok := <-s.Events():
			if !ok {
				return finished, failed("helper closed without a verdict")
			}
			switch ev := ev.(type) {
			case helper.Prompt:
				reply, err := c.cfg.Dialogs.Authenticate(ctx, c.caller, c.request(notes, ev))
				switch {
				case ctx.Err() != nil:
					return finished, cancelled
				case err != nil:
					return finished, failed("authentication dialog: %v", err)
				}
				if answer, ok := reply.(portaldialogs.AuthAnswer); ok {
					if err := s.Respond(answer.Response.Reveal()); err != nil {
						return finished, failed("%v", err)
					}
					continue
				}
				action, ok := reply.(portaldialogs.AuthAction)
				if !ok {
					return finished, failed("authentication dialog replied a %T", reply)
				}
				return c.act(action)
			case helper.Info:
				notes = append(notes, portaldialogs.AuthNote{Text: ev.Text})
				if err := c.cfg.Dialogs.ShowAuthentication(ctx, c.caller, c.request(notes, helper.Prompt{})); err != nil {
					return finished, failed("authentication dialog: %v", err)
				}
			case helper.Error:
				notes = append(notes, portaldialogs.AuthNote{Error: true, Text: ev.Text})
				if err := c.cfg.Dialogs.ShowAuthentication(ctx, c.caller, c.request(notes, helper.Prompt{})); err != nil {
					return finished, failed("authentication dialog: %v", err)
				}
			case helper.Success:
				return finished, nil
			case helper.Failure:
				if ev.Cause != nil {
					return finished, failed("%v", ev)
				}
				return rejected, nil
			}
		}
	}
}

// act applies what the user did other than answering: a cancel ends
// the request, a switch selects another identity for the next attempt.
func (c *conversation) act(action portaldialogs.AuthAction) (outcome, *dbus.Error) {
	switch action := action.(type) {
	case portaldialogs.AuthCancel:
		return finished, cancelled
	case portaldialogs.AuthSwitch:
		if _, ok := c.names[polkit.UID(action.UID)]; !ok {
			return finished, failed("authentication dialog chose uid %d, which was not offered", action.UID)
		}
		c.base.Selected = action.UID
		return switched, nil
	default:
		return finished, failed("authentication dialog reported a %T", action)
	}
}

func watchEnded(watch *portaldialogs.AuthWatch) error {
	if err := watch.Close(); err != nil {
		return err
	}
	return errors.New("watch ended")
}

func (c *conversation) request(notes []portaldialogs.AuthNote, p helper.Prompt) portaldialogs.AuthRequest {
	r := c.base
	r.Notes = notes
	r.Prompt = p.Text
	r.Echo = p.Echo
	return r
}
