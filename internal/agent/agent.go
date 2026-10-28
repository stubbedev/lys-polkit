// Package agent answers polkitd's BeginAuthentication and
// CancelAuthentication: one request at a time, first in first out, each
// a PAM conversation through polkit's helper shown in the shell's
// authentication dialog.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/helper"
	"github.com/stubbedev/lys-polkit/internal/polkit"
)

// FailedNote is the note a new attempt shows after the helper's
// FAILURE.
const FailedNote = "Authentication failed"

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
	UserName func(polkit.UID) (string, error)
	// SessionUser is the uid of the session's user, the identity chosen
	// first when it is offered.
	SessionUser polkit.UID
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
func (a *Agent) BeginAuthentication(_, message, iconName string, details map[string]string, cookie string, identities []polkit.Identity) *dbus.Error {
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
		base:   portaldialogs.AuthRequest{Message: message, Icon: iconName, Details: detailLines(shown)},
	}
	if err := c.identities(uids); err != nil {
		return failed("%v", err)
	}
	return c.run(j.ctx)
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

func detailLines(d polkit.Details) []portaldialogs.AuthDetail {
	var lines []portaldialogs.AuthDetail
	if d.Program != "" {
		lines = append(lines, portaldialogs.AuthDetail{Label: "Program", Value: d.Program})
	}
	if d.CommandLine != "" {
		lines = append(lines, portaldialogs.AuthDetail{Label: "Command line", Value: d.CommandLine})
	}
	if d.CallerPID != 0 {
		lines = append(lines, portaldialogs.AuthDetail{Label: "Caller process", Value: strconv.FormatUint(uint64(d.CallerPID), 10)})
	}
	return lines
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

func (c *conversation) identities(uids []polkit.UID) error {
	c.names = make(map[polkit.UID]string, len(uids))
	c.base.Identities = make([]portaldialogs.AuthIdentity, 0, len(uids))
	c.base.Selected = uint32(uids[0])
	for _, uid := range uids {
		name, err := c.cfg.UserName(uid)
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
			notes = []portaldialogs.AuthNote{{Error: true, Text: FailedNote}}
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
