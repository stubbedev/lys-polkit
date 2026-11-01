// Package passwd resolves users through the system's name service with
// getent, so every source nsswitch.conf names answers without cgo.
package passwd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	lookupTimeout   = 10 * time.Second
	pipeGrace       = time.Second
	getentNotFound  = 2
	passwdFields    = 7
	passwdNameField = 0
	passwdUIDField  = 2
)

// Name is the login name of uid. A directory that does not answer within
// the lookup timeout or before ctx ends fails the lookup.
func Name(ctx context.Context, uid uint32) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	key := strconv.FormatUint(uint64(uid), 10)
	cmd := exec.CommandContext(ctx, "getent", "passwd", key) //nolint:gosec // key is a formatted number
	cmd.WaitDelay = pipeGrace
	out, err := cmd.Output()
	if err == nil {
		return parseName(string(out), key)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("getent passwd %s: %w", key, ctxErr)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", fmt.Errorf("getent passwd %s: %w", key, err)
	}
	if exit.ExitCode() == getentNotFound {
		return "", fmt.Errorf("no user with uid %s in the name service", key)
	}
	return "", fmt.Errorf("getent passwd %s: %w: %s", key, err, strings.TrimSpace(string(exit.Stderr)))
}

func parseName(out, key string) (string, error) {
	line := strings.TrimSuffix(out, "\n")
	fields := strings.Split(line, ":")
	if len(fields) != passwdFields || fields[passwdNameField] == "" {
		return "", fmt.Errorf("getent passwd %s: malformed entry %q", key, line)
	}
	if fields[passwdUIDField] != key {
		return "", fmt.Errorf("getent passwd %s: entry %q is for another uid", key, line)
	}
	return fields[passwdNameField], nil
}
