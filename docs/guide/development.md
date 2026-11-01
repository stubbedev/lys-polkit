# Development

Every dev tool (Go toolchain at the version `go.mod` declares, gopls,
golangci-lint, delve, just, git, gh, dbus-daemon for the tests) comes from the
flake: `direnv allow` on entry, or `nix develop` explicitly.
`GOTOOLCHAIN=local` keeps the compiler from downloading itself.

D-Bus goes through [lys-lib](https://github.com/stubbedev/lys-lib)'s patched
godbus and `dbusx`. The dialogs' contract is lys-lib's `portaldialogs`.

## Recipes

| Recipe | What it does |
| --- | --- |
| `just check` | Every release gate in order: vet, lint, test, build |
| `just vet` | Static analysis with `go vet` |
| `just lint` | `golangci-lint run` against the strict config in `.golangci.yml` |
| `just test` | `go test ./...` |
| `just build` | Compile-check every package |
| `just fmt` | Format every Go source in place |

## Tests

The tests run without polkitd, logind or the shell: fakes of the polkit
Authority, logind and the shell's dialogs serve on private dbus-daemons
(lys-lib's `dbustest`), and the test binary itself plays
`polkit-agent-helper-1` over both transports.

## Nix

- `nix build` builds the package with the user unit in `lib/systemd/user`
  (`nix/lys-polkit.nix`; `vendorHash` follows `go.sum`).
- `nix flake check` evaluates the home-manager module and checks the unit is
  wired to the graphical session. On x86_64 with KVM it also runs
  `tests/polkit-vm.nix`: a NixOS VM where a real polkit 127 authenticates
  `pkexec` through lys-polkit, with `internal/vmtest/fakedialogs` answering for
  lys-shell's dialogs from a script (right password, a wrong one then the right
  one, cancel, and no agent once lys-polkit stops).
- `nix fmt` formats the nix files with alejandra.

## CI

`.github/workflows/ci.yml` runs `just check` in the dev shell, and
`nix flake check`. `.github/workflows/security.yml` runs CodeQL (Go and GitHub
Actions), Grype and govulncheck, uploading findings to code scanning;
dependency review gates pull requests. Dependabot keeps the Go module and the
workflows current.

## Linting

`.golangci.yml` starts from the standard linter set and layers on bodyclose,
copyloopvar, errorlint, gosec, intrange, misspell, modernize, nolintlint,
perfsprint, revive, unconvert, unparam, usestdlibvars and whitespace, with
gofumpt and gci as formatters. No issue caps: every finding fails the build.
