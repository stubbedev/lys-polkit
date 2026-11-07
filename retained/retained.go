// Package retained is lys-polkit's session-bus contract for the admin
// authorizations polkitd retains after an auth_admin_keep action: the
// TemporaryAuthorizations property of dev.stubbe.lys.Polkit1, its
// PropertiesChanged, and RevokeAll and Revoke.
package retained

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const (
	ServiceName = "dev.stubbe.lys.Polkit1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/Polkit")
	Interface   = "dev.stubbe.lys.Polkit1"
	Property    = "TemporaryAuthorizations"
)

type Authorization struct {
	ID       string
	ActionID string
	Obtained time.Time
	Expires  time.Time
}

func Sort(list []Authorization) {
	slices.SortFunc(list, func(a, b Authorization) int {
		return cmp.Or(a.Expires.Compare(b.Expires), cmp.Compare(a.ID, b.ID))
	})
}

type wire struct {
	ID       string
	ActionID string
	Obtained uint64
	Expires  uint64
}

func toWire(list []Authorization) ([]wire, error) {
	out := make([]wire, len(list))
	for i, a := range list {
		obtained, err := intconv.Exact[uint64](a.Obtained.Unix())
		if err != nil {
			return nil, fmt.Errorf("authorization %s obtained at %v: %w", a.ID, a.Obtained, err)
		}
		expires, err := intconv.Exact[uint64](a.Expires.Unix())
		if err != nil {
			return nil, fmt.Errorf("authorization %s expiring at %v: %w", a.ID, a.Expires, err)
		}
		out[i] = wire{ID: a.ID, ActionID: a.ActionID, Obtained: obtained, Expires: expires}
	}
	return out, nil
}

func fromWire(list []wire) ([]Authorization, error) {
	out := make([]Authorization, len(list))
	for i, w := range list {
		obtained, err := FromUnix(w.Obtained)
		if err != nil {
			return nil, fmt.Errorf("authorization %s obtained: %w", w.ID, err)
		}
		expires, err := FromUnix(w.Expires)
		if err != nil {
			return nil, fmt.Errorf("authorization %s expiring: %w", w.ID, err)
		}
		out[i] = Authorization{ID: w.ID, ActionID: w.ActionID, Obtained: obtained, Expires: expires}
	}
	return out, nil
}

func FromUnix(seconds uint64) (time.Time, error) {
	s, err := intconv.Exact[int64](seconds)
	if err != nil {
		return time.Time{}, fmt.Errorf("unix time %d: %w", seconds, err)
	}
	return time.Unix(s, 0).UTC(), nil
}
