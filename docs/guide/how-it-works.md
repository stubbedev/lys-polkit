# How it works

## Registering

lys-polkit connects to the system bus and the session bus, then registers with
polkitd for the session polkitd attributes it to: its own logind session, or
the user's display session when it runs as a systemd user service outside any
session. polkitd refuses an agent registering for any other session.

It registers again when polkitd restarts, and unregisters on SIGTERM or SIGINT.
If polkitd refuses the registration, for example because another agent already
serves the session, it exits non-zero with polkitd's message instead of running
unregistered.

## Answering a request

Requests are answered one at a time, first in first out. For each one:

1. lys-polkit starts a PAM conversation through the helper, as the selected
   identity.
2. The helper's prompts appear in the dialog, and so do its information and
   error messages, such as a fingerprint reader asking for a finger. The dialog
   also shows polkitd's message and icon, and its details: the action id, the
   action's vendor and vendor website, and the program, command line and
   calling process (its executable's name and pid) when polkitd sends them.
3. Answers go to the helper and are not kept.
4. On success the helper has told polkitd itself. lys-polkit takes the dialog
   down and returns to polkitd.

A wrong password starts a new attempt with "Authentication failed" shown.
Cancelling returns polkit's `Cancelled`, and so does polkitd cancelling the
request, queued or running. If the dialog host goes away during a request, it
fails with polkit's `Failed`.

## Details

The action's vendor comes from polkitd's `EnumerateActions`, asked once in the
session's locale and kept until polkitd restarts or reports a change. When that
fails the dialog still opens, with the action id alone, and the failure is
logged. The calling process is named from `/proc/<pid>/exe`, or `comm` when the
executable cannot be read; a process already gone shows its pid alone.

The detail labels and the "Authentication failed" note are translated from the
gettext catalogs built into lys-polkit, chosen by `LANGUAGE`, `LC_ALL`,
`LC_MESSAGES` and `LANG`. German ships today; other languages fall back to
English.

## Retained admin authorizations

After an `auth_admin_keep` action polkitd keeps a temporary authorization for
the session (five minutes by default), in which the same action is granted
without asking. lys-polkit serves them on the session bus as
`dev.stubbe.lys.Polkit1` at `/dev/stubbe/lys/Polkit`:

- `TemporaryAuthorizations`, a property of `a(sstt)`: each authorization's id,
  action id, and the Unix times it was obtained and expires, soonest expiry
  first. `PropertiesChanged` is emitted whenever the list changes.
- `RevokeAll()` and `Revoke(id)`, which forward to polkitd's
  `RevokeTemporaryAuthorizations` and `RevokeTemporaryAuthorizationById`.

The list is read again after each successful authentication, on polkitd's
`Changed` signal, after each registration (so a restarted polkitd is read
again), and once at the earliest expiry. With no authorizations held it arms no
timer and makes no calls. `lys-polkit revoke`, the polkit counterpart of
`sudo -k`, calls `RevokeAll`. The Go package
`github.com/stubbedev/lys-polkit/retained` is the client side of the
contract.

## Identities

polkitd offers the identities that may authenticate. The session's user is
selected first when offered, otherwise the first one offered, and the dialog
can switch to another.

Only `unix-user` identities are accepted: polkit 127 expands admin groups into
users before it asks an agent.

Each identity's login name is looked up with `getent passwd` before the dialog
opens, and one that cannot be found fails the request. That goes through the
system's own name service, so users from any `nsswitch.conf` source count, such
as LDAP, SSSD or systemd's userdb, in a binary built without cgo. `getent` has
to be on the service's `PATH`, which it is with glibc and with musl's utilities.
A lookup that takes more than ten seconds fails.

## Supported polkit

lys-polkit is built and tested against polkit 127. It reaches the agent helper
the way polkit 127 does:

- through the socket-activated `/run/polkit/agent-helper.socket` when it exists
  (polkit 127's default; on Ubuntu 26.04 the helper is not setuid, so this is
  the only way). A socket that exists but refuses the connection fails the
  attempt rather than falling back, since a fallback would hide the broken
  socket.
- otherwise by running the setuid `polkit-agent-helper-1`, which is how older
  polkit releases work: polkit's own `/usr/lib/polkit-1/polkit-agent-helper-1`,
  or the `/run/wrappers/bin/polkit-agent-helper-1` wrapper of a NixOS whose
  polkit module defines one (those up to 25.11 do; current unstable only has the
  socket). The first that exists runs, so one binary serves both, and
  [a build can name others](./configuration#build-time-values).

Messages from the helper's stderr go to lys-polkit's log, not to the dialog.
