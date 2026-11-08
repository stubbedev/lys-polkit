# How it works

## Registration

lys-polkit connects to the system and session buses and registers with
polkitd for its logind session or, when running as a user service outside a
session, the user's display session. It re-registers when polkitd restarts and
unregisters on SIGTERM or SIGINT. If polkitd refuses the registration it exits
1 with polkitd's message.

## Requests

Requests run one at a time, first in first out. For each:

1. A PAM conversation starts through the helper as the selected identity.
2. The dialog shows the helper's prompts, info and error messages (such as a
   fingerprint prompt), polkitd's message and icon, and details: action id,
   vendor, vendor website, and the program, command line and caller
   (executable name and pid) when polkitd sends them.
3. Answers go to the helper and are not stored.
4. On success the helper informs polkitd; lys-polkit closes the dialog and
   returns.

| Event | Result |
| --- | --- |
| Wrong password | New attempt, with "Authentication failed" shown |
| User cancels, or polkitd cancels (queued or running) | polkit `Cancelled` |
| Dialog host disappears | polkit `Failed` |

## Details

- Vendor data comes from polkitd's `EnumerateActions`, fetched once in the
  session's locale and cached until polkitd restarts or emits `Changed`. If
  it fails, the dialog shows the action id only and the error is logged.
- The caller is named from `/proc/<pid>/exe`, falling back to `comm`; an
  exited process shows its pid only.
- Detail labels and "Authentication failed" come from embedded gettext
  catalogs (German included; other locales use English), selected by
  `LANGUAGE`, `LC_ALL`, `LC_MESSAGES`, `LANG`.

## Identities

- polkitd supplies the identities allowed to authenticate. The session user is
  preselected if offered, else the first one; the dialog can switch.
- Only `unix-user` identities are accepted. polkit 127 expands admin groups
  into users before asking the agent.
- Login names are resolved with `getent passwd` before the dialog opens, so
  any `nsswitch.conf` source (LDAP, SSSD, systemd userdb) works in the cgo-free
  binary. An unknown user, or a lookup over 10 seconds, fails the request.

## Retained admin authorizations

After an `auth_admin_keep` action polkitd grants the same action again without
asking for a while (five minutes by default). lys-polkit exposes these on the
session bus as `dev.stubbe.lys.Polkit1` at `/dev/stubbe/lys/Polkit`:

| Member | Description |
| --- | --- |
| `TemporaryAuthorizations` property, `a(sstt)` | id, action id, obtained and expiry Unix times; soonest expiry first; emits `PropertiesChanged` |
| `RevokeAll()` | Calls polkitd's `RevokeTemporaryAuthorizations` for the session |
| `Revoke(id)` | Calls polkitd's `RevokeTemporaryAuthorizationById` |

The list is refreshed after each successful authentication, on polkitd's
`Changed` signal, after each registration, and at the earliest expiry. With
none held, no timer runs. `lys-polkit revoke` calls `RevokeAll`. The Go
package `github.com/stubbedev/lys-polkit/retained` is the client.

## Supported polkit

Built and tested against polkit 127. The helper is reached by:

1. The socket `/run/polkit/agent-helper.socket`, if it exists. A socket that
   exists but refuses the connection fails the attempt.
2. Otherwise the first existing setuid `polkit-agent-helper-1` from
   [`main.helperPrograms`](./configuration#build-time-values): polkit's
   `/usr/lib/polkit-1/polkit-agent-helper-1` or the NixOS wrapper
   `/run/wrappers/bin/polkit-agent-helper-1`. One that exists but cannot run
   fails the attempt.

The helper's stderr goes to lys-polkit's log, not the dialog.
