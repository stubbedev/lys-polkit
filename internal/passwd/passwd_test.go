package passwd_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-polkit/internal/passwd"
)

const ldapUID = 70001

func fakeGetent(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "getent"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestNameResolvesAUserOutsideEtcPasswd(t *testing.T) {
	fakeGetent(t, `[ "$1 $2" = "passwd 70001" ] && [ $# = 2 ] || exit 1
echo 'carol:*:70001:70001:Carol:/home/carol:/bin/sh'`)
	name, err := passwd.Name(t.Context(), ldapUID)
	if err != nil || name != "carol" {
		t.Fatalf("Name = %q, %v; want carol", name, err)
	}
}

func TestNameFails(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown user", body: "exit 2", want: "no user with uid 70001"},
		{name: "getent failing", body: "echo broken >&2; exit 1", want: "broken"},
		{name: "too few fields", body: "echo carol:x:70001", want: "malformed"},
		{name: "too many fields", body: "echo carol:x:70001:70001:::/bin/sh:more", want: "malformed"},
		{name: "no name", body: "echo :x:70001:70001::/:/bin/sh", want: "malformed"},
		{name: "another uid", body: "echo carol:x:70002:70002::/:/bin/sh", want: "another uid"},
		{name: "no output", body: "exit 0", want: "malformed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeGetent(t, tc.body)
			name, err := passwd.Name(t.Context(), ldapUID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Name = %q, %v; want an error with %q", name, err, tc.want)
			}
		})
	}
}

func TestNameWithoutGetentFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if name, err := passwd.Name(t.Context(), ldapUID); err == nil || !strings.Contains(err.Error(), "getent") {
		t.Fatalf("Name = %q, %v; want an error naming getent", name, err)
	}
}

func TestNameStopsWithItsContext(t *testing.T) {
	fakeGetent(t, "while :; do :; done")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := passwd.Name(ctx, ldapUID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Name = %v, want the deadline", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Name took %s after its deadline", took)
	}
}
