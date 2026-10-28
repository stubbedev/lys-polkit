// Command lys-polkit is the lys desktop's polkit authentication agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"runtime/debug"
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

// version is the release the build names with -ldflags -X; without one
// it is the module version Go recorded.
var version string

const usage = `Usage: lys-polkit [--version | --help]

lys-polkit is the lys desktop's polkit authentication agent. It registers
with polkitd for the user's graphical session on the system bus, runs each
authentication through polkit's agent helper, and asks for the password
through lys-shell's dialogs on the session bus. It runs until SIGTERM or
SIGINT, then unregisters.

Options:
  --version  print the version and exit
  --help     print this help and exit
`

// command is what the arguments ask for.
type command int

const (
	serve command = iota
	printVersion
	printHelp
)

// parseArgs reads the command line, without the program name.
func parseArgs(args []string) (command, error) {
	fs := flag.NewFlagSet("lys-polkit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showVersion := fs.Bool("version", false, "")
	showHelp := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return printHelp, nil
		}
		return serve, err
	}
	switch {
	case fs.NArg() > 0:
		return serve, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case *showVersion && *showHelp:
		return serve, errors.New("--version and --help exclude each other")
	case *showVersion:
		return printVersion, nil
	case *showHelp:
		return printHelp, nil
	default:
		return serve, nil
	}
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Version
	}
	return "unknown"
}

func main() {
	cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "lys-polkit: %v\n\n%s", err, usage)
		os.Exit(2)
	}
	switch cmd {
	case printVersion:
		fmt.Println("lys-polkit", buildVersion())
		return
	case printHelp:
		fmt.Print(usage)
		return
	case serve:
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	err = run(ctx)
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
