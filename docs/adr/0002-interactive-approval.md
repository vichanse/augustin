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
- An action identical to one already refused in the session (same capability, tool and arguments, compared after JSON compaction) is refused again without prompting. Compaction only removes whitespace outside strings, so it can only widen what counts as refused and never changes what executes. Keys are not reordered and values are not reinterpreted. Approvals are never remembered: each approved action is asked for individually.
- The registry copies the call arguments before authorization, so the handler runs the exact action that was shown.
- There is no approve-all option, no persistent grant, and workspace trust grants nothing. The model cannot change the mode or the policy.

## Only a complete preview can be approved

Approval means approving the exact action, so the user must be able to see all of it:

- The broker bounds the tool name to 64 bytes and the arguments to 4 KiB, and marks the request `Truncated` if either was cut. `Resolve(id, true)` on a truncated request is refused and the request stays pending: it can only be denied, whatever the UI does.
- The shell also requires the complete panel (border, labels, every action line, options) plus the mode line and the key hint to fit the actual terminal size. It renders the panel and measures it; there are no assumed minimum dimensions. While an approval is pending, the live area above the panel is cropped from the top so it never pushes the panel off screen. This is re-checked in the decision path on every Enter, not only when drawing. On an incomplete preview, arrow keys keep Deny selected; on a complete one they toggle between Deny and Approve. A resize that makes the preview incomplete moves the selection back to Deny, and a resize that makes it complete never selects Approve.
- The panel says why approval is disabled (size limit or terminal size).

The preview must not hide details either:

- Characters that would not show as themselves (controls, bidi and zero-width format characters, non-ASCII spaces) are written as JSON `\uXXXX` escapes instead of being stripped. Since the policy only executes valid JSON arguments, the preview is an equivalent JSON text that decodes to exactly the executed value. Invalid UTF-8 bytes appear as `\xNN`.
- Lines are wrapped by display width, character by character, so no space is dropped at a break.
- JSON compaction only removes insignificant whitespace; string contents are unchanged.

## Timeout while an approval is pending

The approval waits on the turn context. Waiting counts against the turn budget (`AUGUSTIN_TIMEOUT_SECONDS`, 120 s by default): the deadline is neither paused nor extended. When it expires, the request is withdrawn, the action is not executed, and the turn fails with `deadline`. Esc rejects the action; Ctrl+C interrupts the whole turn, which also withdraws the request; closing the shell releases it with `ErrClosed`.

## Consequences

- At the time of this decision the production CLI was read-only. ADR 0003 adds `edit_file` in interactive edit mode; its approvals go through this broker with the prepared diff in `Request.Diff`.
- A slow decision can consume most of the turn budget. Pausing the deadline during approval is deliberately deferred until real edit tools exist and their budgets are known.
- Large actions (over 4 KiB, or taller than the terminal) cannot be approved in this design. Future edit tools will need a paged or diff-based preview before they can propose large changes.
- Visually confusable but distinct printable characters (homoglyphs such as Latin `a` and Cyrillic `а`) are shown as themselves. They are visible, but a user may not notice the difference.
- A model can still trigger a new prompt by changing key order or number formatting. This costs the user a keypress, never an unapproved action.
