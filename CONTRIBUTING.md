# Contributing

Issues and pull requests are welcome. rx is an early MVP, so please open an issue before starting on larger changes.

## Setup

You need [Go](https://go.dev/dl/) (version in [`go.mod`](go.mod)), `zsh` and `bash` 4.1+ for the widget tests (they are skipped without them; the bash tests also need zsh, whose zpty module drives the terminal) and [golangci-lint](https://golangci-lint.run) for linting.

```sh
make build   # bin/rx
make test    # go test ./... (TESTFLAGS=-race CGO_ENABLED=1 for the race detector)
make lint    # golangci-lint run
make help    # all targets
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for how the pieces fit together.

## Pull requests

- Keep changes focused and add tests for new behavior.
- Run `make fmt lint test` before pushing. CI runs the same checks.
- Use [Conventional Commits](https://www.conventionalcommits.org) for commit and PR titles, e.g. `feat(tui): add spinner theme role` or `fix(zsh): restore cursor position`.
- Update the README when you change user-facing behavior.

## Adding a shell

Start with the widget contract in [`internal/shell/proto`](internal/shell/proto/proto.go) and the registry in [`internal/shell/shell.go`](internal/shell/shell.go). A new shell needs a widget script under `internal/shell/<name>/`, a history reader and a registry entry. Its widget tests can reuse `internal/shell/testdata/fake-rx`, which stands in for rx, and `internal/shell/testdata/driver.zsh`, which plays keys against an interactive shell in a pty. See `internal/shell/bash` for an example.

## Releases

Maintainers release by pushing a `vX.Y.Z` tag. The [release workflow](.github/workflows/release.yml) runs GoReleaser and publishes binaries for Linux and macOS to GitHub Releases. Try it locally with `make snapshot`.
