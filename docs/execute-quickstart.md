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

Also check, in a fresh copy: when you deny the first `go test` request, no test result is reported, the activity line shows `! run_command (user_rejected)`, and Augustin explains that it could not run the tests.

Notes:

- Sessions always start read-only. `/mode` cannot change during a turn or while an approval is pending.
- Esc or Ctrl+C during a run stops the command and its children.
- `augustin run` (headless) never edits or runs commands.
- Commands get a minimal environment: your API key and other credentials are not passed to them.
