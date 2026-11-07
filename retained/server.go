package retained

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type Revoker interface {
	RevokeAll(ctx context.Context) error
	Revoke(ctx context.Context, id string) error
}

const revokeTimeout = 10 * time.Second

type Server struct {
	conn    *dbus.Conn
	revoker Revoker

	mu   sync.Mutex
	list []wire
}

func Serve(conn *dbus.Conn, revoker Revoker) (*Server, func(), error) {
	s := &Server{conn: conn, revoker: revoker, list: []wire{}}
	release, err := dbusx.Serve(conn, dbusx.Service{
		Name:       ServiceName,
		Path:       ServicePath,
		Interface:  Interface,
		Methods:    methods{s},
		Properties: dbusx.Getters{Property: func() any { return s.snapshot() }},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("serve %s: %w", ServiceName, err)
	}
	return s, release, nil
}

func (s *Server) snapshot() []wire {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list
}

func (s *Server) Publish(list []Authorization) error {
	next, err := toWire(list)
	if err != nil {
		return fmt.Errorf("publish %s: %w", Property, err)
	}
	s.mu.Lock()
	same := slices.Equal(s.list, next)
	if !same {
		s.list = next
	}
	s.mu.Unlock()
	if same {
		return nil
	}
	if err := s.conn.Emit(ServicePath, propertiesChanged, Interface, map[string]dbus.Variant{Property: dbus.MakeVariant(next)}, []string{}); err != nil {
		return fmt.Errorf("emit %s change: %w", Property, err)
	}
	return nil
}

type methods struct{ s *Server }

func (m methods) RevokeAll() *dbus.Error {
	ctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
	defer cancel()
	if err := m.s.revoker.RevokeAll(ctx); err != nil {
		return dbusx.Failed(err.Error())
	}
	return nil
}

func (m methods) Revoke(id string) *dbus.Error {
	ctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
	defer cancel()
	if err := m.s.revoker.Revoke(ctx, id); err != nil {
		return dbusx.Failed(err.Error())
	}
	return nil
}
