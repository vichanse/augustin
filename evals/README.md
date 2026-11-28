# Evaluation harness

Run the deterministic integration scenario with:

```sh
go test -v ./evals/...
```

The discount fixture adds a discount instead of subtracting it. The test copies it into a temporary workspace, runs the real agent loop and authorized read tool, and checks the diagnosis, observed successful read, iteration count and unchanged fixture.

This is a runtime regression test with a deterministic provider, not a benchmark of LLM reasoning or autonomous bug fixing. The provider deliberately recognizes the fixture. The Go test runner reports failures independently of the provider's claims.

## Bug-fixing evaluation

`cases/` holds the bug-fixing dataset and `augustin-eval` runs it with hidden evaluator tests, deterministically or against a real model. See [the evaluation quickstart](../docs/evaluation-quickstart.md), including how to add a case, and ADR 0007.
