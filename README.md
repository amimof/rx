# rx

**Edit shell commands by their parts, not by moving the cursor around.**

<p align="center">
  <img src="./demo/demo.gif"/>
</p>

> ⚠️ rx is an early MVP. Things will change, break and get renamed. Feedback and ideas are very welcome.

You run the same commands over and over with small differences:

```sh
kubectl describe pod my-app-xxx -n production
git tag -a "v0.39.2" -m "v0.39.2"
docker logs -f --tail=100 my-container
ssh -i ~/.ssh/id_ed25519 user@server01
curl -H "Authorization: Bearer $TOKEN" https://api.example.com
find /var/log -name "*.log" -mtime -1
tcpdump -i eth0 host 10.0.0.1 -nn
```

History search gets you close, but then you still have to edit the namespace, the pod name or a flag by hand. rx splits the command in your prompt into **slots** (arguments and flags) and lets you change each one from a list of suggestions.

## How it works

1. Type or recall a command, e.g. `find /var/log -name "*.log" -mtime -1`.
2. Press **Ctrl-X Ctrl-X**. A small editor opens below your prompt.
3. Press **Tab** to move between slots and type to fuzzy-search values.
4. Press **Enter**. The edited command is put back in your prompt. Press Enter again to run it.

Suggestions come from:

- **Your shell history**: values you've used before, most frequent and recent first.
- **Shell completion**: the same values you'd get from pressing Tab in zsh, such as namespaces, git branches or ssh hosts.
- **Plugins**: small YAML files that add shortcuts and extra knowledge about a command.

## Install

rx currently supports **zsh** only (`rx init zsh`). You need Go to build it.

```sh
go install github.com/amimof/rx/cmd/rx@latest
```

Then add this line to your `~/.zshrc` and open a new shell:

```zsh
eval "$(rx init zsh)"
```

Check that everything is set up:

```zsh
rx-doctor
```

## Keys

| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | Next / previous slot |
| Type | Fuzzy-search values for the slot |
| `↑` `↓` | Pick a value |
| `Ctrl+N` / `Ctrl+P` | Same as `↓` / `↑` |
| `Enter` | Put the command back in the prompt |
| `Ctrl+E` | Put the command back in the prompt and run it |
| `Ctrl+S` | Cycle the suggestion source: all, history, spec, provider, cobra, shell |
| `Ctrl+D` | Remove the current slot |
| `Ctrl+U` / `Ctrl+W` | Clear the search |
| `Esc` | Clear the search, press again to cancel |
| `Ctrl+C` | Cancel |
| `Ctrl+<key>` / `Alt+<key>` | Plugin shortcuts, e.g. `Ctrl+A` toggles `--all-namespaces` for kubectl |

