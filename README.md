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
- [Engineering instructions](AGENTS.md)

## Contributing

Discuss changes in issues and submit focused pull requests with tests. Never include credentials or private data.

## License

Apache-2.0. See [LICENSE](LICENSE).
