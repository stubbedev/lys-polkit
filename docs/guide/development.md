# Development

The flake's dev shell provides Go (the `go.mod` version), gopls,
golangci-lint, delve, just, git, gh, dbus and getent, with `CGO_ENABLED=0` and
`GOTOOLCHAIN=local`. Enter it with `direnv allow` or `nix develop`.

D-Bus goes through [lys-lib](https://github.com/stubbedev/lys-lib)'s godbus
fork and `dbusx`; the dialog contract is lys-lib's `portaldialogs`.

## Recipes

| Recipe | Effect |
| --- | --- |
| `just check` | `vet`, `lint`, `test`, `build` in order |
| `just vet` | `go vet ./...` |
| `just lint` | `golangci-lint run` with `.golangci.yml` |
| `just test` | `go test ./...` |
| `just build` | Compile-checks every package |
| `just fmt` | `gofmt -w .` |
| `just vendor-hash` | Updates `vendorHash` in `nix/lys-polkit.nix` from `go.sum` |
| `just release-patch` / `release-minor` / `release-major` | Tags and pushes the next release from a clean default branch with green CI |

## Tests

`go test` needs no polkitd, logind or shell: fakes of the polkit Authority,
logind and the dialogs serve on private dbus-daemons (lys-lib's `dbustest`),
and the test binary acts as `polkit-agent-helper-1` over both socket and
setuid transports.

## Nix

| Command | Effect |
| --- | --- |
| `nix build` | Builds the package with the user unit in `lib/systemd/user` (`nix/lys-polkit.nix`) |
| `nix flake check` | Checks the home-manager module's unit wiring; on x86_64 also runs the NixOS VM test `tests/polkit-vm.nix` |
| `nix fmt` | Formats nix files with alejandra |

The VM test runs `pkexec` through lys-polkit against a real polkitd, with
`internal/vmtest/fakedialogs` scripting the dialog: right password, details,
a name-service-only user, wrong then right password, cancel, the setuid helper
without the socket, `auth_admin_keep` listing/reuse/revoke/expiry, and no
agent after lys-polkit stops.

## CI

| Workflow | Runs |
| --- | --- |
| `ci.yml` | `just check` in the dev shell on x86_64 and aarch64, `nix build` on aarch64, `nix flake check` |
| `security.yml` | CodeQL, Grype, govulncheck (to code scanning); dependency review on pull requests |
| `vendor-hash.yml` | Updates `vendorHash` on pull requests that change `go.mod` or `go.sum` |
| `docs.yml` | Builds and publishes this site on pushes to `main` |

Dependabot updates Go modules, GitHub Actions and the docs' npm packages.

## Linting

`.golangci.yml`: standard linters plus bodyclose, copyloopvar, errorlint,
gosec, intrange, misspell, modernize, nolintlint, perfsprint, revive,
unconvert, unparam, usestdlibvars and whitespace; formatters gofumpt and gci.
No issue caps.
