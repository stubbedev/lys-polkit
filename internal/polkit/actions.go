package polkit

import (
	"context"
	"fmt"
	"sync"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type ActionDescription struct {
	ActionID         string
	Description      string
	Message          string
	VendorName       string
	VendorURL        string
	IconName         string
	ImplicitAny      uint32
	ImplicitInactive uint32
	ImplicitActive   uint32
	Annotations      map[string]string
}

type Actions struct {
	conn   *dbus.Conn
	locale string

	mu         sync.Mutex
	generation uint64
	known      map[string]ActionDescription
}

func NewActions(conn *dbus.Conn, locale string) *Actions {
	return &Actions{conn: conn, locale: locale}
}

func (a *Actions) Forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.generation++
	a.known = nil
}

func (a *Actions) Lookup(ctx context.Context, id string) (ActionDescription, error) {
	a.mu.Lock()
	known, generation := a.known, a.generation
	a.mu.Unlock()
	if known == nil {
		var err error
		if known, err = a.enumerate(ctx); err != nil {
			return ActionDescription{}, err
		}
		a.mu.Lock()
		if a.generation == generation {
			a.known = known
		}
		a.mu.Unlock()
	}
	d, ok := known[id]
	if !ok {
		return ActionDescription{}, fmt.Errorf("action %s is not registered with polkitd", id)
	}
	return d, nil
}

func (a *Actions) enumerate(ctx context.Context) (map[string]ActionDescription, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var all []ActionDescription
	if err := authority(a.conn).CallWithContext(ctx, AuthorityIface+".EnumerateActions", 0, a.locale).Store(&all); err != nil {
		return nil, fmt.Errorf("enumerate polkit actions for locale %s: %w", a.locale, err)
	}
	known := make(map[string]ActionDescription, len(all))
	for _, d := range all {
		known[d.ActionID] = d
	}
	return known, nil
}
