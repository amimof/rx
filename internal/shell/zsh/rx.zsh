# rx zsh integration. Load with:  eval "$(rx init zsh)"
#
# The rx-edit widget runs `rx edit` in the background with its stdin and
# stdout on a pair of fifos. rx draws its editor on /dev/tty and sends
# requests over the fifos; this widget answers completion requests with the
# live shell's completion system, so results match what <Tab> would give,
# with the same environment, aliases and zstyles. See internal/shell/proto
# for the protocol.

zmodload zsh/zutil 2>/dev/null

typeset -g _rx_bin=${_rx_bin:-rx}
typeset -ga _rx_hits _rx_descs _rx_tags _rx_msgs
# Write end of the request fifo while rx-edit runs.
typeset -g _rx_w=

# Escape for the line protocol: \ -> \\, newline -> \n, tab -> \t.
_rx_esc() {
  local s=${1//\\/\\\\}
  s=${s//$'\n'/\\n}
  REPLY=${s//$'\t'/\\t}
}

# Reverse of _rx_esc. Only \\, \n and \t are escapes; any other escaped
# character stands for itself, matching proto.Unescape.
_rx_unesc() {
  local s=$1 out= c
  while [[ $s == *\\* ]]; do
    out+=${s%%\\*}
    s=${s#*\\}
    c=${s[1]}
    s=${s:1}
    case $c in
      (n) out+=$'\n' ;;
      (t) out+=$'\t' ;;
      (*) out+=$c ;;
    esac
  done
  REPLY=$out$s
}

# compadd replacement used while capturing. Calls that only query matches
# (-O, -A, -D) are passed through untouched; everything else is recorded
# instead of being shown.
_rx_compadd() {
  local -a __apre __hpre __dscr __oad __mesg __opts
  zparseopts -a __opts P:=__apre p:=__hpre d:=__dscr O:=__oad A:=__oad D:=__oad x:=__mesg \
    X+: f i: S: s: I: r: R: W: F: M+: E: q e Q n U C J: V: a l k o:: 1 2 2>/dev/null
  if (( $#__oad )); then
    builtin compadd "$@"
    return
  fi
  if (( $#__mesg )); then
    _rx_msgs+=("${__mesg[2]:-${__mesg[1]#-x}}")
  fi
  local -a __hits __disp
  if (( $#__dscr )); then
    __disp=("${(@P)${__dscr[2]:-${__dscr[1]#-d}}}")
  fi
  builtin compadd -A __hits -D __disp "$@"
  local pre="${__hpre[2]:-${__hpre[1]#-p}}${__apre[2]:-${__apre[1]#-P}}" i
  for (( i = 1; i <= $#__hits; i++ )); do
    _rx_hits+=("$pre${__hits[i]}")
    _rx_descs+=("${__disp[i]}")
    _rx_tags+=("$curtag")
  done
  # Register the matches too so completion functions that check the
  # return status or compstate[nmatches] behave as usual.
  builtin compadd "$@"
}

# _message adds its text with `builtin compadd -x`, bypassing the wrapper
# above, so plain messages are recorded here. `_message -e` goes through
# compadd and is handled by _rx_compadd.
_rx_message() {
  local -a args=("$@") gopt
  zparseopts -D -a gopt 1 2 V J
  if [[ $1 == -e ]]; then
    _rx_orig_message "${args[@]}"
    return
  fi
  [[ $1 == -r ]] && shift
  [[ -n $1 ]] && _rx_msgs+=("$1")
  return 0
}

_rx_capture_main() {
  if (( ! $+functions[_rx_orig_message] )); then
    autoload +X _message 2>/dev/null
    functions -c _message _rx_orig_message
  fi
  {
    compadd() { _rx_compadd "$@" }
    functions -c _rx_message _message
    _main_complete
  } always {
    unfunction compadd 2>/dev/null
    functions -c _rx_orig_message _message
  }
  # Never insert or list anything.
  compstate[insert]=''
  compstate[list]=''
  compstate[old_list]=''
}
zle -C _rx_capture complete-word _rx_capture_main

# The completion context while capturing is :completion:_rx_capture:..., so
# these override the user's styles only for rx: no "no matches" warnings and
# no listing prompts.
zstyle ':completion:_rx_capture:*:warnings' format ''
zstyle ':completion:_rx_capture:*' list-prompt ''
zstyle ':completion:_rx_capture:*' select-prompt ''
zstyle ':completion:_rx_capture:*' menu no

# INFO <word>: alias expansion, completion function and whether that
# function is cobra-generated (so rx may call `cmd __complete` directly).
_rx_info() {
  local word=$1 exp=${aliases[$1]}
  # Split into an array first: subscripting a one-word (z) result directly
  # would index characters.
  local -a ws=(${(z)exp})
  local cmd=${ws[1]:-$word}
  local fn=${_comps[$cmd]} cobra=0
  if [[ -n $fn ]]; then
    [[ ${functions[$fn]} == *'builtin autoload -X'* ]] && autoload +X $fn 2>/dev/null
    [[ ${functions[$fn]} == *__complete* ]] && cobra=1
  fi
  _rx_esc "$exp"
  _rx_reply "I"$'\t'"$REPLY"$'\t'"$fn"$'\t'"$cobra" END
}

# Sends response lines to rx in one write. If rx has exited, the error is
# swallowed rather than printed over the prompt; the read loop then sees EOF.
_rx_reply() {
  print -r -u $_rx_w -l -- "$@" 2>/dev/null
}

# COMPLETE <line>: complete at the end of line and send the matches.
_rx_complete() {
  _rx_hits=() _rx_descs=() _rx_tags=() _rx_msgs=()
  BUFFER=$1
  CURSOR=$#BUFFER
  zle _rx_capture 2>/dev/null
  local i v d
  local -a out
  for (( i = 1; i <= $#_rx_hits; i++ )); do
    _rx_esc "$_rx_hits[i]"; v=$REPLY
    _rx_esc "$_rx_descs[i]"; d=$REPLY
    out+=("C"$'\t'"$v"$'\t'"$d"$'\t'"$_rx_tags[i]")
  done
  for v in "${_rx_msgs[@]}"; do
    _rx_esc "$v"
    out+=("M"$'\t'"$REPLY")
  done
  _rx_reply "${out[@]}" END
}

rx-edit() {
  # nomonitor keeps rx in the shell's process group so it may read the
  # terminal.
  setopt localoptions nomonitor nonotify noshwordsplit
  if [[ -z ${BUFFER//[[:space:]]/} ]]; then
    zle -M "rx: nothing to edit"
    return 0
  fi
  if ! whence -p -- $_rx_bin >/dev/null; then
    zle -M "rx: binary not found: $_rx_bin"
    return 1
  fi
  local log=${RX_LOG:-${XDG_CACHE_HOME:-$HOME/.cache}/rx/rx.log}
  [[ -d ${log:h} ]] || mkdir -p ${log:h} 2>/dev/null
  local orig_buf=$BUFFER orig_cur=$CURSOR
  local -a al
  local k
  for k in ${(k)aliases}; do
    _rx_esc "$k=${aliases[$k]}"
    al+=("$REPLY")
  done
  local -x RX_ALIASES=${(pj:\n:)al}
  local -x RX_HISTFILE=${HISTFILE:-$HOME/.zsh_history}

  local req done=0 ended=0 err= d= r=
  local -a f
  _rx_w=
  zle -I
  {
    if ! d=$(mktemp -d "${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/rx.XXXXXX" 2>/dev/null); then
      d= err="cannot create a temporary directory" ended=1
    elif ! mkfifo -m 600 $d/in $d/out 2>/dev/null; then
      err="mkfifo failed in ${d:h}" ended=1
    else
      # &! disowns rx, so no job notice appears at the next prompt. Opening
      # a fifo blocks until the other end is opened too: the order here
      # matches rx's redirections, and neither side opens one read-write,
      # so rx exiting is seen as EOF below.
      "$_rx_bin" edit --shell zsh --cursor "$CURSOR" -- "$BUFFER" <$d/in >$d/out 2>>$log &!
      exec {_rx_w}>$d/in {r}<$d/out
      # Open descriptors keep working after the names are gone.
      rm -rf -- $d

      while IFS= read -r -u $r req; do
        f=("${(@ps:\t:)req}")
        case $f[1] in
          (INFO)     _rx_unesc "$f[2]"; _rx_info "$REPLY" ;;
          (COMPLETE) _rx_unesc "$f[2]"; _rx_complete "$REPLY" ;;
          (DONE)
            _rx_unesc "$f[3]"
            BUFFER=$REPLY
            CURSOR=$f[2]
            done=1 ended=1
            break ;;
          (CANCEL) ended=1; break ;;
          (ERROR)  _rx_unesc "$f[2]"; err=$REPLY; ended=1; break ;;
        esac
      done
    fi
  } always {
    [[ -n $_rx_w ]] && exec {_rx_w}>&-
    [[ -n $r ]] && exec {r}<&-
    _rx_w=
    [[ -n $d && -d $d ]] && rm -rf -- $d
  }
  if (( ! done )); then
    BUFFER=$orig_buf
    CURSOR=$orig_cur
  fi
  (( ended )) || err="exited unexpectedly (see $log)"
  zle reset-prompt
  [[ -n $err ]] && zle -M "rx: $err"
  return 0
}
zle -N rx-edit

# rx-bind [key]: bind rx-edit in the emacs, viins and vicmd keymaps. Call it
# again after plugins that reset keymaps (e.g. zsh-vi-mode) have loaded.
rx-bind() {
  local key=${1:-${RX_KEY:-^X^X}} km
  for km in emacs viins vicmd; do
    bindkey -M $km "$key" rx-edit
  done
}

# rx-doctor: rx's own checks plus the shell-side state rx can't see.
rx-doctor() {
  "$_rx_bin" doctor --shell zsh
  print -r -- "widget:"
  print -r -- "  binary       $_rx_bin"
  print -r -- "  main keymap  ${${(z)$(bindkey -lL main)}[3]}"
  local km b
  for km in emacs viins vicmd; do
    b=$(bindkey -M $km | grep -F ' rx-edit')
    print -r -- "  $km${(l:13-$#km:: :)}${b:-(not bound)}"
  done
  print -r -- "  log          ${RX_LOG:-${XDG_CACHE_HOME:-$HOME/.cache}/rx/rx.log}"
}

rx-bind
