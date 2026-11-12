# rx fish integration. Load with:  rx init fish | source
#
# The rx-edit binding runs `rx edit` as a foreground job piped into
# _rx_serve: rx draws its editor on /dev/tty, sends requests on its stdout
# and reads the answers from a fifo. _rx_serve runs inside the shell (fish
# runs functions in pipelines in-process), so it answers completion requests
# with the live shell's `complete -C`, as <Tab> would. See
# internal/shell/proto for the protocol.
#
# Needs fish 3.4 or newer.

set -q _rx_bin; or set -g _rx_bin rx

# Escape for the line protocol: \ -> \\, newline -> \n, tab -> \t. Prints
# exactly one line.
function _rx_esc
    string join '\n' -- (string split \n -- $argv[1] | string replace -a -- \\ \\\\ | string replace -a -- \t '\t')
end

# Reverse of _rx_esc. Only \\, \n and \t are escapes; any other escaped
# character stands for itself, matching proto.Unescape. Prints the value
# without a newline: use (_rx_unesc $s | string collect -N -a).
function _rx_unesc
    set -l out
    for t in (string match -ar -- '\\\\.?|[^\\\\]+' $argv[1])
        if test (string sub -l 1 -- $t) = \\
            switch (string sub -s 2 -- $t)
                case n
                    set -a out \n
                case t
                    set -a out \t
                case ''
                    set -a out \\
                case '*'
                    set -a out (string sub -s 2 -- $t)
            end
        else
            set -a out $t
        end
    end
    printf %s $out
end

# Sends response lines to rx in one write. If rx has exited the write just
# fails; the read loop then sees EOF.
function _rx_reply
    printf '%s\n' $argv 2>/dev/null
end

# _rx_aliasof NAME: the expansion of an alias made with `alias`, which
# defines a function described as "alias NAME=VALUE" (or "alias NAME VALUE").
function _rx_aliasof
    functions -q -- $argv[1]; or return 1
    set -l d (functions -Dv -- $argv[1])[5]
    string match -q 'alias *' -- $d; or return 1
    string replace -r -- '^alias '(string escape --style=regex -- $argv[1])'[= ]' '' $d
end

# _rx_loadcomp CMD: loads CMD's completions if fish hasn't yet, like <Tab>
# does, and prints where they come from (the file, or "complete" when
# defined in the session). Listing with `complete -c` doesn't autoload.
function _rx_loadcomp
    set -l c (string replace -r -- '.*/' '' $argv[1])
    test -n "$c"; or return 1
    if not complete -c $c | string length -q
        # Completion files often run the command; fish reports a missing
        # one on the terminal, past any redirection.
        command -sq -- $argv[1]; or functions -q -- $c; or return 1
        set -l src
        for d in $fish_complete_path
            if test -f $d/$c.fish
                set src $d/$c.fish
                begin
                    source $src </dev/null
                end >/dev/null 2>&1
                break
            end
        end
        # fish 4 may have its completions built into the binary.
        if test -z "$src"
            set -l body (status get-file completions/$c.fish 2>/dev/null)
            or return 1
            set src completions/$c.fish
            begin
                string join \n -- $body | source
            end >/dev/null 2>&1
        end
        complete -c $c | string length -q; or return 1
        echo $src
        return
    end
    echo complete
end

# _rx_marker CMD MARKER...: the first marker found in CMD's completions or
# the functions they call, directly or not. CLI frameworks put the protocol
# call in a helper (cobra's __kubectl_perform_completion) or in the
# arguments script (clap's COMPLETE=fish).
function _rx_marker
    set -l text (complete -c (string replace -r -- '.*/' '' $argv[1]))
    set -l all $text
    set -l seen
    set -l queue
    set -l i 1
    while true
        for w in (string match -ar -- '[A-Za-z0-9_:.-]+' $text)
            contains -- $w $seen; and continue
            set -a seen $w
            functions -q -- $w; and set -a queue $w
        end
        set -q queue[$i]; or break
        test $i -gt 200; and break
        set text (functions -- $queue[$i] 2>/dev/null)
        set -a all $text
        set i (math $i + 1)
    end
    for m in $argv[2..-1]
        test -n "$m"; or continue
        if string match -qr -- (string escape --style=regex -- $m) $all
            echo $m
            return
        end
    end
