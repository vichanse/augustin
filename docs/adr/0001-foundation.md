# ADR 0001: Harness-first Go runtime

Status: Proposed

Use Go and the standard library initially for a portable, testable CLI. Rust offers stronger compile-time guarantees but adds development complexity for this project. Agent frameworks may accelerate prototypes but obscure runtime behavior and introduce dependencies.

Keep provider interfaces independent of orchestration. Treat model output, skills and workspace contents as untrusted. Enforce policy outside the model. Workspace path checks alone are not a sandbox; command execution requires isolation and explicit authorization.

Start with deterministic unit tests and add real-model evaluations separately. Revisit these choices as evidence accumulates.
