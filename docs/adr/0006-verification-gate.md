# ADR 0006: Harness-owned verification gate

Status: Proposed

## Context

In execute mode the model decided alone whether to run tests and when to stop, and its final answer could claim success without evidence. The harness must track verification itself and never present unverified work as verified.

## Decision

- **Evidence comes from the execution boundary.** `tools.Registry` emits typed `tools.Evidence` for every prepared action: `applied` (edit written, or command ran until it exited or timed out, with its status and exit code), `rejected` (approval refused) or `failed` (approved but not completed: canceled, changed file, start error). Nothing is inferred from assistant messages, model JSON, stdout text or `ToolCompleted` events. Tool results returned to the model are unchanged.
- **`internal/verification.Tracker` owns the verdict** for one turn. Each applied edit increments the mutation revision. A test run counts only if the approved request is exactly `go test ./...`, its evidence is `applied`, its status `exited` and its exit code 0; it then covers the current revision, and any later edit invalidates it. `go build`, `go vet`, `git diff` and `git status` never verify. The latest run on the current revision decides: a timeout after a pass is a failure. A command whose `Run` fails for any reason other than a normal process exit (for example a child holding output open past the wait delay) reports `status: "error"`, whatever its exit code, and leaves verification `incomplete`. Statuses: `not_required` (no edit), `pending`, `passed`, `failed` (non-zero exit or timeout after the last edit), `rejected` (the user declined the test run after the last edit), `incomplete` (the run did not complete, or the gate ran out of budget or retries).
- **Completion gate in `agent.Loop`.** When the model finishes, normally or with a truncated answer, the loop asks the tracker. In execute mode:
  - **Test run due** (an edit not covered by a run, or an interrupted run): the harness requests `go test ./...` itself instead of relying on the model to call the tool. Manual testing showed a real model asking for approval in prose until the continuations ran out. The call goes through the same execution path as model calls (events, budgets, registry, approval panel, typed evidence) and is carried by a separate assistant message, followed by its tool result and a harness note, which keeps provider message sequencing valid. The model then gets one more call to synthesize, or to fix the code if the tests failed. This needs a tool call and a later model call; it only happens if the turn offers `run_command`, so never in read-only or edit mode.
  - **Tests failed:** the model already has the output, so the loop appends a harness instruction to fix and rerun, which needs a non-final iteration for the edit.
  - Both kinds of continuation share a limit of two per turn, and the reserved final response never regains tools: harness runs happen between model calls. They consume the existing iteration, tool-call, output and timeout budgets. After a rejection the gate does not insist, and the broker already refuses an identical retry without asking again. When the gate cannot continue, the verdict becomes `incomplete` with the reason.
- **Nothing runs without approval.** The gate only talks to the model; every edit and test run still goes through the policy and the approval panel.
- **Edit mode** never asks for tests: changes are reported as `pending` with the reason that tests cannot run there. Read-only turns have nothing to verify and show no verdict.
- **Authoritative report.** `agent.Result.Verification` carries the report, also when the run fails or is canceled after an edit; `ui.TurnResult` passes it to the shell, which prints a "Harness verification" block after, and apart from, the assistant's Markdown. Unless the status is `passed`, the block states that any claim of passing tests in the answer is not backed by harness evidence.
- **Activity colors come from outcomes.** In the shell, a running tool is a gray `◌`, a success a green `✓`, a failure or non-zero exit a red `✗`, and a rejected, canceled, timed-out or errored run a yellow `!`. Only the glyph is colored; the tool label keeps the default foreground, and `NO_COLOR` disables colors. `ToolCompleted` only means a tool returned: for commands the mark comes from the registry evidence (status and exit code), delivered to the shell through a display-only `ToolEvidence` hook that persistent telemetry does not implement.

## Consequences

- **This is evidence of one test result, not proof of correctness.** A passing `go test ./...` says nothing about untested behavior, flaky tests or the quality of the tests.
- **External changes are invisible.** The tracker only sees edits made through `edit_file`. Files changed by another program, or by project code that tests or builds execute, are not detected and do not invalidate a pass.
- **Per turn only.** Each turn starts at `not_required`; a later turn does not know that an earlier one left changes unverified.
- The decision to run the tests is deterministic, but the run still needs the user's approval. A model can ignore a failure instruction; after two continuations or when the budget runs out, the verdict is `failed` or `incomplete`, never `passed`.
- The harness note and the separate assistant message are model-visible conversation turns created by Augustin, not by the model.
- Continuations use the turn budget: a slow test suite may need a higher `AUGUSTIN_TIMEOUT_SECONDS` or `AUGUSTIN_MAX_ITERATIONS`.
