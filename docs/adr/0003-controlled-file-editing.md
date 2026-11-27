# ADR 0003: Controlled file editing

Status: Proposed

## Context

Augustin should be able to fix a simple bug in an existing file, with a human approving every change. ADR 0002 provides the approval broker and panel; nothing could write to the workspace yet.

## Decision

- **One tool, one operation.** `edit_file` replaces one exact, non-empty `old_string` that occurs exactly once (overlapping occurrences count) in an existing UTF-8 text file. `new_string` may be empty. Arguments must be exactly these three strings: unknown, duplicate or null fields and trailing data are rejected. There is no whole-file write, file creation or deletion.
- **Prepare, approve, apply.** The registry runs `permit` (mode and capability) before reading anything, then the preparer, then asks for approval of the prepared diff, checks cancellation, and calls `Apply`. A prepared tool is always treated as a mutation: it is denied if no policy is set or if it were ever declared `Read`.
- **Preparation has no side effects.** It reuses the read path protections (`localPath`, `checkedStat`: no absolute paths or traversal, no symlink components or targets, no sensitive names, regular files only, 1 MiB limit), rejects files and results that are not UTF-8 or contain NUL, computes the new content, a single-hunk unified diff with three context lines, the file identity and a SHA-256 of the original.
- **Approval binds to that exact preparation.** The proposal shown to the user carries the path and diff; `Apply` is a closure over the prepared content, original identity and hash. The arguments are frozen before preparation. Diffs over 4 KiB are rejected before anyone is asked (`diff_too_large`); the broker bounds them again as defense in depth, and the panel keeps the complete-preview rule of ADR 0002.
- **Apply revalidates, then replaces atomically.** It re-reads the target through the same protections and requires the same file (`os.SameFile`) with the same hash; otherwise it fails with `file_changed`. It writes a new `.augustin-edit-*.tmp` file in the same directory through the workspace `os.Root`, syncs it, applies the original permission bits, checks cancellation, and renames it over the target. The original is never truncated; the temporary file is removed on any failure.
- **Modes are a local user choice.** `/mode read-only` (default at every launch) and `/mode edit` are shell commands, refused during a turn or a pending approval. Each turn snapshots the mode. Only an interactive edit-mode turn with an approval broker gets `edit_file`, the edit system prompt and `EditPolicy`; headless `run` always uses `ReadPolicy` without an approver. Edit mode is never persisted, and workspace trust grants nothing.
- **Results are explicit.** A successful edit returns `{"status":"applied", "path", "removed_lines", "added_lines", "note"}` and states that no tests or commands were run. Failures reach the model as recoverable tool errors (`user_rejected`, `file_changed`, `old_string_not_found`, `old_string_ambiguous`, `no_op_edit`, `not_text_file`, `diff_too_large`, `invalid_arguments`) or as terminal `permission_denied`.
- **Diff display stays faithful.** File text is shown with tabs as `→` plus padding and other non-printing characters as `⟨U+XXXX⟩`; the marker characters themselves are escaped, so every rendering maps back to one text. Removed lines are red, added lines green and hunk headers cyan when color is enabled; plain text otherwise.
- Telemetry keeps metadata only: tool name and error reason, never paths, contents or diffs.

## Consequences

- **Concurrent writers are not fully excluded.** The identity and hash check narrows the window but cannot close it: another process can modify the file between the check and the rename, and that change is then lost. `os.Root` keeps every operation inside the workspace, but the symlink checks are not race-free either (see `checkedStat`). Augustin should only edit workspaces that nothing else is writing to.
- Rename replaces the inode: ownership, extended attributes and hard links to the original are not preserved. Only the ordinary permission bits are copied.
- A crash after the rename leaves the new content in place; a crash before it may leave a `.augustin-edit-*.tmp` file behind.
- Large edits and multi-file changes are not possible in this MVP; the model must split work into small exact replacements, each approved separately.
- Line endings are not normalized: a CRLF file stays CRLF only where the replacement text says so.
