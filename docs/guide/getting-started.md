# Getting started

lys-polkit is the polkit authentication agent of the lys desktop. polkitd asks
it to authenticate actions such as `pkexec`; it runs the PAM conversation
through polkit's agent helper and shows it in lys-shell's dialog.

## Requirements

| Service | Bus name |
| --- | --- |
| polkitd, see [supported polkit](./how-it-works#supported-polkit) | `org.freedesktop.PolicyKit1` (system) |
| systemd-logind | `org.freedesktop.login1` (system) |
| [lys-shell](https://stubbedev.github.io/lys-shell/) dialogs, `Authenticate` contract | `dev.stubbe.lys.PortalDialogs1` (session) |

Without lys-shell's dialogs every request fails with polkit's `Failed`.

## Set up

1. [Install](./install) lys-polkit and its user unit.
2. Disable any other polkit agent. polkitd accepts one agent per session.

## Check

```sh
systemctl --user status lys-polkit
pkexec --disable-internal-agent true
```

The dialog opens. The right password exits 0; cancel exits 126.
`No authentication agent found` means lys-polkit is not registered for this
session.

## Troubleshooting

```sh
journalctl --user -u lys-polkit
```

| Message starts with | Meaning |
| --- | --- |
| `polkit: register the agent at /dev/stubbe/lys/PolicyKit1/AuthenticationAgent for unix-session` | polkitd refused the registration, usually because another agent serves the session. polkitd's reason follows. |
| `uid N has no display session in logind` | Running outside a session, and the user has no display session. |
| `polkit: connect to the system bus` / `connect to the session bus` | That bus is unreachable. |
