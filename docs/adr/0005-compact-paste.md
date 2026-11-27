# ADR 0005: Compact paste in the composer

Status: Proposed

## Context

Large multiline pastes filled the composer and the conversation scrollback. The textarea also silently truncated pastes at its 8192-character limit. The goal is the compact form `[Pasted text #1 +216 lines]` while the model still receives the exact pasted text.

## Decision

- **Detection.** Bubble Tea delivers a bracketed paste as one `KeyMsg` with `Paste` set; its content is never parsed as keys and its `String()` is wrapped in `[...]`, so a paste can never match Enter, Esc, a shortcut or an approval key. Typed input, including multiline input with Alt+Enter, is never compacted.
- **When a paste becomes a block.** At least 5 logical lines or 1,000 characters; also, to keep content exact and the terminal safe, any paste containing control characters (other than newline, tab and carriage return), containing the marker runes below, or not fitting in the textarea's character limit. Other pastes stay ordinary text, as before. A paste over 1 MiB is refused with an explicit notice; nothing is truncated.
- **Representation.** Each block is one rune from the Supplementary Private Use Area-B (U+100000 + n) inserted in the composer, with its content kept in memory under that rune. IDs increase for the whole session and are never reused. Because a block is a single character to the textarea, left/right step over it in one move and Backspace or Delete remove it whole, without intercepting editing keys.
- **No string replacement.** Only marker runes are ever rendered as labels or expanded. Text typed to look like `[Pasted text #1 +5 lines]` stays literal.
- **Display versus submission.** The composer view, the live area and the scrollback show labels (`+N lines`, or `· N chars` for one line). The `TurnFunc` prompt, the model history and saved sessions get the expanded text, so a resumed session never contains labels. A prompt containing a block is never a local slash command.
- **Lifetime.** After each update, blocks no longer referenced by the composer, the saved draft, the submitted prompt or the prompt history are dropped. Deleting a block or clearing the composer therefore frees its content, while ↑ recall and the restore of a failed or canceled turn keep theirs. There is no persistent paste store.

## Consequences

- Prompt history (up to 100 entries) keeps its pasted content in memory for the session, up to 1 MiB per block.
- The textarea wraps a block as one cell; a line that a label pushes past the width is rewrapped when drawn, so the composer can show one or two more lines than its computed height.
- Small pastes keep the textarea's existing normalization (tabs and CRLF line endings are converted). Only blocks are preserved byte for byte as delivered by Bubble Tea, which already drops invalid UTF-8.
- At most 65,534 blocks per session; beyond that, pastes are refused with a notice.
