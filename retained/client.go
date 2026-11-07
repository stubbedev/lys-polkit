package retained

import (
	"context"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const (
	propertiesIface   = "org.freedesktop.DBus.Properties"
	propertiesChanged = propertiesIface + ".PropertiesChanged"
)

type Client struct{ obj dbus.BusObject }

func NewClient(conn *dbus.Conn) *Client {
	return &Client{obj: conn.Object(ServiceName, ServicePath)}
}

func (c *Client) Authorizations(ctx context.Context) ([]Authorization, error) {
	var list []wire
	if err := c.obj.CallWithContext(ctx, propertiesIface+".Get", 0, Interface, Property).Store(&list); err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", Property, ServiceName, err)
	}
	authorizations, err := fromWire(list)
	if err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", Property, ServiceName, err)
	}
	return authorizations, nil
}

func (c *Client) RevokeAll(ctx context.Context) error {
	if err := c.obj.CallWithContext(ctx, Interface+".RevokeAll", 0).Err; err != nil {
		return fmt.Errorf("revoke every retained authorization through %s: %w", ServiceName, err)
	}
	return nil
}

func (c *Client) Revoke(ctx context.Context, id string) error {
	if err := c.obj.CallWithContext(ctx, Interface+".Revoke", 0, id).Err; err != nil {
		return fmt.Errorf("revoke retained authorization %s through %s: %w", id, ServiceName, err)
	}
	return nil
}

func ChangedMatch() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender(ServiceName),
		dbus.WithMatchObjectPath(ServicePath),
		dbus.WithMatchInterface(propertiesIface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, Interface),
	}
}

func Changed(sig *dbus.Signal) ([]Authorization, bool, error) {
	if sig.Path != ServicePath || sig.Name != propertiesChanged || len(sig.Body) != 3 {
		return nil, false, nil
	}
	if iface, _ := sig.Body[0].(string); iface != Interface {
		return nil, false, nil
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return nil, false, fmt.Errorf("%s from %s: changed properties of type %T", propertiesChanged, sig.Sender, sig.Body[1])
	}
	v, ok := changed[Property]
	if !ok {
		return nil, false, nil
	}
	var list []wire
	if err := dbus.Store([]any{v.Value()}, &list); err != nil {
		return nil, false, fmt.Errorf("%s from %s: %s: %w", propertiesChanged, sig.Sender, Property, err)
	}
	authorizations, err := fromWire(list)
	if err != nil {
		return nil, false, fmt.Errorf("%s from %s: %w", propertiesChanged, sig.Sender, err)
	}
	return authorizations, true, nil
}
