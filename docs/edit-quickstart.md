# Edit quickstart

Interactive edit mode lets Augustin change existing files, one exact replacement at a time, only after you approve the diff. Use a disposable workspace: there is no undo, and nothing runs your tests.

```sh
export AUGUSTIN_PROVIDER=openai
export AUGUSTIN_MODEL=<your-model>
export AUGUSTIN_ALLOW_REMOTE_DATA=true
read -s OPENAI_API_KEY && export OPENAI_API_KEY
workspace=$(mktemp -d)
cp evals/fixtures/buggy-pricing/discount.go.txt "$workspace/discount.go"
go build -o augustin ./cmd/augustin
cd "$workspace" && "$OLDPWD/augustin"
```

Trust the workspace for this session, then:

```
/mode edit
Find and fix the bug in discount.go.
```

Expected:

1. Augustin reads `discount.go` and identifies `price + discount` as the wrong operation.
2. The approval panel shows the path and a diff replacing `return price + discount` with `return price - discount`. Deny is selected.
3. **Deny** (Enter or Esc): `discount.go` is unchanged and Augustin reports the refusal.
4. In a fresh copy of the workspace, select **Approve once** with ↓ and press Enter: the file now subtracts, and Augustin reports the change and says tests were not run.

Check with `cat discount.go`. Use a new workspace copy for each scenario.

Notes:

- Sessions always start read-only; `/mode` cannot change during a turn or while an approval is pending.
- `augustin run` (headless) never edits.
- After an approved edit, a **Harness verification** block says `Verification pending — files modified, tests not run`: edit mode cannot verify. To also run tests after an edit, use `/mode execute` (see [the execute quickstart](execute-quickstart.md)).
- A change too large to show completely in the terminal cannot be approved; enlarge the terminal or ask for a smaller edit.
- Do not edit a workspace that another program or editor is writing to (see ADR 0003).
