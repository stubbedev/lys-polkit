// Command lys-polkit is the lys desktop's polkit authentication agent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/agent"
	"github.com/stubbedev/lys-polkit/internal/helper"
	"github.com/stubbedev/lys-polkit/internal/logind"
	"github.com/stubbedev/lys-polkit/internal/polkit"
)

// helperProgram is the setuid polkit-agent-helper-1, used where polkit
// has no helper socket; the build sets it for its platform with
// -ldflags -X.
var helperProgram = "/usr/lib/polkit-1/polkit-agent-helper-1"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	err := run(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lys-polkit:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	system, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("connect to the system bus: %w", err)
	}
	defer func() { _ = system.Close() }()
	session, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the session bus: %w", err)
	}
	defer func() { _ = session.Close() }()

	uid, err := intconv.Exact[uint32](os.Getuid())
	if err != nil {
		return fmt.Errorf("uid %d: %w", os.Getuid(), err)
	}
	pid, err := intconv.Exact[uint32](os.Getpid())
	if err != nil {
		return fmt.Errorf("pid %d: %w", os.Getpid(), err)
	}
	id, err := logind.Session(ctx, system, pid, uid)
	if err != nil {
		return err
	}
	a := agent.New(agent.Config{
		Dialogs:     portaldialogs.NewClient(session),
		Transport:   helper.Transport{Socket: helper.SocketPath, Program: helperProgram},
		UserName:    userName,
		SessionUser: polkit.UID(uid),
	})
	defer a.Close()
	return polkit.Serve(ctx, system, polkit.Registration{Subject: polkit.SessionSubject(id), Locale: polkit.Locale(os.Getenv)}, a)
}

func userName(uid polkit.UID) (string, error) {
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return "", fmt.Errorf("look up uid %d: %w", uid, err)
	}
	return u.Username, nil
}
