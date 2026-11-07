package tracker

import (
	"context"
	"log/slog"
	"time"

	"github.com/stubbedev/lys-polkit/retained"
)

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Config struct {
	List    func(context.Context) ([]retained.Authorization, error)
	Publish func([]retained.Authorization) error
	Clock   Clock
}

const expiryResolution = time.Second

type Tracker struct {
	cfg  Config
	poke chan struct{}
}

func New(cfg Config) *Tracker {
	return &Tracker{cfg: cfg, poke: make(chan struct{}, 1)}
}

func (t *Tracker) Refresh() {
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

func (t *Tracker) Run(ctx context.Context) {
	var timer Timer
	var expired <-chan time.Time
	disarm := func() {
		if timer != nil {
			timer.Stop()
		}
		timer, expired = nil, nil
	}
	defer disarm()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.poke:
		case <-expired:
			timer, expired = nil, nil
		}
		list, err := t.cfg.List(ctx)
		if err != nil {
			slog.Error("list retained polkit authorizations", "err", err)
			continue
		}
		if err := t.cfg.Publish(list); err != nil {
			slog.Error("publish retained polkit authorizations", "err", err)
		}
		disarm()
		if len(list) == 0 {
			continue
		}
		timer = t.cfg.Clock.NewTimer(t.untilExpiry(list))
		expired = timer.C()
	}
}

func (t *Tracker) untilExpiry(list []retained.Authorization) time.Duration {
	earliest := list[0].Expires
	for _, a := range list[1:] {
		if a.Expires.Before(earliest) {
			earliest = a.Expires
		}
	}
	return max(earliest.Sub(t.cfg.Clock.Now())+expiryResolution, expiryResolution)
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) Timer { return systemTimer{time.NewTimer(d)} }

type systemTimer struct{ t *time.Timer }

func (s systemTimer) C() <-chan time.Time { return s.t.C }

func (s systemTimer) Stop() bool { return s.t.Stop() }
