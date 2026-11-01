# lys-polkit dev tasks.

# List all recipes with their descriptions.
default:
    @just --list

# Run every release gate in order: vet, lint, test, build.
check: vet lint test build

# Static analysis of every package with go vet.
vet:
    go vet ./...

# Lint every package; settings live in .golangci.yml.
lint:
    golangci-lint run

# Run the test suite for every package.
test:
    go test ./...

# Compile-check every package; the output is discarded.
build:
    go build -o /dev/null ./...

# Format every Go source in place with gofmt.
fmt:
    gofmt -w .

# Set nix/lys-polkit.nix's vendorHash to the hash of the modules go.sum pins.
vendor-hash:
    nix run github:stubbedev/lys-ci#vendor-hash -- nix/lys-polkit.nix .#default.goModules
