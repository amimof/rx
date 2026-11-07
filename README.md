# rx

**Edit shell commands by their parts, not by moving the cursor around.**

> ⚠️ rx is an early MVP. Things will change, break and get renamed. Feedback and ideas are very welcome.

You run the same commands over and over with small differences:

```sh
k get pods -n kube-system
k get pods -n monitoring
k get pods -l app=nginx -n production
```

History search gets you close, but then you still have to edit the namespace, the pod name or a flag by hand. rx splits the command in your prompt into **slots** (arguments and flags) and lets you change each one from a list of suggestions.

## How it works

1. Type or recall a command, e.g. `k get pods -n monitoring`.
2. Press **Alt+e**. A small editor opens below your prompt.
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
| `Enter` | Put the command back in the prompt |
| `Ctrl+D` | Remove the current slot |
| `Esc` | Clear the search, press again to cancel |
| `Alt+<key>` | Plugin shortcuts, e.g. `Alt+A` toggles `--all-namespaces` for kubectl |

The last slot, **+ flag**, lets you add a new flag.

To use a different key than Alt+e, set `RX_KEY` before the `eval` line, or call `rx-bind` after it:

```zsh
RX_KEY='^[r'     # Alt+r
eval "$(rx init zsh)"
```

## Plugins

Plugins are optional YAML files that describe a command: which flags take values, which flags cancel each other out, and one-key shortcuts. rx ships with a [kubectl plugin](plugins/kubectl.yaml). You can add your own in `~/.config/rx/plugins/`.

```yaml
name: kubectl
match: { command: kubectl, aliases: [k] }
flags:
  - { name: namespace, short: n, takes: value, mutex: [all-namespaces] }
  - { name: all-namespaces, short: A, takes: none, mutex: [namespace] }
  - { name: output, short: o, takes: value, values: [wide, yaml, json, name] }
transforms:
  - { key: A, op: toggle, flag: all-namespaces, desc: "all namespaces" }
  - { key: o, op: cycle, flag: output, desc: "cycle output" }
```

A `cycle` transform steps through the transform's `values`, or else the flag's or positional's `values`. If neither has any, rx cycles through the candidates the editor would list for that slot: history first, then completions. The slot gets focus while they load.

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

By default rx asks your shell for completions, or calls the command's own completion directly when it is built with cobra (like kubectl). Set `complete:` to `shell`, `cobra` or `none` to choose explicitly.

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
