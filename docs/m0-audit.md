# M0 readiness review — 2026-11-26

Scope: static review of agent loop, workspace tools, events, engineering instructions and Makefile. PR #14 CI succeeded (run 45793525751). This is not a penetration test or a full repository audit.

## Findings and gates

| Priority | Finding | Required action |
| --- | --- | --- |
| P0 | Workspace reads may expose credentials or private repository data to a remote provider. | Require explicit workspace/data consent and a reviewed file access policy before enabling remote reads. |
| P0 | os.Root confines path resolution but does not isolate mounts, hard links or concurrent special-file replacement. | Use trusted disposable workspaces; isolate untrusted execution before enabling writes or commands. |
| P0 | Iteration limit does not bound tool calls, cumulative output or model cost. | Add explicit tool and output budgets, request limits and a run deadline. |
| P1 | Tool arguments accept unknown JSON fields; tool IDs are checked only within one response. | Validate argument contracts and define run-wide call identity semantics. |
| P1 | Cancellation may leave unmatched lifecycle events; sink failures do not fail the run. | Define terminal event semantics and audit failure policy. |
| P1 | Event sequences restart per run; shared sinks cannot correlate runs. | Add run identifiers and timing metadata. |
| P1 | Deterministic evaluation checks runtime wiring, not model capability or patch correctness. | Keep issue #8 open; add real-model and independent patch/test evaluation. |

## Engineering retrospective

PR #14 failed formatting and then a Makefile escaping regression. The final CI passed. Before proposing future code changes, run gofmt and make check in a compatible Go environment, verify the published diff and require successful CI. If local execution is unavailable, explicitly report that limitation and rely on CI before merge.

## M1 entry

Proceed with a bounded read-only agent, initially on trusted synthetic fixtures. Remote model integration must not silently transmit private workspace contents. No unrestricted shell or autonomous writes. Resolve P0 gates before enabling the corresponding capabilities. M0 is a foundation, not a production security guarantee.
