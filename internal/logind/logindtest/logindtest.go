// Package logindtest is a fake systemd-logind for tests: the Manager's
// GetSessionByPID, the calling user's Display, and the sessions' Ids,
// on a bus the test owns.
package logindtest

import (
	"testing"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/logind"
)

// Logind is the fake's state: Own is the session every process is in
// (none when its Path is empty), Display is the user's display session
// (none when its Path is empty). Both sessions are exported with their
// Ids.
type Logind struct {
	Own     logind.SessionRef
	Display logind.SessionRef
}

type manager struct{ own logind.SessionRef }

func (m manager) GetSessionByPID(uint32) (dbus.ObjectPath, *dbus.Error) {
	if m.own.Path == "" {
		return "", dbus.NewError(logind.ErrNoSessionForPID, []any{"PID does not belong to any known session"})
	}
	return m.own.Path, nil
}

type methodless struct{}

type export struct {
	path  dbus.ObjectPath
	iface dbusx.Interface
}

// Serve exports the fake on conn under org.freedesktop.login1.
func (l Logind) Serve(t testing.TB, conn *dbus.Conn) {
	t.Helper()
	display := l.Display
	if display.Path == "" {
		display = logind.SessionRef{Path: "/"}
	}
	exports := []export{
		{logind.ManagerPath, dbusx.Interface{Name: logind.ManagerIface, Methods: manager{own: l.Own}}},
		{logind.UserSelfPath, dbusx.Interface{Name: logind.UserIface, Methods: methodless{}, Properties: dbusx.Getters{
			"Display": func() any { return display },
		}}},
	}
	for _, s := range []logind.SessionRef{l.Own, l.Display} {
		if s.Path == "" {
			continue
		}
		id := string(s.ID)
		exports = append(exports, export{s.Path, dbusx.Interface{Name: logind.SessionIface, Methods: methodless{}, Properties: dbusx.Getters{
			"Id": func() any { return id },
		}}})
	}
	for _, e := range exports {
		if _, err := dbusx.Export(conn, e.path, e.iface); err != nil {
			t.Fatalf("logindtest: export %s: %v", e.path, err)
		}
	}
	if _, err := dbusx.Own(conn, logind.BusName); err != nil {
		t.Fatalf("logindtest: %v", err)
	}
}
