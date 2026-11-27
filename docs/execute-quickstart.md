# Execute quickstart

Execute mode lets Augustin run a few allowlisted commands (`go test ./...`, `go vet ./...`, `go build ./...`, `git diff`, `git status`) after you approve each run, in addition to the approved edits of edit mode. **An approved test or build runs the project's own code with your privileges; this is not a sandbox** (see ADR 0004). Use a disposable workspace whose code you trust.

```sh
export AUGUSTIN_PROVIDER=openai
export AUGUSTIN_MODEL=<your-model>
export AUGUSTIN_ALLOW_REMOTE_DATA=true
read -s OPENAI_API_KEY && export OPENAI_API_KEY
go build -o augustin ./cmd/augustin
workspace=$(mktemp -d)
for f in go.mod discount.go discount_test.go; do
  cp "evals/fixtures/discount-module/$f.txt" "$workspace/$f"
done
cd "$workspace" && go test ./...   # fails: FinalPrice(50, 10) = 45, want 40
"$OLDPWD/augustin"
```

Trust the workspace for this session, then:

```
/mode execute
Run the tests, identify the bug, fix it and run the tests again.
The discount is an absolute amount, not a percentage.
```

Expected:

1. The approval panel shows `Executable: …/go`, `[1] "test"`, `[2] "./..."`, the workspace directory and the warning. Approve.
2. The tests fail (`FinalPrice(50, 10) = 45, want 40`).
3. Augustin reads `discount.go` and proposes replacing `return price - price*discount/100` with `return price - discount`. Approve the diff.
4. Augustin asks to run `go test ./...` again. Approve: the tests pass.
5. Augustin reports the change and both test results, with their exit codes.

### Harness verification

After any approved edit, Augustin prints a **Harness verification** block below the answer. It is computed from what actually ran, not from the model's words (see ADR 0006).

In a fresh copy of the workspace, ask without mentioning tests:

```
/mode execute
Find and fix the bug in the discount calculation.
```

Expected: Augustin reads `discount.go` and proposes the fix; approve the diff. If the model tries to finish without running the tests, the harness sends it back, and an approval panel for `go test ./...` appears. Approve it: the tests pass and the block shows:

```
Harness verification
✓ Verification passed
Files modified: 1 (discount.go)
Command: go test ./...
Exit code: 0
Tests passed after the last approved edit.
```

Repeat in another fresh copy and **deny** the `go test ./...` request: nothing runs, no second approval panel appears, and the block shows `✗ Verification rejected — the user declined test execution`.

Repeat with `/mode edit` instead: no test is requested and the block shows `! Verification pending — files modified, tests not run`.

Also check, in a fresh copy: when you deny the first `go test` request, no test result is reported, the activity line shows `! run_command (user_rejected)`, and Augustin explains that it could not run the tests.

Notes:

- Sessions always start read-only. `/mode` cannot change during a turn or while an approval is pending.
- Esc or Ctrl+C during a run stops the command and its children.
- `augustin run` (headless) never edits or runs commands.
- Commands get a minimal environment: your API key and other credentials are not passed to them.
