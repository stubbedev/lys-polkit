package polkit

import (
	"context"
	"fmt"

	libpolkit "github.com/stubbedev/lys-lib/polkit"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/retained"
)

type Temporary struct {
	authority libpolkit.Authority
	subject   libpolkit.Subject
}

func NewTemporary(conn *dbus.Conn, subject libpolkit.Subject) *Temporary {
	return &Temporary{authority: libpolkit.NewAuthority(conn), subject: subject}
}

func (t *Temporary) List(ctx context.Context) ([]retained.Authorization, error) {
	all, err := t.authority.EnumerateTemporaryAuthorizations(ctx, t.subject)
	if err != nil {
		return nil, err
	}
	list := make([]retained.Authorization, len(all))
	for i, a := range all {
		obtained, err := retained.FromUnix(a.Obtained)
		if err != nil {
			return nil, fmt.Errorf("temporary authorization %s obtained: %w", a.ID, err)
		}
		expires, err := retained.FromUnix(a.Expires)
		if err != nil {
			return nil, fmt.Errorf("temporary authorization %s expiring: %w", a.ID, err)
		}
		list[i] = retained.Authorization{ID: a.ID, ActionID: a.ActionID, Obtained: obtained, Expires: expires}
	}
	retained.Sort(list)
	return list, nil
}

func (t *Temporary) RevokeAll(ctx context.Context) error {
	return t.authority.RevokeTemporaryAuthorizations(ctx, t.subject)
}

func (t *Temporary) Revoke(ctx context.Context, id string) error {
	return t.authority.RevokeTemporaryAuthorizationByID(ctx, id)
}
