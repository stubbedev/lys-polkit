# Configuration

There is no configuration file.

## Home-manager options

| Option | Type | Default | Effect |
| --- | --- | --- | --- |
| `services.lys-polkit.enable` | bool | `false` | Installs the package and links its user unit into `graphical-session.target.wants` |
| `services.lys-polkit.package` | package | `lys-polkit.packages.${system}.default` | Package whose binary and user unit are used |

## Build-time values

Set with `go build -ldflags "-X <variable>=<value>"`.

| Variable | Default | Effect |
| --- | --- | --- |
| `main.helperPrograms` | `/usr/lib/polkit-1/polkit-agent-helper-1:/run/wrappers/bin/polkit-agent-helper-1` | Colon-separated candidate paths of the setuid helper; the first that exists runs, only when the [helper socket](./how-it-works#supported-polkit) is absent |
| `main.version` | Module version recorded by Go | Printed by `--version` |

## Command line

```
lys-polkit [--version | --help]
lys-polkit revoke
```

| Invocation | Effect |
| --- | --- |
| `lys-polkit` | Serves as the session's agent until SIGTERM or SIGINT, then unregisters |
| `lys-polkit revoke` | Revokes every [retained admin authorization](./how-it-works#retained-admin-authorizations) of the session (15 s timeout) |
| `--version` | Prints `lys-polkit <version>` and exits |
| `--help` | Prints usage and exits |

Exit status: 0 on success, 1 on failure, 2 on an invalid command line.

## Language

polkitd translates action messages into the locale lys-polkit registers with:
the first set of `LC_ALL`, `LC_MESSAGES`, `LANG`, else `C`. lys-polkit's own
strings follow `LANGUAGE`, `LC_ALL`, `LC_MESSAGES`, `LANG` (see
[details](./how-it-works#details)). The unit inherits these from the systemd
user manager.

## User unit

`dist/lys-polkit.service.in`:

| Setting | Value |
| --- | --- |
| `PartOf`, `After`, `WantedBy` | `graphical-session.target` |
| `Restart` / `RestartSec` | `on-failure` / `2` |
| `StartLimitBurst` / `StartLimitIntervalSec` | `3` / `60` |

A repeatedly refused registration ends as a failed unit, not a restart loop.
