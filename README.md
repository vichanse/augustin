# Augustin

**The harness-first coding agent.**

Augustin is an open-source project building a model-agnostic coding agent for reliable, verifiable software engineering.

## Status

Early development: the current CLI is a scaffold, not yet a functional coding agent.

## Principles

- Harness-first: enforce permissions and execution limits outside the model.
- Eval-driven: measure task outcomes independently of model claims.
- Model-agnostic and local-first.
- Build in public with small, reviewable changes.

## Getting started

Requires Go 1.25 or later and Make.

```sh
make check
go run ./cmd/augustin --help
```

## Documentation

- [Vision](docs/vision.md)
- [Roadmap](docs/roadmap.md)
- [Architecture decision](docs/adr/0001-foundation.md)
- [Interactive approval](docs/adr/0002-interactive-approval.md)
- [Controlled file editing](docs/adr/0003-controlled-file-editing.md)
- [Edit quickstart](docs/edit-quickstart.md)
- [Controlled command execution](docs/adr/0004-controlled-command-execution.md)
- [Execute quickstart](docs/execute-quickstart.md)
- [Compact paste](docs/adr/0005-compact-paste.md)
- [Verification gate](docs/adr/0006-verification-gate.md)
- [Engineering instructions](AGENTS.md)

## Contributing

Discuss changes in issues and submit focused pull requests with tests. Never include credentials or private data.

## License

Apache-2.0. See [LICENSE](LICENSE).
