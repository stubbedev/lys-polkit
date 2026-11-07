// Command fakedialogs stands in for lys-shell's dialogs in the NixOS VM
// test: it serves dev.stubbe.lys.PortalDialogs1 on the session bus and
// answers each authentication's prompts from a script, one line per
// prompt ("answer <text>" or "cancel"), logging every call except the
// answers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

func main() {
	script := flag.String("script", "", "file of prompt answers, one per line")
	logPath := flag.String("log", "", "file the calls are appended to")
	flag.Parse()
	if *script == "" || *logPath == "" {
		fmt.Fprintln(os.Stderr, "fakedialogs: -script and -log are required")
		os.Exit(2)
	}
	if err := run(*script, *logPath); err != nil {
		fmt.Fprintln(os.Stderr, "fakedialogs:", err)
		os.Exit(1)
	}
}

func run(script, logPath string) error {
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644) //nolint:gosec // the VM test's log, read by the test driver
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer log.Close()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	h := &host{script: script, log: log, prompts: map[string]int{}}
	release, err := portaldialogs.NewDaemon(h).Export(conn)
	if err != nil {
		return err
	}
	defer release()
	h.logf("ready")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()
	return nil
}

// host answers authentications; the other dialogs are not part of the
// test.
type host struct {
	portaldialogs.Host
	script string
	log    *os.File

	mu      sync.Mutex
	prompts map[string]int
}

func (h *host) logf(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(h.log, format+"\n", args...)
}

func (h *host) Authenticate(c portaldialogs.Caller, r portaldialogs.AuthRequest) portaldialogs.AuthReply {
	h.mu.Lock()
	n := h.prompts[c.Handle]
	h.prompts[c.Handle] = n + 1
	h.mu.Unlock()
	names := make([]string, 0, len(r.Identities))
	for _, id := range r.Identities {
		names = append(names, id.Name)
	}
	h.logf("authenticate handle=%s selected=%d identities=%s prompt=%q notes=%s details=%s", c.Handle, r.Selected, strings.Join(names, ","), r.Prompt, notes(r.Notes), details(r.Details))
	reply, err := h.reply(n)
	if err != nil {
		h.logf("script: %v; cancelling", err)
		return portaldialogs.AuthCancel{}
	}
	return reply
}

func (h *host) reply(n int) (portaldialogs.AuthReply, error) {
	data, err := os.ReadFile(h.script)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if n >= len(lines) {
		return nil, fmt.Errorf("no line for prompt %d", n+1)
	}
	switch verb, arg, _ := strings.Cut(lines[n], " "); verb {
	case "cancel":
		return portaldialogs.AuthCancel{}, nil
	case "answer":
		return portaldialogs.AuthAnswer{Response: portaldialogs.NewResponse(arg)}, nil
	default:
		return nil, errors.New("unknown script verb " + verb)
	}
}

func (h *host) ShowAuthentication(c portaldialogs.Caller, r portaldialogs.AuthRequest) {
	h.logf("show handle=%s notes=%s", c.Handle, notes(r.Notes))
}

func (h *host) Dismiss(handle string) {
	h.mu.Lock()
	delete(h.prompts, handle)
	h.mu.Unlock()
	h.logf("dismiss handle=%s", handle)
}

func notes(ns []portaldialogs.AuthNote) string {
	return bracketed(ns, func(n portaldialogs.AuthNote) string {
		kind := "info"
		if n.Error {
			kind = "error"
		}
		return fmt.Sprintf("%s:%q", kind, n.Text)
	})
}

func details(ds []portaldialogs.AuthDetail) string {
	return bracketed(ds, func(d portaldialogs.AuthDetail) string { return fmt.Sprintf("%s:%q", d.Label, d.Value) })
}

func bracketed[T any](items []T, show func(T) string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = show(item)
	}
	return "[" + strings.Join(parts, " ") + "]"
}
