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
   also shows polkitd's message and icon, and the program, command line and
   caller process when polkitd sends them.
3. Answers go to the helper and are not kept.
4. On success the helper has told polkitd itself. lys-polkit takes the dialog
   down and returns to polkitd.

A wrong password starts a new attempt with "Authentication failed" shown.
Cancelling returns polkit's `Cancelled`, and so does polkitd cancelling the
request, queued or running. If the dialog host goes away during a request, it
fails with polkit's `Failed`.

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
