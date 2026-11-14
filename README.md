# rx

[![CI](https://github.com/amimof/rx/actions/workflows/ci.yml/badge.svg)](https://github.com/amimof/rx/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/amimof/rx)](https://github.com/amimof/rx/releases/latest)

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
3. Press **Tab** to move between slots. Type a value, or pick one of the suggestions with **↓**.
4. Press **Enter**. The edited command is put back in your prompt. Press Enter again to run it.

Suggestions come from:

- **Your shell history**: values you've used before, most frequent and recent first.
- **Shell completion**: the same values you'd get from pressing Tab in zsh, bash or fish, such as namespaces, git branches or ssh hosts.
- **Plugins**: small YAML files that add shortcuts and extra knowledge about a command.

## Install

rx supports **zsh**, **bash** 4.1 or newer and **fish** 3.4 or newer, on Linux and macOS.

Download the latest release for your platform from [GitHub Releases](https://github.com/amimof/rx/releases) and put `rx` on your `PATH`:

```sh
curl -fsSL "https://github.com/amimof/rx/releases/latest/download/rx_$(uname -s)_$(uname -m).tar.gz" | tar -xz rx
sudo install rx /usr/local/bin/
rx --version
```

Each release also has a `checksums.txt` to verify the download. To build from source instead, run `go install github.com/amimof/rx/cmd/rx@latest`.

Then add the line for your shell and open a new shell:

```sh
eval "$(rx init zsh)"     # ~/.zshrc
eval "$(rx init bash)"    # ~/.bashrc
rx init fish | source     # ~/.config/fish/config.fish
```

macOS ships bash 3.2 as `/bin/bash`, which is too old. Use a newer bash, e.g. from Homebrew (`brew install bash`). With bash, rx runs `history -a` before it opens, so it sees the commands of the current session. In bash this appends them to `$HISTFILE` earlier than bash would by itself.

Check that everything is set up:

```zsh
rx-doctor
```

## Keys

| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | Next / previous slot |
| Type | Type a value. Matching suggestions are listed, but only an exact match is picked, so what you typed is used. On `+ flag`, the best match is picked |
| `↑` `↓` | Pick a value, `↑` from the top goes back to what you typed |
| `Ctrl+N` / `Ctrl+P` | Same as `↓` / `↑` |
| `Enter` | Put the command back in the prompt |
| `Ctrl+E` | Put the command back in the prompt and run it |
| `Ctrl+S` | Cycle the suggestion source: all, then each [enabled source](#completion-sources) the slot has |
| `Ctrl+D` | Remove the current slot |
| `Ctrl+Z` | Undo the last edit, along with what [rules](#rules) did after it |
| `Ctrl+U` / `Ctrl+W` | Clear the search |
| `Esc` | Clear the search, press again to cancel |
| `Ctrl+C` | Cancel |
| `Ctrl+<key>` / `Alt+<key>` | Plugin shortcuts, e.g. `Ctrl+A` toggles `--all-namespaces` for kubectl |

The last slot, **+ flag**, lets you add a new flag. All of these keys except plugin shortcuts can be changed in the [config file](#configuration).

## Configuration

rx reads `~/.config/rx/config.yaml` (or `$XDG_CONFIG_HOME/rx/config.yaml`, or the file given with `--config`). Every setting is optional:

```yaml
key: "^X^X"                # key that opens rx, in caret notation
log: ~/.cache/rx/rx.log    # widget log file
prompt: "❯"                # symbol before the command in the editor; "" for none
complete: auto             # completion sources, or a list of them; plugins may replace it
rules: live                # when plugin rules run: live, commit or off
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
  undo:      [ctrl+z]
theme:                     # editor colors; a role replaces that role's default
  flag:   "#5f87ff"        # a string sets only the text color
  value:  { fg: 2, bold: true }
  sel:    { fg: "#ffaf00", bg: 236, bold: true }
  border: { fg: 8 }        # instead of faint
```

Keys are named like `ctrl+e`, `alt+y`, `shift+tab`, `enter`, `esc`, `up`. An empty list (`drop: []`) unbinds an action. Single characters and space can't be bound, since they type into the search. Run `rx doctor` to check the file.

Theme roles are `head` (the command word), `flag`, `value` (a flag's value), `arg` (positional arguments), `sel` (the selected candidate), `spinner` (shown while candidates load), `dim` (descriptions, status and the help line), `err`, `border`, `prompt` and `focus` (the `=` between a focused flag and its value). A role takes `fg` and `bg` colors, either an ANSI color number `0`–`255` or hex `#rgb`/`#rrggbb`, and `bold`, `italic`, `faint`, `underline` and `reverse`. Roles you don't list keep their default look. The focused slot is always shown reversed. Hex colors are approximated on terminals with fewer colors, and `NO_COLOR` turns colors off.

Environment variables override the file: `RX_KEY`, `RX_LOG`, `RX_PROMPT`, `RX_RULES`, and `RX_COMPLETE` and `RX_KEYS_<ACTION>` with a comma-separated list, e.g. `RX_COMPLETE=history,shell` or `RX_KEYS_NEXT_SLOT=tab,ctrl+f`. The theme can only be set in the file.

`key` and `log` are baked into the widget by `rx init`, so changes take effect in new shells. To use a different key than Ctrl-X Ctrl-X without the config file, set `RX_KEY` before the `eval` line, or call `rx-bind` after it. The key is written in caret notation (`^X^X`, `^[r` for Alt+r), which every shell accepts. bash also takes readline syntax (`\C-x\C-x`), and fish its own escapes (`\cx\cx`) and, from fish 4, key names (`ctrl-x,ctrl-x`). The default replaces the `exchange-point-and-mark` binding of zsh and bash in emacs mode; pick another key if you use it.

```sh
RX_KEY='^[r'     # Alt+r
eval "$(rx init zsh)"
```

In bash, rx is bound in the emacs, vi-insert and vi-command keymaps through a readline macro. That is how Ctrl+E can run the command as if Enter was pressed: it uses the internal sequences `\C-x\C-^e` and `\C-x\C-^a`.

In fish, rx is bound in the `default` and `insert` modes, which cover emacs and vi key bindings. fish binds a single Ctrl-X to copy to the clipboard. That still works with the default key, but fish waits for its key sequence timeout first (`fish_sequence_key_delay_ms`) in case a second Ctrl-X follows. rx loads aliases made with `alias`. Abbreviations need no support, since fish expands them before rx sees the command.

## Plugins

Plugins are optional YAML files that describe a command: which flags take values, which flags cancel each other out, one-key shortcuts, and [rules](#rules) that adjust the command as you edit it. rx ships with plugins for [kubectl](plugins/kubectl.yaml), [git](plugins/git.yaml) and [curl](plugins/curl.yaml) (`Ctrl+X` cycles the HTTP method). You can add your own in `~/.config/rx/plugins/`.

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

A transform `key` is `ctrl+<letter>`, `alt+<char>`, or a bare character, which means `alt+<char>`. Plugin shortcuts take precedence over the editor's own keys, so avoid `ctrl+` keys you use in the editor (by default `ctrl+c`, `d`, `e`, `n`, `p`, `s`, `u`, `w` and `z`). `ctrl+i` and `ctrl+m` can't be used, since terminals send them as Tab and Enter.

A `cycle` transform steps through the transform's `values`, or else the flag's or positional's `values`. If neither has any, rx cycles through the candidates the editor would list for that slot: history first, then completions. Cycling focuses the slot and selects its new value in the list.

A `focus` transform jumps to a slot so you can search its candidates and replace the value, adding the slot if the command doesn't have it yet. In kubectl, `Ctrl+G` jumps to the resource name.

Use `focus:` to choose which slot the editor starts on. It takes a positional name, `pos:N`, or a flag (`namespace`, `--namespace`, `-n`), or a list of these. The first one present in the command wins. Positionals declared in the plugin can always be focused, even before they're typed. Flags that aren't in the command are skipped. Without a match, rx starts on the first flag that takes a value.

```yaml
name: flux
match: { command: flux }
positionals:
  - { index: 0, name: subcommand }
  - { index: 1, name: kind }
focus: kind          # `flux get kustomization` starts on "kustomization"
```

```yaml
name: find
match: { command: find }
focus: [-name, -type] # `find . -name "*.txt" -type d` starts on "*.txt"
```

Check your plugin with `rx plugins lint my-plugin.yaml`.

### Rules

A rule is a conditional transformation: when an edit makes its conditions hold, it changes other slots to match. The kubectl plugin's rule turns `kubectl get pods my-pod -n default` into `kubectl logs my-pod -n default` when you change `get` to `logs`, because `logs` takes the pod name without the resource. The curl plugin's rule adds `-d '{}'` when the method becomes POST, PUT or PATCH. The editor shows what a rule did below the list.

The `rules` setting in the [config file](#configuration) decides when rules run:

| Value | Rules run |
|---|---|
| `live` (default) | as you type or move through the list. The preview and the rule notice show the result right away, e.g. typing `logs` shows `kubectl logs my-pod -n default`, and Backspace brings `pods` back. Nothing changes until Tab, Enter or Ctrl+E commits the value, and then the command is exactly what the preview showed. On `+ flag`, only a fully typed flag name (`--json`, `-d`) or one picked with ↑/↓ triggers rules. |
| `commit` | when a value is committed with Tab, Shift+Tab, Enter or Ctrl+E |
| `off` | never |

Plugin shortcuts and `Ctrl+D` change the command directly, so with `live` or `commit` they run rules at once.

Rules remember the command from before they changed it. When their conditions stop holding, they put it back: change `logs` back to `get` and `pods` returns in front of the pod name, written as you typed it. Changes you made yourself are kept. If you rename the pod, edit the body or add the resource back yourself, the rule leaves your version alone, and a slot you delete isn't added again. Rules that already hold when rx opens don't fire, so opening rx never changes the command. `Ctrl+Z` undoes an edit together with what the rules did after it.

```yaml
values:                    # spellings conditions treat as the same value
  resource:
    pod: [po, pods]
rules:
  - name: logs-removes-resource
    when:                  # one condition, or a list that must all hold
      - argument: { name: subcommand, equals: logs }
      - argument: { name: resource, equals: pod }   # also matches po and pods
      - argument: { name: name, exists: true }
    then:                  # one action, or a list
      - remove: { argument: resource }
      - remove: { flag: output }
  - name: post-adds-body   # in curl's plugin
    when:
      flag: { name: request, in: [POST, PUT, PATCH] }
    then:
      add: { flag: data, value: "{}", unless_present: true }
```

- **Conditions** test one slot: a positional by its name (`argument:`) or a flag by its long name (`flag:`), with exactly one check: `exists: true|false`, `equals`, `in` (any of a list) or `matches` (a regular expression). `not:` negates another condition. A flag given more than once matches if any of its values does.
- **Actions** change one slot each:
  - `add` adds a flag or argument. A flag is added again if it's already there, since flags like `-H` repeat; an argument that's already there gets the value. With `unless_present: true`, a slot that has a value is left alone. An argument added without a value becomes an empty slot that the editor focuses.
  - `remove` removes a flag or argument. The arguments after a removed one move up a position, so the pod name takes the resource's place.
  - `set` sets a value, adding the slot if it's missing.
- **Values** are [Go templates](https://pkg.go.dev/text/template) over the command's slots by name, as in providers (`{{.name}}`, `{{default "default" .namespace}}`).
- A rule is judged without its own changes, so removing the resource it checked doesn't undo it. Rules that keep undoing each other stop after 10 rounds.

Positionals are not yet scoped to subcommands. `kubectl logs my-pod` as typed has `my-pod` in the `resource` slot, which is why the rule needs a name. `rx rules -- 'k get pods my-pod'` prints each rule, whether it holds and the command it would produce.

### Completion sources

Candidates come from these sources:

| Source | Values |
|---|---|
| `history` | values used for the slot in your shell history |
| `spec` | the plugin's `values` |
| `provider` | the plugin's `providers` |
| `cli` | the command's own completion, for commands built with a supported CLI framework |
| `shell` | your shell's completion, as `Tab` would (in bash, without descriptions, which bash completion doesn't have) |

`complete:` picks the enabled sources, either one source or a list of them. `auto`, the default, enables all of them, and `none` enables none. Set it in the config file for every command, or in a plugin for that command. A plugin's `complete:` replaces the config file's.

```yaml
complete: [history, spec, provider, cli]   # kubectl: skip the shell
cli: cobra
```

The `cli` source runs the command's own completion through the protocol of the framework it is built with:

| Framework | Protocol |
|---|---|
| [cobra](https://github.com/spf13/cobra) (Go) | `cmd __complete <args>` |
| [clap](https://github.com/clap-rs/clap) (Rust) | `COMPLETE=fish cmd -- cmd <args>`, from `clap_complete::CompleteEnv` |

rx detects the framework from the command's completion function in zsh, bash or fish, so it never runs a command that may not support the protocol. A plugin can set `cli:` to `cobra` or `clap` to skip detection, which also makes `cli` work outside the shell widget, e.g. in `rx complete`. With `cli` enabled, rx uses the command's own completion when it knows the framework, and asks the shell otherwise. If the command turns out not to speak the protocol, rx falls back to the shell for the rest of the session.

Only clap programs that use `CompleteEnv` with its default `COMPLETE` variable (like [jj](https://www.jj-vcs.dev)) have a protocol rx can call. Many Rust tools ship static completion scripts instead, which `shell` keeps handling.

In the editor, `Ctrl+S` switches to a single source for the rest of the session. Only enabled sources the slot has are offered, and `cli` only for commands whose framework rx knows.

## Troubleshooting

- Run `rx-doctor` in your shell to see which keys are bound and whether rx can find your history.
- bash: the widget is built on readline's `bind -x`. Line editors that replace readline, such as ble.sh, are untested.
- fish: rx sees the commands of the current session only if fish writes history, which it doesn't in private mode (`fish --private`).
- If something goes wrong, rx shows a message below the prompt. More detail is in `~/.cache/rx/rx.log`.

## Contributing

Issues and pull requests are welcome. Since this is an MVP, please open an issue before starting on larger changes. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test and add support for another shell.
