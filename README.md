# lys-polkit

lys-polkit is the lys desktop's polkit authentication agent, in place of
hyprpolkitagent. When a program asks polkitd for an action that needs
authentication (`pkexec`, a settings panel, a package manager), polkitd asks
the agent registered for the user's session; lys-polkit runs the PAM
conversation through polkit's agent helper and asks for the password, or
shows the fingerprint prompt, in lys-shell's authentication dialog.

- It registers for the session polkitd attributes it to: its own logind
  session, or the user's display session when it runs as a systemd user
  service outside any session.
- It registers again when polkitd restarts, and unregisters on SIGTERM or
  SIGINT. If polkitd refuses the registration (another agent already serves
  the session) it exits non-zero with polkitd's message instead of running
  unregistered.
- Requests are answered one at a time. A wrong password starts a new attempt
  with "Authentication failed" shown, the dialog can switch to another of
  the identities polkitd offers, and cancelling returns polkit's
  `Cancelled`.

## Supported polkit

lys-polkit is built and tested against polkit 127. It reaches the agent
helper the way polkit 127 does:

- through the socket-activated `/run/polkit/agent-helper.socket` when it
  exists (polkit 127's default; on Ubuntu 26.04 the helper is not setuid,
  so this is the only way), and a socket that exists but refuses the
  connection fails the attempt rather than falling back;
- otherwise by running the setuid `polkit-agent-helper-1` at the path the
  build names, which is how older polkit releases work.

Only `unix-user` identities are accepted: polkit 127 expands admin groups
into users before it asks an agent.

## Runtime requirements

- polkitd on the system bus (`org.freedesktop.PolicyKit1`)
- systemd-logind (`org.freedesktop.login1`), for the session to register
- lys-shell's dialogs on the session bus (`dev.stubbe.lys.PortalDialogs1`,
  with the `Authenticate` contract). Without them every authentication
  fails with polkit's `Failed`; nothing is authorized and nothing waits.

## Installation

### NixOS, or nix with home-manager

The flake exports a home-manager module:

```nix
{
  inputs.lys-polkit.url = "github:stubbedev/lys-polkit";

  # in the home-manager configuration
  imports = [inputs.lys-polkit.homeManagerModules.default];
  services.lys-polkit.enable = true;
  # one agent per session: drop the one it replaces
  services.hyprpolkitagent.enable = false;
}
```

It installs the package and the `lys-polkit.service` user unit, wanted by
`graphical-session.target`. On NixOS, `security.polkit.enable = true`
provides polkitd and polkit 127's helper socket, which lys-polkit uses; the
setuid path the package names, `/run/wrappers/bin/polkit-agent-helper-1`,
only matters for a polkit without the socket. The same module works on
another distribution with home-manager: polkit 127's helper socket is used
there too, and an older polkit's helper path is set with
`services.lys-polkit.package = lys-polkit.packages.${system}.default.override
{ helperProgram = "/usr/lib/polkit-1/polkit-agent-helper-1"; };`.

### Another distribution, without nix

Build with the helper path of the distribution (Debian and Ubuntu shown),
install the binary and the user unit, and enable it for the graphical
session:

```sh
go build -ldflags "-X main.version=$(git describe --always) -X main.helperProgram=/usr/lib/polkit-1/polkit-agent-helper-1" -o lys-polkit .
install -Dm755 lys-polkit ~/.local/bin/lys-polkit
mkdir -p ~/.config/systemd/user
sed "s|@bindir@|$HOME/.local/bin|" dist/lys-polkit.service.in > ~/.config/systemd/user/lys-polkit.service
systemctl --user daemon-reload
systemctl --user enable --now lys-polkit.service
```

Disable any other polkit agent the session starts first. `lys-polkit
--version` prints the version and `lys-polkit --help` the usage.

The unit restarts lys-polkit on failure, at most three times a minute, so a
refused registration ends in a failed unit visible in `systemctl --user
status lys-polkit` instead of a restart loop.

## Development

| Recipe | What it does |
| --- | --- |
| `just check` | Every release gate in order: vet, lint, test, build |
| `just vet` | Static analysis with `go vet` |
| `just lint` | `golangci-lint run` against the strict config in `.golangci.yml` |
| `just test` | `go test ./...` |
| `just build` | Compile-check every package |
| `just fmt` | Format every Go source in place |

Every dev tool (Go toolchain pinned to the version go.mod declares, gopls,
golangci-lint, delve, just, git, gh, dbus-daemon for the tests) comes from
the flake: `direnv allow` on entry, or `nix develop` explicitly.
`GOTOOLCHAIN=local` keeps the compiler from downloading itself.

The tests run without polkitd, logind or the shell: fakes of the polkit
Authority, logind and the shell's dialogs serve on private dbus-daemons
(lys-lib's `dbustest`), and the test binary itself plays
`polkit-agent-helper-1` over both transports.

D-Bus goes through [lys-lib](https://github.com/stubbedev/lys-lib)'s
patched godbus and `dbusx`; the dialogs' contract is lys-lib's
`portaldialogs`.

## Nix

- `nix build` builds the package with the user unit in
  `lib/systemd/user` (`nix/lys-polkit.nix`; `vendorHash` follows go.sum)
- `nix flake check` evaluates the home-manager module and checks the unit
  is wired to the graphical session, and on x86_64 (with KVM) runs
  `tests/polkit-vm.nix`: a NixOS VM where a real polkit 127 authenticates
  `pkexec` through lys-polkit, with `internal/vmtest/fakedialogs` answering
  for lys-shell's dialogs from a script (right password, a wrong one then
  the right one, cancel, and no agent once lys-polkit stops)
- `nix fmt` formats the nix files with alejandra

## CI

`.github/workflows/security.yml` runs CodeQL (Go and GitHub Actions), Grype and
govulncheck, uploading findings to code scanning; dependency review gates
pull requests. Dependabot keeps the module and the workflows current.

## Linting

`.golangci.yml` starts from the standard linter set and layers on bodyclose,
copyloopvar, errorlint, gosec, intrange, misspell, modernize, nolintlint,
perfsprint, revive, unconvert, unparam, usestdlibvars and whitespace, with
gofumpt and gci as formatters. No issue caps: every finding fails the build.
