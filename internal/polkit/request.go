package polkit

import (
	"errors"
	"fmt"
	"strconv"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// UID is a Unix user id.
type UID uint32

// Identity is a polkit identity as BeginAuthentication receives it,
// (sa{sv}): its kind and its details.
type Identity struct {
	Kind    string
	Details map[string]dbus.Variant
}

const unixUser = "unix-user"

// ParseIdentities reads the identities polkitd offers as the users they
// name. polkit 127 expands admin groups into their users before asking
// an agent, so any kind but unix-user is refused by name, as is an
// offer of none.
func ParseIdentities(ids []Identity) ([]UID, error) {
	if len(ids) == 0 {
		return nil, errors.New("no identity offered to authenticate as")
	}
	uids := make([]UID, 0, len(ids))
	for _, id := range ids {
		if id.Kind != unixUser {
			return nil, fmt.Errorf("identity kind %q is not supported, only %s", id.Kind, unixUser)
		}
		v, ok := id.Details["uid"]
		if !ok {
			return nil, fmt.Errorf("%s identity without a uid", unixUser)
		}
		uid, ok := v.Value().(uint32)
		if !ok {
			return nil, fmt.Errorf("%s identity with a uid of type %s, want u", unixUser, v.Signature())
		}
		uids = append(uids, UID(uid))
	}
	return uids, nil
}

// PID is a process id; 0 is none.
type PID uint32

// Details are the BeginAuthentication details an authentication shows.
// Each is empty (0 for the pid) when polkitd did not send it.
type Details struct {
	CallerPID   PID
	Program     string
	CommandLine string
}

// The detail keys Details reads.
const (
	callerPIDKey   = "polkit.caller-pid"
	programKey     = "program"
	commandLineKey = "command_line"
)

// ParseDetails reads the details shown from polkitd's a{ss}; the rest
// are not shown. A caller pid that is not a process id is refused.
func ParseDetails(details map[string]string) (Details, error) {
	d := Details{Program: details[programKey], CommandLine: details[commandLineKey]}
	if s, ok := details[callerPIDKey]; ok {
		pid, err := strconv.ParseUint(s, 10, 32)
		if err != nil || pid == 0 {
			return Details{}, fmt.Errorf("detail %s %q is not a process id", callerPIDKey, s)
		}
		d.CallerPID = PID(pid)
	}
	return d, nil
}
