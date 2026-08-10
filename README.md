# Axiom

Observability and efficiency toolkit for AI coding agents.

Axiom helps developers understand and reduce the context, token usage, cost,
latency, and redundant work produced by AI coding agents — without sacrificing
correctness.

> **Status:** early foundation. This repository currently ships a minimal CLI
> scaffold (`help` / `version`). Agent integrations and analysis features will
> land in follow-up PRs.

## Requirements

- Go 1.26 or newer

## Install from source

```bash
git clone https://github.com/exequieldeferrari/axiom.git
cd axiom
make build
./bin/axiom version
```

## Usage

```bash
axiom           # show help
axiom help      # show help
axiom version   # print version
```

## Development

```bash
make build   # build ./bin/axiom
make test    # go test ./...
make lint    # gofmt check + go vet
make run     # go run ./cmd/axiom
```

Release builds can override the version string:

```bash
go build -ldflags "-X github.com/exequieldeferrari/axiom/internal/version.Version=v1.2.3" -o bin/axiom ./cmd/axiom
```

## License

[Apache License 2.0](LICENSE)
