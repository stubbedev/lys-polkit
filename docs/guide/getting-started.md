# Getting started

lys-polkit is the lys desktop's polkit authentication agent, in place of
hyprpolkitagent. When a program asks polkitd for an action that needs
authentication (`pkexec`, a settings panel, a package manager), polkitd asks
the agent registered for the user's session. lys-polkit runs the PAM
conversation through polkit's agent helper and shows it in lys-shell's
authentication dialog.

## What you need

- polkitd on the system bus (`org.freedesktop.PolicyKit1`). See
  [supported polkit](./how-it-works#supported-polkit) for the versions.
- systemd-logind (`org.freedesktop.login1`), for the session to register.
- [lys-shell](https://stubbedev.github.io/lys-shell/)'s dialogs on the session
  bus (`dev.stubbe.lys.PortalDialogs1`, with the `Authenticate` contract).
  Without them every authentication fails with polkit's `Failed`: nothing is
  authorized and nothing waits.

## Set it up

1. [Install](./install) lys-polkit and its user unit, which starts with
   `graphical-session.target`.
2. Stop whatever other polkit agent the session starts. Each session has one
   agent, and polkitd refuses a second.

## Check that it works

The unit should be active:

```sh
systemctl --user status lys-polkit
```

Then ask for an authentication. `--disable-internal-agent` stops pkexec from
using its own terminal agent, so the request goes to lys-polkit:

```sh
pkexec --disable-internal-agent true
```

lys-shell's dialog opens. The right password makes the command exit 0, and
cancelling makes it exit 126. If it reports `No authentication agent found`,
lys-polkit is not registered for this session.

## If the unit failed

lys-polkit prints why it stopped, and the journal keeps it:

```sh
journalctl --user -u lys-polkit
```

| Message starts with | What it means |
| --- | --- |
| `register polkit agent for unix-session` | polkitd refused the registration, usually because another agent already serves the session. The rest of the line is polkitd's message. |
| `uid N has no display session in logind` | The service runs outside any session and the user has no display session to register for. |
| `connect to the system bus` or `connect to the session bus` | lys-polkit could not reach that bus. |
