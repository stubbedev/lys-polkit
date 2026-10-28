package logind_test

import (
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"

	"github.com/stubbedev/lys-polkit/internal/logind"
	"github.com/stubbedev/lys-polkit/internal/logind/logindtest"
)

var (
	tty     = logind.SessionRef{ID: "c2", Path: "/org/freedesktop/login1/session/c2"}
	display = logind.SessionRef{ID: "3", Path: "/org/freedesktop/login1/session/_33"}
)

func TestSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fake logindtest.Logind
		want logind.SessionID
	}{
		{name: "in its own session", fake: logindtest.Logind{Own: tty, Display: display}, want: "c2"},
		{name: "outside any session", fake: logindtest.Logind{Display: display}, want: "3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := dbustest.Start(t)
			tc.fake.Serve(t, bus.Conn(t))
			got, err := logind.Session(t.Context(), bus.Conn(t), 42, 1000)
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			if got != tc.want {
				t.Errorf("Session = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionWithoutDisplayNamesTheUID(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	logindtest.Logind{}.Serve(t, bus.Conn(t))
	_, err := logind.Session(t.Context(), bus.Conn(t), 42, 1000)
	if err == nil || !strings.Contains(err.Error(), "uid 1000 has no display session") {
		t.Fatalf("Session = %v, want the missing display session of uid 1000", err)
	}
}

func TestSessionWithoutLogindFails(t *testing.T) {
	t.Parallel()

	bus := dbustest.Start(t)
	_, err := logind.Session(t.Context(), bus.Conn(t), 42, 1000)
	if err == nil || !strings.Contains(err.Error(), "GetSessionByPID(42)") {
		t.Fatalf("Session = %v, want GetSessionByPID's failure", err)
	}
}
