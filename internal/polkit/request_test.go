package polkit_test

import (
	"reflect"
	"strings"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-polkit/internal/polkit"
)

func user(uid any) polkit.Identity {
	return polkit.Identity{Kind: "unix-user", Details: map[string]dbus.Variant{"uid": dbus.MakeVariant(uid)}}
}

func TestParseIdentities(t *testing.T) {
	t.Parallel()

	got, err := polkit.ParseIdentities([]polkit.Identity{user(uint32(1000)), user(uint32(0))})
	if err != nil {
		t.Fatalf("ParseIdentities: %v", err)
	}
	if want := []polkit.UID{1000, 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("ParseIdentities = %v, want %v", got, want)
	}
}

func TestParseIdentitiesRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []polkit.Identity
		want string
	}{
		{name: "none", ids: nil, want: "no identity offered"},
		{name: "a group", ids: []polkit.Identity{user(uint32(1000)), {Kind: "unix-group", Details: map[string]dbus.Variant{"gid": dbus.MakeVariant(uint32(10))}}}, want: `identity kind "unix-group"`},
		{name: "a netgroup", ids: []polkit.Identity{{Kind: "unix-netgroup"}}, want: `identity kind "unix-netgroup"`},
		{name: "no uid", ids: []polkit.Identity{{Kind: "unix-user", Details: map[string]dbus.Variant{}}}, want: "without a uid"},
		{name: "a uid of another type", ids: []polkit.Identity{user(int32(1000))}, want: "of type i"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, err := polkit.ParseIdentities(tc.ids); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseIdentities = %v, %v; want an error with %q", got, err, tc.want)
			}
		})
	}
}

func TestParseDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   map[string]string
		want polkit.Details
	}{
		{name: "pkexec", in: map[string]string{"program": "/usr/bin/true", "command_line": "/usr/bin/true -x", "polkit.caller-pid": "4242", "user": "root"}, want: polkit.Details{CallerPID: 4242, Program: "/usr/bin/true", CommandLine: "/usr/bin/true -x"}},
		{name: "none", in: map[string]string{}, want: polkit.Details{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := polkit.ParseDetails(tc.in)
			if err != nil || got != tc.want {
				t.Errorf("ParseDetails = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	for _, pid := range []string{"", "0", "-1", "abc", "4294967296"} {
		if got, err := polkit.ParseDetails(map[string]string{"polkit.caller-pid": pid}); err == nil {
			t.Errorf("ParseDetails accepted caller pid %q as %+v", pid, got)
		}
	}
}
