# Configuration

lys-polkit has no configuration file. It finds its session, its helper and
its dialogs by itself, so there is little to set.

## Home-manager options

| Option | Default | What it does |
| --- | --- | --- |
| `services.lys-polkit.enable` | `false` | Installs the package and links its user unit into the wants of `graphical-session.target` |
| `services.lys-polkit.package` | `lys-polkit.packages.${system}.default` | The package whose user unit is installed |

## Build-time values

The helper path and the version are set when the binary is built, with
`-ldflags -X`.

| Variable | Default | What it is |
| --- | --- | --- |
| `main.helperProgram` | `/usr/lib/polkit-1/polkit-agent-helper-1`, or `/run/wrappers/bin/polkit-agent-helper-1` in the nix package | The setuid helper, used only when [the helper socket](./how-it-works#supported-polkit) does not exist |
| `main.version` | The module version Go recorded | What `--version` prints |

To set the helper path with home-manager, override the package:

```nix
services.lys-polkit.package = inputs.lys-polkit.packages.${pkgs.stdenv.hostPlatform.system}.default.override {
  helperProgram = "/usr/lib/polkit-1/polkit-agent-helper-1";
};
```

## Command line

```
lys-polkit [--version | --help]
```

| Option | What it does |
| --- | --- |
| `--version` | Prints the version and exits |
| `--help` | Prints the usage and exits |

Without options it serves until SIGTERM or SIGINT, then unregisters. A
failure exits 1 and a wrong command line exits 2.

## Language

lys-polkit tells polkitd the first of `LC_ALL`, `LC_MESSAGES` and `LANG` that
is set in its environment, else `C`. polkitd translates the action messages
into that locale. The unit takes these from the systemd user manager's
environment.

## The user unit

`dist/lys-polkit.service.in` is part of and ordered after
`graphical-session.target`. It restarts lys-polkit on failure after 2 seconds,
with at most three starts a minute, so a refused registration ends in a failed
unit visible in `systemctl --user status lys-polkit` instead of a restart
loop.
