# ADR 0004: Controlled command execution

Status: Proposed

## Context

After editing code (ADR 0003), Augustin should run the tests, read the results and iterate, with a human approving every run. Nothing could execute a program yet.

## Decision

- **One tool, an exact allowlist.** `run_command` takes exactly `{"command": string, "args": [string]}`; unknown, duplicate and null fields, non-string arguments and trailing data are rejected. Only these requests are accepted, compared argument by argument (never as a joined string): `go test ./...`, `go vet ./...`, `go build ./...`, `git diff`, `git status`. No flags, no other subcommands.
- **No shell.** The program is started with `exec.CommandContext` and separate arguments. Nothing interprets `;`, `|`, `>`, `$()` or globs; such text is simply not an allowed argument.
- **What runs is what is approved.** Each allowlist entry maps to a fixed argv. Git always runs as `git --no-pager -c core.fsmonitor=false -c core.pager=cat …`, with `diff --no-ext-diff --no-textconv --no-color`. The approval panel shows the resolved executable, every argument on its own numbered, Go-quoted line, the working directory and a warning that local project code may run. Deny is the default, incomplete previews cannot be approved (ADR 0002), and each approval covers one invocation.
- **Trusted executables.** The executable is looked up in the host `PATH` at preparation, keeping only absolute entries outside the workspace, and is rejected if it resolves (through symlinks) inside the workspace. The resolved path is what is executed.
- **Fixed working directory and minimal environment.** Commands run in the canonical workspace root. The model cannot set the directory or the environment. The environment is the filtered `PATH`, then `GOTOOLCHAIN=local`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, `GIT_PAGER=cat`, `PAGER=cat`, `GIT_OPTIONAL_LOCKS=0` and `NO_COLOR=1`, plus only `HOME`, `TMPDIR`, `LANG`, `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOPROXY` and `XDG_CACHE_HOME` from the host. Provider credentials such as `OPENAI_API_KEY`, and `GOFLAGS`, are excluded by construction.
- **Bounded lifecycle.** Each run has a 2-minute timeout, further bounded by the turn deadline (approval waiting counts against the turn, as in ADR 0002). Esc and Ctrl+C cancel the turn context, which stops the command. Stdin is `/dev/null`. On Unix the command gets its own process group; cancellation, the timeout and the end of the command kill the whole group, so no ordinary child keeps running.
- **Bounded output.** Stdout and stderr are captured separately into buffers that keep the first and last 8 KiB and only count the rest, so memory stays bounded however much a process prints. Escape sequences, control and format characters are removed and invalid UTF-8 is replaced before the output reaches the model.
- **Failures are results.** The result is JSON: `command`, `args`, `executable`, `exit_code`, `status` (`exited` or `timeout`), `duration_ms`, `stdout`, `stderr`, `stdout_truncated`, `stderr_truncated`. A non-zero exit code is returned to the model, not raised as an error. Cancellation or the turn deadline ends the turn. Disallowed commands and missing executables are recoverable tool errors (`command_not_allowed`, `executable_not_found`).
- **Modes.** `/mode execute` permits Edit and Execute, each action approved separately. Only interactive execute-mode turns expose `run_command` and the execute prompt, which tells the model that any non-zero `exit_code` is a failure and that it may only claim passing tests after `go test ./...` returned 0 after its last edit. The edit and execute prompts also tell the model to call `edit_file` and `run_command` directly instead of asking for permission in the conversation, and to run tests, edit and rerun tests in that order when asked. This is guidance only: approval is always enforced by the harness, and conversational consent never authorizes an action. Read-only remains the startup default, edit mode cannot run commands, headless `run` stays read-only, and trust grants nothing.
- Telemetry keeps metadata only: tool name and error reason, never commands' output.

## Consequences

- **This is not a sandbox.** An approved `go test` or `go build` compiles and runs project code with the user's privileges: tests, `init` functions, `TestMain` and cgo can read and write any file the user can, use the network and start programs. `go` may also download modules through `GOPROXY`. Only approve commands in workspaces whose code you trust.
- **Git can still run repository-configured programs.** Pager, external diff, textconv and fsmonitor helpers are disabled, and the system config is ignored, but repository or global configuration can still define, for example, clean/smudge filters that `git status` and `git diff` may run.
- **Process isolation is best effort.** A process can leave its process group (for example with `setsid`) and survive cancellation. Outside Unix only the direct child is killed. The group is killed right after the command exits, which closes a tiny window where its ID could be reused.
- The environment is minimal: projects relying on other variables (proxies, private module settings, `GOFLAGS`) may fail. `HOME` is passed so the Go caches work, which also lets project code find user configuration there.
- The allowlist is deliberately narrow: no `-run`, `-v` or package selection, so the model always runs the whole module. Widening it needs a new decision.
