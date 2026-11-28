# Evaluation quickstart

`augustin-eval` measures whether Augustin fixes bugs, using hidden evaluator tests the agent never sees (see ADR 0007).

## Deterministic (no network, no API key)

```sh
go run ./cmd/augustin-eval --list          # cases and scripted scenarios
make eval                                  # all scenarios, expected outcomes checked
go run ./cmd/augustin-eval --case pagination --json report.json
```

Exit status 0 means every scenario produced its expected outcome. This is a test of the framework, not of a model.

## Real model (explicit opt-in)

Run only in a disposable environment: project tests execute model-modified code on this machine, and temporary workspaces are not a sandbox. Do not expose unrelated credentials.

```sh
export AUGUSTIN_PROVIDER=openai
export AUGUSTIN_MODEL=<your-model>
export AUGUSTIN_ALLOW_REMOTE_DATA=true
read -s OPENAI_API_KEY && export OPENAI_API_KEY

go run ./cmd/augustin-eval --mode real --smoke                     # read-only smoke test (M1.6)
go run ./cmd/augustin-eval --mode real --case discount
go run ./cmd/augustin-eval --mode real --all --repeat 3 --json eval.json
make eval-real EVAL_ARGS="--all --max-duration 20m"
```

Per-turn budgets come from the usual variables (`AUGUSTIN_MAX_ITERATIONS`, `AUGUSTIN_MAX_TOOL_CALLS`, `AUGUSTIN_TIMEOUT_SECONDS`); `--max-duration` bounds the whole run. Costs appear only with explicit prices: `--input-price 0.25 --output-price 2` (USD per million tokens). Exit status 0 means every attempt was resolved; 1 means at least one was not; 130 means canceled.

## Reading the summary

Illustrative layout, not a measured result:

```
CASE              RESULT       TIME    TOKENS   VERIFICATION   HIDDEN
discount          RESOLVED     12.4s   4210     passed         passed
regression-trap   UNRESOLVED   31.2s   9250     passed         failed

Resolved: 1/2 (50%) · denominator excludes 0 infrastructure error(s)
```

`VERIFICATION` is the harness verdict the user would have seen; `RESULT` is the independent verdict. A row with `passed` verification and an unresolved result means the visible tests were not enough. Infrastructure errors (broken fixture, missing toolchain) are never counted as agent results.

## Adding a case

1. Create `evals/cases/<id>/` with a `case.json` (see an existing case; `id` must match the directory), the buggy module under `workspace/` and the evaluator tests under `hidden/`. Every file ends in `.txt` (`go.mod.txt`, `foo.go.txt`, `foo_test.go.txt`); hidden test files must not reuse a workspace file name.
2. Keep it small and standard-library only. The prompt describes the expected behaviour, not the fix. `allowed_paths` lists the non-test source files the agent may change. `baseline_failure` is a string the buggy module's `go test ./...` output must contain.
3. Add a `reference` scenario (and ideally a failing one) to `Scenarios` in `evals/scripts.go`, then run `go test ./evals` and `make eval`.
4. Bump `DatasetVersion` in `evals/dataset.go` when changing existing cases.