end

# INFO <word> [marker...]: alias expansion, where the completions come from
# and the first marker found in them. Markers identify completions generated
# by CLI frameworks rx can call directly (e.g. `cmd __complete` for cobra).
function _rx_info
    set -l exp (_rx_aliasof $argv[1])
    set -l cmd (string split -n ' ' -- "$exp")[1]
    set -q cmd[1]; or set cmd $argv[1]
    set -l src (_rx_loadcomp $cmd)
    set -l match
    test -n "$src"; and set match (_rx_marker $cmd $argv[2..-1])
    _rx_reply I\t(_rx_esc "$exp")\t(_rx_esc "$src")\t(_rx_esc "$match") END
end

# COMPLETE <line>: complete at the end of line and send the matches with
# their descriptions.
function _rx_complete
    set -l out
    for c in (complete --do-complete=$argv[1] </dev/null 2>/dev/null)
        set -l p (string split -m 1 \t -- $c)
        set -a out C\t(_rx_esc $p[1])\t(_rx_esc "$p[2]")\t
    end
    _rx_reply $out END
end

# The protocol loop. stdin is rx's stdout, stdout the fifo rx reads. The
# outcome is left in _rx_done, _rx_ended, _rx_run, _rx_line, _rx_cursor
# and _rx_err.
function _rx_serve
    # Both ends of the fifo are open once this runs; open descriptors keep
    # working after the name is gone.
    rm -rf -- $_rx_dir
    while read -l req
        set -l f (string split \t -- $req)
        switch $f[1]
            case INFO
                set -l args
                for a in $f[2..-1]
                    set -a args (_rx_unesc $a | string collect -N -a)
                end
                _rx_info $args
            case COMPLETE
                _rx_complete (_rx_unesc $f[2] | string collect -N -a)
            case DONE
                set -g _rx_line (_rx_unesc $f[3] | string collect -N -a)
                set -g _rx_cursor $f[2]
                test "$f[4]" = run; and set -g _rx_run 1
                set -g _rx_done 1
                set -g _rx_ended 1
                return
            case CANCEL
                set -g _rx_ended 1
                return
            case ERROR
                set -g _rx_err (_rx_unesc $f[2] | string collect -N -a)
                set -g _rx_ended 1
                return
        end
    end
end

function _rx_logfile
    if set -q RX_LOG[1]; and test -n "$RX_LOG"
        echo $RX_LOG
    else if set -q _rx_log[1]; and test -n "$_rx_log"
        echo $_rx_log
    else if set -q XDG_CACHE_HOME[1]; and test -n "$XDG_CACHE_HOME"
        echo $XDG_CACHE_HOME/rx/rx.log
    else
        echo $HOME/.cache/rx/rx.log
    end
end

# Shows a message below the prompt.
function _rx_msg
    printf '\nrx: %s\n' $argv[1] >&2
    commandline -f repaint
end

