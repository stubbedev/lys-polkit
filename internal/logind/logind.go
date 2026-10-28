// Package logind resolves the session polkitd attributes a process to,
// over org.freedesktop.login1 on the system bus.
package logind

import (
	"context"
	"errors"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// D-Bus identity of systemd-logind (org.freedesktop.login1(5)).
const (
	BusName            = "org.freedesktop.login1"
	ManagerPath        = dbus.ObjectPath("/org/freedesktop/login1")
	ManagerIface       = "org.freedesktop.login1.Manager"
	UserSelfPath       = dbus.ObjectPath("/org/freedesktop/login1/user/self")
	UserIface          = "org.freedesktop.login1.User"
	SessionIface       = "org.freedesktop.login1.Session"
	ErrNoSessionForPID = "org.freedesktop.login1.NoSessionForPID"
	noSessionPath      = dbus.ObjectPath("/")
)

// SessionID is a logind session id.
type SessionID string

// SessionRef is a session as logind's properties name it, (so): its id
// and its object path.
type SessionRef struct {
	ID   SessionID
	Path dbus.ObjectPath
}

// Session is the session polkitd's systemd session monitor attributes
// the process pid of uid to: the process's own session when it is in
// one, else the user's display session. A user service runs outside any
// session, so polkitd resolves it through the display session, and an
// agent registering for any other session is refused.
func Session(ctx context.Context, conn *dbus.Conn, pid, uid uint32) (SessionID, error) {
	path, err := sessionPath(ctx, conn, pid, uid)
	if err != nil {
		return "", err
	}
	var id SessionID
	if err := property(ctx, conn, path, SessionIface, "Id", &id); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("logind session %s has an empty Id", path)
	}
	return id, nil
}

func sessionPath(ctx context.Context, conn *dbus.Conn, pid, uid uint32) (dbus.ObjectPath, error) {
	var own dbus.ObjectPath
	err := conn.Object(BusName, ManagerPath).CallWithContext(ctx, ManagerIface+".GetSessionByPID", 0, pid).Store(&own)
	if err == nil {
		return own, nil
	}
	var derr dbus.Error
	if !errors.As(err, &derr) || derr.Name != ErrNoSessionForPID {
		return "", fmt.Errorf("logind GetSessionByPID(%d): %w", pid, err)
	}
	var display SessionRef
	if err := property(ctx, conn, UserSelfPath, UserIface, "Display", &display); err != nil {
		return "", err
	}
	if display.Path == noSessionPath || display.Path == "" {
		return "", fmt.Errorf("uid %d has no display session in logind, and process %d is in no session", uid, pid)
	}
	return display.Path, nil
}

func property(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath, iface, name string, dest any) error {
	var v dbus.Variant
	if err := conn.Object(BusName, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, name).Store(&v); err != nil {
		return fmt.Errorf("logind %s %s.%s: %w", path, iface, name, err)
	}
	if err := v.Store(dest); err != nil {
		return fmt.Errorf("logind %s %s.%s %s: %w", path, iface, name, v.Signature(), err)
	}
	return nil
}
