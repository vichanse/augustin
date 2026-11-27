# ADR 0002: Interactive action approval

Status: Proposed

## Context

Future edit and execute tools need a human decision per action. The policy in `internal/tools` already gates capabilities and calls an `Approver`, but nothing connected it to the terminal, and a refusal was reported as a policy violation that ended the run.

## Decision

- The policy stays the only enforcement authority. It decides whether an action may be approved at all (mode and capability) before asking anyone. Unknown capabilities, read-only and plan modes, and policies without an approver are denied without prompting.
- `internal/approval.Broker` implements the approver. It runs in the agent goroutine, publishes one immutable, bounded request snapshot with a unique ID, and waits for the decision, the request context, or `Close`. It has no Bubble Tea dependency.
- The shell only renders the request and calls `Resolve(id, approved)`. Decisions for any other ID are ignored, so stale or duplicate key presses cannot authorize another action. The Bubble Tea update loop never blocks.
- Only one request may be pending; a concurrent one fails with `ErrBusy`. Without an attached UI (headless `run`), requests fail with `ErrClosed`.
- An explicit refusal returns `tools.ErrRejected`, reported to the model as the recoverable `user_rejected` tool error. Policy violations keep returning the terminal `ErrDenied`.
- An action identical to one already refused in the session (same capability, tool and raw arguments) is refused again without prompting. Approvals are never remembered: each approved action is asked for individually.
- The registry copies the call arguments before authorization, so the handler runs the exact action that was shown.
- There is no approve-all option, no persistent grant, and workspace trust grants nothing. The model cannot change the mode or the policy.

## Timeout while an approval is pending

The approval waits on the turn context. Waiting counts against the turn budget (`AUGUSTIN_TIMEOUT_SECONDS`, 120 s by default): the deadline is neither paused nor extended. When it expires, the request is withdrawn, the action is not executed, and the turn fails with `deadline`. Esc rejects the action; Ctrl+C interrupts the whole turn, which also withdraws the request; closing the shell releases it with `ErrClosed`.

## Consequences

- The production CLI remains read-only: `ReadPolicy` never asks for approval. The approval path is exercised by tests with a fake `fake_write` tool that is never offered to a real model.
- A slow decision can consume most of the turn budget. Pausing the deadline during approval is deliberately deferred until real edit tools exist and their budgets are known.
- Refusals are remembered by raw arguments, so a model can trigger a new prompt by changing whitespace. This costs the user a keypress, never an unapproved action.