The last slot, **+ flag**, lets you add a new flag. All of these keys except plugin shortcuts can be changed in the [config file](#configuration).

## Configuration

rx reads `~/.config/rx/config.yaml` (or `$XDG_CONFIG_HOME/rx/config.yaml`, or the file given with `--config`). Every setting is optional:

```yaml
key: "^X^X"                # key that opens rx, in zsh bindkey syntax
log: ~/.cache/rx/rx.log    # widget log file
prompt: "❯"                # symbol before the command in the editor; "" for none
keys:                      # editor keys; a list replaces that action's defaults
  accept:    [enter]
  run:       [ctrl+e]       # accept, then run the command
  cancel:    [ctrl+c]
  back:      [esc]         # clear the search, press again to cancel
  up:        [up, ctrl+p]
  down:      [down, ctrl+n]
  next-slot: [tab]
  prev-slot: [shift+tab]
  source:    [ctrl+s]
  drop:      [ctrl+d]
  clear:     [ctrl+u, ctrl+w]
theme:                     # editor colors; a role replaces that role's default
  flag:   "#5f87ff"        # a string sets only the text color
  value:  { fg: 2, bold: true }
  sel:    { fg: "#ffaf00", bg: 236, bold: true }
  border: { fg: 8 }        # instead of faint
```

Keys are named like `ctrl+e`, `alt+y`, `shift+tab`, `enter`, `esc`, `up`. An empty list (`drop: []`) unbinds an action. Single characters and space can't be bound, since they type into the search. Run `rx doctor` to check the file.

Theme roles are `head` (the command word), `flag`, `value` (a flag's value), `arg` (positional arguments), `sel` (the selected candidate), `dim` (descriptions, status and the help line), `err`, `border`, `prompt` and `focus` (the `=` between a focused flag and its value). A role takes `fg` and `bg` colors, either an ANSI color number `0`–`255` or hex `#rgb`/`#rrggbb`, and `bold`, `italic`, `faint`, `underline` and `reverse`. Roles you don't list keep their default look. The focused slot is always shown reversed. Hex colors are approximated on terminals with fewer colors, and `NO_COLOR` turns colors off.

Environment variables override the file: `RX_KEY`, `RX_LOG`, `RX_PROMPT`, and `RX_KEYS_<ACTION>` with a comma-separated list, e.g. `RX_KEYS_NEXT_SLOT=tab,ctrl+f`. The theme can only be set in the file.

`key` and `log` are baked into the widget by `rx init zsh`, so changes take effect in new shells. To use a different key than Ctrl-X Ctrl-X without the config file, set `RX_KEY` before the `eval` line, or call `rx-bind` after it. The default replaces zsh's `exchange-point-and-mark` binding in emacs mode; pick another key if you use it.

```zsh
RX_KEY='^[r'     # Alt+r
eval "$(rx init zsh)"
```

## Plugins

Plugins are optional YAML files that describe a command: which flags take values, which flags cancel each other out, and one-key shortcuts. rx ships with plugins for [kubectl](plugins/kubectl.yaml), [git](plugins/git.yaml) and [curl](plugins/curl.yaml) (`Ctrl+X` cycles the HTTP method). You can add your own in `~/.config/rx/plugins/`.

```yaml
name: kubectl
match: { command: kubectl, aliases: [k] }
flags:
  - { name: namespace, short: n, takes: value, mutex: [all-namespaces] }
  - { name: all-namespaces, short: A, takes: none, mutex: [namespace] }
  - { name: output, short: o, takes: value, values: [wide, yaml, json, name] }
transforms:
  - { key: ctrl+a, op: toggle, flag: all-namespaces, desc: "all namespaces" }
  - { key: ctrl+o, op: cycle, flag: output, desc: "cycle output" }
```

A transform `key` is `ctrl+<letter>`, `alt+<char>`, or a bare character, which means `alt+<char>`. Plugin shortcuts take precedence over the editor's own keys, so avoid `ctrl+` keys you use in the editor (by default `ctrl+c`, `d`, `e`, `n`, `p`, `s`, `u` and `w`). `ctrl+i` and `ctrl+m` can't be used, since terminals send them as Tab and Enter.

A `cycle` transform steps through the transform's `values`, or else the flag's or positional's `values`. If neither has any, rx cycles through the candidates the editor would list for that slot: history first, then completions. Cycling focuses the slot and selects its new value in the list.

A `focus` transform jumps to a slot so you can search its candidates and replace the value, adding the slot if the command doesn't have it yet. In kubectl, `Ctrl+G` jumps to the resource name.

Use `focus:` to choose which slot the editor starts on. It takes a positional name, `pos:N`, or a flag (`namespace`, `--namespace`, `-n`), or a list of these. The first one present in the command wins. Positionals declared in the plugin can always be focused, even before they're typed. Flags that aren't in the command are skipped. Without a match, rx starts on the first flag that takes a value.

```yaml
name: flux
match: { command: flux }
positionals:
  - { index: 0, name: verb }
  - { index: 1, name: kind }
focus: kind          # `flux get kustomization` starts on "kustomization"
```

```yaml
name: find
match: { command: find }
focus: [-name, -type] # `find . -name "*.txt" -type d` starts on "*.txt"
```

By default rx asks your shell for completions, or calls the command's own completion directly when it is built with cobra (like kubectl). Set `complete:` to `shell`, `cobra` or `none` to choose explicitly. In the editor, `Ctrl+S` switches to a single source for the rest of the session. Only the sources a slot has are offered, and cobra only for commands rx knows are built with it.

Check your plugin with `rx plugins lint my-plugin.yaml`.

## Troubleshooting

- Run `rx-doctor` in zsh to see which keys are bound and whether rx can find your history.
- If something goes wrong, rx shows a message below the prompt. More detail is in `~/.cache/rx/rx.log`.

## Contributing

Issues and pull requests are welcome. Since this is an MVP, please open an issue before starting on larger changes.

```sh
go build -o bin/rx ./cmd/rx
go test ./...
```

The zsh integration tests need `zsh` installed and are skipped without it.

To add support for another shell, start with the widget contract in [`internal/shell/proto`](internal/shell/proto/proto.go) and the registry in [`internal/shell/shell.go`](internal/shell/shell.go). A new shell needs a widget script under `internal/shell/<name>/`, a history reader and a registry entry. Its widget tests can reuse `internal/shell/testdata/fake-rx`.
