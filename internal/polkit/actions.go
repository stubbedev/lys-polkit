package polkit

import (
	"context"
	"fmt"
	"sync"

	libpolkit "github.com/stubbedev/lys-lib/polkit"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type Actions struct {
	authority libpolkit.Authority
	locale    string

	mu         sync.Mutex
	generation uint64
	known      map[string]libpolkit.ActionDescription
}

func NewActions(conn *dbus.Conn, locale string) *Actions {
	return &Actions{authority: libpolkit.NewAuthority(conn), locale: locale}
}

func (a *Actions) Forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.generation++
	a.known = nil
}

func (a *Actions) Lookup(ctx context.Context, id string) (libpolkit.ActionDescription, error) {
	a.mu.Lock()
	known, generation := a.known, a.generation
	a.mu.Unlock()
	if known == nil {
		var err error
		if known, err = a.enumerate(ctx); err != nil {
			return libpolkit.ActionDescription{}, err
		}
		a.mu.Lock()
		if a.generation == generation {
			a.known = known
		}
		a.mu.Unlock()
	}
	d, ok := known[id]
	if !ok {
		return libpolkit.ActionDescription{}, fmt.Errorf("action %s is not registered with polkitd", id)
	}
	return d, nil
}

func (a *Actions) enumerate(ctx context.Context) (map[string]libpolkit.ActionDescription, error) {
	all, err := a.authority.EnumerateActions(ctx, a.locale)
	if err != nil {
		return nil, err
	}
	known := make(map[string]libpolkit.ActionDescription, len(all))
	for _, d := range all {
		known[d.ActionID] = d
	}
	return known, nil
}
