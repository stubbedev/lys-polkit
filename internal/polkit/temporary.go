package polkit

import (
	"context"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/retained"
)

type TemporaryAuthorization struct {
	ID       string
	ActionID string
	Subject  Subject
	Obtained uint64
	Expires  uint64
}

type Temporary struct {
	conn    *dbus.Conn
	subject Subject
}

func NewTemporary(conn *dbus.Conn, subject Subject) *Temporary {
	return &Temporary{conn: conn, subject: subject}
}

func (t *Temporary) List(ctx context.Context) ([]retained.Authorization, error) {
	var all []TemporaryAuthorization
	if err := callAuthority(ctx, t.conn, "EnumerateTemporaryAuthorizations", t.subject).Store(&all); err != nil {
		return nil, fmt.Errorf("enumerate temporary authorizations of %s: %w", t.subject, err)
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
	if err := callAuthority(ctx, t.conn, "RevokeTemporaryAuthorizations", t.subject).Err; err != nil {
		return fmt.Errorf("revoke the temporary authorizations of %s: %w", t.subject, err)
	}
	return nil
}

func (t *Temporary) Revoke(ctx context.Context, id string) error {
	if err := callAuthority(ctx, t.conn, "RevokeTemporaryAuthorizationById", id).Err; err != nil {
		return fmt.Errorf("revoke temporary authorization %s: %w", id, err)
	}
	return nil
}
