## Overview

rx is one Go binary plus a ~240-line zsh widget (bash and fish have their own widgets; see [bash](#bash) and [fish](#fish) at the end, the rest describes zsh). The widget is embedded in the binary and printed by `rx init zsh`. At runtime they are **two processes connected by a coprocess pipe**. The **Go side** does almost everything: parsing, classification, history, the TUI, and the plugins. The **zsh side** does two things only Go can't: it reads and writes the live `$BUFFER`, and it runs zsh's own completion system.

## Where zsh ends and Go begins

### 1. Bootstrap: Go prints zsh
- `eval "$(rx init zsh)"` → `newInitCmd` (`cmd/rx/init.go`) → `zsh.Script(opts)` (`internal/shell/zsh/zsh.go`).
- `rx.zsh` is compiled into the binary with `//go:embed`. Go prepends `typeset -g _rx_bin='/abs/path/to/rx'`, so the widget always calls the same binary that generated it, plus `_rx_key` and `_rx_log` from the config.
- **Config** (`internal/config`): viper reads `~/.config/rx/config.yaml`, and `RX_*` environment variables override it. The widget key and log file are baked in by `rx init`, where `RX_KEY`/`RX_LOG` set in the shell still win at bind time. The editor keymap (`tui.Keymap`) and color theme (`tui.Theme`, file only) are loaded by `rx edit`. The CLI is built with cobra (`cmd/rx/*.go`, one file per command).
- After that, everything in `internal/shell/zsh/rx.zsh` runs inside your interactive shell.

### 2. Key press: zsh starts Go
`rx-edit` (`rx.zsh:155`) is the ZLE widget bound to Ctrl-X Ctrl-X by `rx-bind`. It:
- Exports state that Go can't get any other way:
  - `RX_ALIASES`: escaped `name=value` lines taken from `$aliases`
  - `RX_HISTFILE`
  - `RX_COPROC=1`
- Runs `coproc rx edit --cursor $CURSOR -- "$BUFFER"` (`rx.zsh:181`) and sends stderr to `~/.cache/rx/rx.log`.
- Sets `nomonitor` so the coprocess stays in the shell's process group and can read the terminal.

### 3. The boundary: a line protocol over the coprocess pipes
Go's stdout is a request channel to zsh, and its stdin carries zsh's replies. The TUI does **not** draw on stdout. It opens `/dev/tty` directly (`main.go:239`). The protocol is documented at `internal/complete/zsh/zsh.go:4-16`:

| Direction | Message | Handled by |
|---|---|---|
| rx → zsh | `INFO <word> <marker…>` | `_rx_info`: alias expansion, `$_comps[cmd]`, and the first marker (e.g. `__complete` for cobra) found in that function |
| rx → zsh | `COMPLETE <line>` | `_rx_complete`: runs real zsh completion and streams back `C`/`M` lines, then `END` |
| rx → zsh | `DONE <cursor> <line> [run]` | sets `BUFFER`/`CURSOR`, ends the loop; with `run` (Ctrl-E) it then calls `zle accept-line` |
| rx → zsh | `CANCEL` / `ERROR <msg>` | restores the original buffer, optionally `zle -M` |

The fields are tab-separated and escaped the same way on both sides: `_rx_esc`/`_rx_unesc` in zsh, `complete.Escape`/`Unescape` in Go. The Go client is `zsh.Client` (`internal/complete/zsh/zsh.go:33`). It serializes requests with a mutex, because zsh can only run one completion at a time. If a request times out, the client is "poisoned" and the zsh backend is turned off for the rest of the session.

### 4. The clever zsh part: capturing completions
`_rx_complete` sets `BUFFER` to the synthesized line and calls `zle _rx_capture`. This is a completion widget (`zle -C`) that runs `_main_complete` while two functions are temporarily replaced:
- `compadd` → `_rx_compadd` (`rx.zsh:42`) records matches, descriptions and tags into `_rx_hits`/`_rx_descs`/`_rx_tags`. It still calls `builtin compadd` so completion functions behave normally.
- `_message` → `_rx_message` captures message text.

Then `compstate[insert]`/`list` are cleared so nothing gets shown or inserted. Context-specific `zstyle`s on `:completion:_rx_capture:*` turn off warnings and prompts without changing your own styles. The result is the same candidates that Tab would give you, including your aliases, env and zstyles.

### 5. Exit: zsh cleans up
`coproc :` closes the old pipes and `wait` reaps the process (`rx.zsh:202`). If no `DONE` arrived, the original buffer is restored. `zle reset-prompt` redraws the prompt.

## Go internals (everything inside `rx edit`)

1. **Parse** (`internal/parse`): tokenizes the buffer into segments (pipelines/`&&` etc.). `SegmentAt(pos)` picks the segment under the cursor.
2. **Specs/plugins** (`internal/spec`, `plugins/*.yaml`): bundled YAML via `embed.FS`, plus `~/.config/rx/plugins/`. Defines flags (value or bool, mutex), positionals, providers and transforms.
3. **Classify** (`spec/classify.go`): turns the segment into a `model.Command` of typed **slots** (`Positional`, `Flag`, `Bool`; `model/model.go:46`), resolving aliases. When no spec exists, history is used to guess which flags take values.
4. **History** (`internal/history`): indexes `$HISTFILE` into per-command/per-slot values ranked by frequency and recency. It's cached on disk and keyed by a salt built from specs and aliases (`main.go:177`).
5. **Engine** (`internal/engine/engine.go`): for each focused slot it picks one backend (`backendFor`, line 175):
   - a YAML **provider** (a shell script) if the spec defines one;
   - **cli**: runs the command's own completion through its CLI framework's protocol (`internal/complete/cli`: cobra's `cmd __complete …`, clap's `COMPLETE=fish cmd -- …`). The protocol is pinned by the plugin's `cli:` or detected from the marker zsh's `INFO` found in the completion function (e.g. kubectl);
   - otherwise **zsh**, i.e. the coprocess round-trip above.
   
   Results come from a disk cache first (stale-while-revalidate, 30s TTL), with a 2s timeout.
6. **TUI** (`internal/tui`): the slot editor on `/dev/tty`. It merges history and completion candidates, does fuzzy search, and applies plugin transforms (`internal/transform`, e.g. the Ctrl+A toggle).
   - **Selection**: the typed text is the value. Typing filters the list but only selects a candidate that matches exactly (`typedSel`). ↓/Ctrl-N picks a row, and ↑ from the top row goes back to the typed text. The `+ flag` slot selects the best match instead, since flag names are rarely typed in full. A row the user moved to stays selected as more candidates arrive.
   - **Linking** (`ActLink`, Ctrl+Q): `toggleLink` records the IDs of the other slots whose value equals the focused slot's in `linked`. The preview shows the pending value in them too, styled with the `link` role, and `pending` applies it to them on its copy. `commit` writes the value to the focused and linked slots with one undo entry and one `settle`, then ends the link. Keys that only pick or filter (↑/↓, clear, source) keep it; `refocus` and every other key drop it without writing.
   - **Rendering**: `View` draws a box (`box`) with the preview, the search line, the list, status lines and the key legend. The preview is wrapped with `cellbuf.Wrap`, not `ansi.Wrap`, which ends styles at each line break and restarts them on the next line. A long focused value therefore stays highlighted on every line, and its color doesn't run into the border.
   - **Height**: bubbletea's inline renderer drops lines from the top of a view taller than the terminal. `fit` prevents that by shrinking the list to 3 rows, then dropping the legend, then shrinking the list to 1 row, then clipping the preview. `window` keeps the preview lines ending at the focused slot and marks the hidden ones with `…`. The borders, search line and status lines are always shown. If the terminal reports no height, nothing is limited.
   - **Rules** (`internal/rule`): a plugin's `rules` are conditional transformations, e.g. kubectl's "logs removes the resource". `rule.Init` records which rules hold when the editor opens, so those don't fire. After every edit (a committed value, a transform, an added or dropped slot) the TUI calls `rule.Settle`. It reverts active rules whose conditions no longer hold, newest first, and fires rules whose conditions have just started to hold. It repeats until nothing changes, at most 10 rounds. An active rule is judged on a copy of the command with its own changes reverted, so removing the slot it tested doesn't undo it. Firing records each slot it touched (`Activation`): the slot it added, the old slot for a changed value, or a removed slot with the IDs of its neighbours. Slots carry an `ID` so the record survives other slots moving. Reverting restores from that record, but leaves a slot alone if the user has changed it since: an edited value, a deleted slot, or a removed slot the user added back. `Command.Extract`/`Insert` keep positional indexes right as positionals are removed and restored. Every edit pushes the command and the rule state onto an undo stack (`ActUndo`).
     With `rules: live` (`Options.Rules`), `pending` also runs the rules on a copy of the command and rule state with the focused slot's pending value (the selected row or typed text) written in, and `previewFocus` and the notice render that copy. It is computed on demand, so it follows the query, the selection and arriving candidates without being refreshed. Committing applies the same edit to the real command, so the result matches the preview. `commit` only settles on commit, and `off` turns `settle` and `pending` off.
7. **Render and splice**: `Command.Render()`, then `line.Splice(seg, …)` puts the edited segment back into the full buffer. The cursor position is converted back to characters and sent as `DONE`.

## Summary

```
.zshrc ──eval──> rx init zsh (Go) ──prints──> rx.zsh (embedded)
^X^X ──> rx-edit (zsh) ──coproc──> rx edit (Go: parse → classify → TUI on /dev/tty)
                    ^                    │
                    │   INFO / COMPLETE  │  (Go asks; zsh runs _main_complete with compadd hooked)
                    └──── C/M/I ... END ─┘
                    <── DONE / CANCEL / ERROR ── (zsh writes $BUFFER)
```

The other subcommands (`index`, `complete`, `rules`, `plugins`, `doctor`) are pure Go debugging tools. Without `RX_COPROC` the engine has no zsh client, so only provider completions and `cli` for plugins that pin a protocol work there. `rx-doctor` is a zsh wrapper that runs `rx doctor` and then adds the key-binding state, which only the shell can see.

The zsh side is tested by `internal/shell/zsh/zsh_test.go`. It uses `testdata/driver.zsh` and a `fake-rx` script that speaks the protocol.
- Each test's rc file writes the shell's pid to `<dir>/pid`. After `zpty -d`, which hangs up the shell without waiting, `finish` in the driver waits for that pid to exit. Otherwise the shell can still be writing to the test directory, e.g. bash saving its history on SIGHUP, while `t.TempDir` removes it.
- The end-to-end tests build `rx` themselves, so Go's test cache doesn't see that they depend on `internal/tui`. Run them with `-count=1` after TUI changes.

## bash

`rx init bash` prints `internal/shell/bash/rx.bash`. It speaks the same protocol over the same fifos. The bash-specific parts are:

- **Key binding**: `bind -x` can set `READLINE_LINE`/`READLINE_POINT` but can't accept the line. The user's key is therefore a readline macro that types two internal sequences. `\C-x\C-^e` runs `rx-edit`. `\C-x\C-^a` is rebound by `rx-edit` on every use, to `accept-line` after a DONE with `run` (Ctrl-E) and to `redraw-current-line` otherwise.
- **Completion capture** (`_rx_complete`): bash has no hookable completion widget, so the widget plays readline's part itself:
  - It splits the line into `COMP_WORDS` using `COMP_WORDBREAKS`.
  - It looks up `complete -p`. If there is no spec, it uses bash-completion's on-demand loader (`_comp_load`/`__load_completion`), then the `-D` default spec, which may return 124 to retry.
  - It sets the `COMP_*` variables, runs the `-F` function and the `-C` command, and passes the other options to `compgen`.
  - It applies the `-o default`/`dirnames`/`plusdirs` fallbacks. In command position it uses `compgen -c`.
  - `COMP_TYPE` is 37 (menu-complete) so cobra returns bare values. Bash completion has no descriptions or messages.
- **INFO markers**: framework markers live in helpers (cobra's `__start_X` calls `__X_get_completion_results`). So `_rx_marker` searches the completion function and every function reachable from it.
- **Process and terminal**:
  - `set +m` keeps rx in the shell's process group, and SIGPIPE is ignored while the widget runs. Both are restored afterwards.
  - The widget `wait`s for rx so the terminal is restored before readline redraws.
  - bash ≥ 5.1 clears the prompt line before a `bind -x` command and redraws it afterwards. Messages therefore show up above the prompt.
- **Cursor**: `READLINE_POINT` counts characters since bash 5.0. On bash 4.x the widget converts to and from bytes.
- **History**: the widget runs `history -a` first, because bash only writes `$HISTFILE` on exit. `history.ReadBash` reads plain files and `HISTTIMEFORMAT` files (`#<time>` lines, multi-line entries).

`internal/shell/bash/bash_test.go` drives an interactive bash through the same `driver.zsh`, now in `internal/shell/testdata/`.

## fish

`rx init fish | source` loads `internal/shell/fish/rx.fish`. It speaks the same protocol, but the transport differs:

- **Transport**: fish can't keep descriptors open (there is no `exec {fd}>file`), so the zsh/bash loop doesn't port. `rx-edit` runs one pipeline: `rx edit … <fifo | _rx_serve >fifo`. fish runs functions in pipelines inside the shell process, so `_rx_serve` can use `commandline`, `complete -C` and the session's state. rx is a foreground job and owns the terminal, so no job control tricks are needed. rx is started through `/bin/sh -c 'exec "$@" <fifo'`, because fish waits for a child to start before going on, and a child that opens the fifo before exec would block until fish opens the other end. That would deadlock.
- **Completion capture**: `complete --do-complete=LINE` gives exactly what Tab would, with descriptions (`value<TAB>description`).
- **INFO markers**: listing with `complete -c` doesn't autoload. `_rx_loadcomp` sources the command's completion file from `$fish_complete_path`, or fish 4's built-in copy via `status get-file`, but only when the command exists, because fish reports a missing command on the terminal. `_rx_marker` then searches `complete -c cmd` and every function reachable from it. cobra's `__complete` sits in a helper, and clap's `COMPLETE=fish` sits in the `-a` script. Because markers differ per shell, `cli.Protocol` has `Markers()`.
- **Key binding**: `bind -M default|insert`. DONE with `run` calls `commandline -f execute` directly. Caret notation is converted to raw bytes, which fish 3 and 4 both bind.
- **Aliases**: functions that `alias` created, recognised by their `alias name=value` description. Abbreviations are expanded before rx sees the line.
- **History**: `history.ReadFish` reads fish's YAML-like `- cmd:`/`  when:` file. The widget runs `history save` and exports the file named by `$fish_history`.

`internal/shell/fish/fish_test.go` uses the same driver. fish 4 waits for an answer to its Primary Device Attributes query at startup, which the driver sends. `--no-config` also turns on private mode, so the test config turns it off again.