function rx-edit
    set -l line (commandline | string collect -a)
    if not string match -qr -- '\S' $line
        _rx_msg "nothing to edit"
        return 0
    end
    if not command -sq -- $_rx_bin
        _rx_msg "binary not found: $_rx_bin"
        return 1
    end
    set -l log (_rx_logfile)
    set -l logdir (string replace -r -- '/[^/]*$' '' $log)
    test -d "$logdir"; or mkdir -p -- $logdir 2>/dev/null

    # Aliases made with `alias`. Abbreviations are expanded in the line and
    # in history before rx sees them.
    set -l al
    for fn in (functions -n)
        set -l v (_rx_aliasof $fn); or continue
        set -a al (_rx_esc "$fn=$v")
    end
    set -lx RX_ALIASES (string join \n -- $al | string collect -a)
    # fish_history names the session's history; empty turns history off.
    set -l hist fish
    set -q fish_history; and set hist $fish_history
    set -l data $HOME/.local/share
    set -q XDG_DATA_HOME[1]; and test -n "$XDG_DATA_HOME"; and set data $XDG_DATA_HOME
    set -lx RX_HISTFILE /dev/null
    if test -n "$hist"
        history save 2>/dev/null
        set RX_HISTFILE $data/fish/$hist"_history"
    end

    set -g _rx_done 0
    set -g _rx_ended 0
    set -g _rx_run 0
    set -g _rx_err
    set -g _rx_line
    set -g _rx_cursor
    set -l base /tmp
    set -q TMPDIR[1]; and test -n "$TMPDIR"; and set base $TMPDIR
    set -q XDG_RUNTIME_DIR[1]; and test -n "$XDG_RUNTIME_DIR"; and set base $XDG_RUNTIME_DIR
    set -g _rx_dir (mktemp -d $base/rx.XXXXXX 2>/dev/null)
    if test -z "$_rx_dir"
        set _rx_err "cannot create a temporary directory"
        set _rx_ended 1
    else if not mkfifo -m 600 $_rx_dir/req 2>/dev/null
        set _rx_err "mkfifo failed in $base"
        set _rx_ended 1
        rm -rf -- $_rx_dir
    else
        # rx is a foreground job, so it owns the terminal while its editor
        # runs. sh opens the fifo after it has started: fish may wait for a
        # child to start before going on, and opening a fifo blocks until
        # the other end is opened too.
        /bin/sh -c 'f=$1; shift; exec "$@" <"$f"' rx $_rx_dir/req \
            $_rx_bin edit --shell fish --cursor (commandline -C) -- $line 2>>$log | _rx_serve >$_rx_dir/req
        rm -rf -- $_rx_dir
    end
    set -e _rx_dir

    if test $_rx_done = 1
        commandline -r -- $_rx_line
        commandline -C -- $_rx_cursor
    end
    test $_rx_ended = 1; or set _rx_err "exited unexpectedly (see $log)"
    if test -n "$_rx_err"
        _rx_msg $_rx_err
    else
        commandline -f repaint
    end
    # Run the command as if Enter was pressed, so it lands in history and
    # the usual events fire.
    test $_rx_run = 1; and commandline -f execute
    return 0
end

# _rx_keyseq KEY: KEY as bind takes it. Caret notation as used by the
# config file and zsh (^X^X, ^[e) and fish escapes (\cx\cx, \ee) become the
# raw sequence, which fish 3 and 4 both bind; fish 4 key names
# (ctrl-x,ctrl-x) are kept.
function _rx_keyseq
    set -l k $argv[1]
    if string match -qr -- '^[a-z]+-|,' $k
        echo $k
        return
    end
    string match -q -- '*\\*' $k
    or set k (string replace -a -- '^[' '\e' $k | string replace -a -- '^?' '\x7f' | string replace -ar -- '\^(.)' '\\\\c$1' | string lower)
    string unescape -- $k
end

# rx-bind [key]: bind rx-edit in the default and insert modes, which cover
# emacs and vi. Takes caret notation (^X^X), fish escapes (\cx\cx) or fish 4
# key names (ctrl-x,ctrl-x).
function rx-bind
    set -l key '^X^X'
    if test -n "$argv[1]"
        set key $argv[1]
    else if test -n "$RX_KEY"
        set key $RX_KEY
    else if test -n "$_rx_key"
        set key $_rx_key
    end
    set -l seq (_rx_keyseq $key)
    for m in default insert
        bind -M $m $seq rx-edit
    end
end

# rx-doctor: rx's own checks plus the shell-side state rx can't see.
function rx-doctor
    $_rx_bin doctor --shell fish
    echo widget:
    printf '  %-12s %s\n' binary $_rx_bin 'key bindings' "$fish_key_bindings"
    for m in default insert
        set -l keys (bind -M $m | string match -r -- '^bind (?:-M \S+ )?(.*) rx-edit$' | string match -v -- 'bind *')
        set -q keys[1]; or set keys '(not bound)'
        printf '  %-12s %s\n' $m "$keys"
    end
    printf '  %-12s %s\n' log (_rx_logfile)
end

set -l _rx_v (string match -r -- '^(\d+)\.(\d+)' $version)
if test "$_rx_v[2]" -lt 3; or test "$_rx_v[2]" -eq 3 -a "$_rx_v[3]" -lt 4
    printf 'rx: fish %s is too old, rx needs 3.4 or newer\n' $version >&2
else if status is-interactive
    rx-bind
end
