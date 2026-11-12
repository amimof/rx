# rx bash integration. Load with:  eval "$(rx init bash)"
#
# The rx-edit widget runs `rx edit` in the background with its stdin and
# stdout on a pair of fifos. rx draws its editor on /dev/tty and sends
# requests over the fifos; this widget answers completion requests with the
# live shell's programmable completion (complete -F/-W/-C/...), so results
# match what <Tab> would give. See internal/shell/proto for the protocol.
#
# Needs bash 4.1 or newer (bind -x with READLINE_LINE, {fd} redirections).
# Variables in functions that run completion functions are prefixed with
# _rx_, since bash scoping is dynamic and completion functions may assign to
# names they did not declare.

_rx_bin=${_rx_bin:-rx}
# Write end of the request fifo while rx-edit runs.
_rx_w=

# Readline sequences used internally. The user's key is bound to a macro
# that types both: the first runs rx-edit, the second is rebound by rx-edit
# to accept-line when the command should run right away, since a bind -x
# command can't accept the line itself.
_rx_seq_edit='\C-x\C-^e'
_rx_seq_after='\C-x\C-^a'

# Escape for the line protocol: \ -> \\, newline -> \n, tab -> \t.
_rx_esc() {
  local bs='\' s
  s=${1//"$bs"/"$bs$bs"}
  s=${s//$'\n'/"${bs}n"}
  REPLY=${s//$'\t'/"${bs}t"}
}

# Reverse of _rx_esc. Only \\, \n and \t are escapes; any other escaped
# character stands for itself, matching proto.Unescape.
_rx_unesc() {
  local s=$1 out= c
  while [[ $s == *\\* ]]; do
    out+=${s%%\\*}
    s=${s#*\\}
    c=${s:0:1}
    s=${s:1}
    case $c in
      n) out+=$'\n' ;;
      t) out+=$'\t' ;;
      '') out+='\' ;;
      *) out+=$c ;;
    esac
  done
  REPLY=$out$s
}

# Splits $1 on tabs into _rx_f, keeping empty fields (read -a would merge
# them, since tab is IFS whitespace).
_rx_split() {
  local s=$1
  _rx_f=()
  while [[ $s == *$'\t'* ]]; do
    _rx_f+=("${s%%$'\t'*}")
    s=${s#*$'\t'}
  done
  _rx_f+=("$s")
}

# rx counts the cursor in characters, as READLINE_POINT does since bash 5.0.
# Before that READLINE_POINT counts bytes and is converted.
_rx_bytepoint=0
(( BASH_VERSINFO[0] < 5 )) && _rx_bytepoint=1

# _rx_tochars LINE BYTES: the character offset of byte offset BYTES in LINE.
_rx_tochars() {
  _rx_bytepre "$@"
  REPLY=${#REPLY}
}

# The first $2 bytes of $1. The local LC_ALL ends with the function, which
# restores the user's locale.
_rx_bytepre() {
  local LC_ALL=C
  REPLY=${1:0:$2}
}

# _rx_tobytes LINE CHARS: the byte offset of character offset CHARS in LINE.
_rx_tobytes() {
  _rx_bytelen "${1:0:$2}"
}

_rx_bytelen() {
  local LC_ALL=C
  REPLY=${#1}
}

# Sends response lines to rx in one write. SIGPIPE is ignored while rx-edit
# runs, so if rx has exited the write just fails; the read loop then sees EOF.
_rx_reply() {
  printf '%s\n' "$@" >&"$_rx_w" 2>/dev/null
}

# _rx_spec NAME: the completion spec for NAME as words in _rx_sp. -D is the
# default spec.
_rx_spec() {
  local line
  _rx_sp=()
  if [[ $1 == -D ]]; then
    line=$(complete -p -D 2>/dev/null) || return 1
  else
    [[ -n $1 ]] || return 1
    line=$(complete -p -- "$1" 2>/dev/null) || return 1
  fi
  # complete -p prints a command that recreates the spec, quoted for reuse.
  eval "_rx_sp=(${line#complete })" 2>/dev/null
}

# _rx_loadspec CMD: finds CMD's spec like <Tab> does, by name, then basename,
# then through bash-completion's on-demand loader. Parses it into _rx_fn
# (the -F function), _rx_cc (the -C command), _rx_cg (the compgen options)
# and _rx_opts (the -o options).
_rx_loadspec() {
  _rx_fn= _rx_cc= _rx_opts= _rx_cg=()
  if ! _rx_spec "$1" && ! { [[ $1 == */* ]] && _rx_spec "${1##*/}"; }; then
    if declare -F _comp_load >/dev/null; then
      _comp_load -- "$1" >/dev/null 2>&1 </dev/null
    elif declare -F __load_completion >/dev/null; then
      __load_completion "$1" >/dev/null 2>&1 </dev/null
    else
      return 1
    fi
    _rx_spec "$1" || _rx_spec "${1##*/}" || return 1
  fi
  _rx_parsespec
}

_rx_parsespec() {
  _rx_fn= _rx_cc= _rx_opts= _rx_cg=()
  local i=0 n=${#_rx_sp[@]} t
  while (( i < n )); do
    t=${_rx_sp[i]}
    case $t in
      -F) _rx_fn=${_rx_sp[i+1]}; (( i += 2 )) ;;
      -C) _rx_cc=${_rx_sp[i+1]}; (( i += 2 )) ;;
      -o) _rx_opts+=" ${_rx_sp[i+1]}"; (( i += 2 )) ;;
      -[WAGXPS]) _rx_cg+=("$t" "${_rx_sp[i+1]}"); (( i += 2 )) ;;
      -[DEI]) (( i++ )) ;;
      -?*) _rx_cg+=("$t"); (( i++ )) ;;
      *) break ;;
    esac
  done
}

# _rx_marker FN MARKER...: the first marker found in FN or the functions it
# calls, directly or not. CLI frameworks put the protocol call in a helper
# (cobra's __start_kubectl calls __kubectl_get_completion_results).
_rx_marker() {
  local -a queue=("$1") words
  local -A seen=(["$1"]=1)
  local i=0 fn body m w
  shift
  REPLY=
  while (( i < ${#queue[@]} && i < 200 )); do
    fn=${queue[i++]}
    body=$(declare -f -- "$fn" 2>/dev/null) || continue
    for m in "$@"; do
      # Quoted, so the marker matches literally.
      if [[ -n $m && $body == *"$m"* ]]; then
        REPLY=$m
        return
      fi
    done
    read -r -d '' -a words <<<"${body//[^A-Za-z0-9_:.-]/ }"
    for w in "${words[@]}"; do
      [[ -n ${seen[$w]} ]] && continue
      seen[$w]=1
      declare -F -- "$w" >/dev/null && queue+=("$w")
    done
  done
}

# INFO <word> [marker...]: alias expansion, completion function and the
# first marker found in that function. Markers identify functions generated
# by CLI frameworks rx can call directly (e.g. `cmd __complete` for cobra).
_rx_info() {
  local word=$1 exp= cmd fn= match= e
  shift
  [[ -n $word ]] && exp=${BASH_ALIASES[$word]}
  local -a ws=()
  read -r -a ws <<<"$exp"
  cmd=${ws[0]:-$word}
  if _rx_loadspec "$cmd"; then
    fn=$_rx_fn
    [[ -n $fn ]] && _rx_marker "$fn" "$@" && match=$REPLY
  fi
  _rx_esc "$exp"; e=$REPLY
  _rx_esc "$fn"; fn=$REPLY
  _rx_esc "$match"
  _rx_reply "I"$'\t'"$e"$'\t'"$fn"$'\t'"$REPLY" END
}

# _rx_isbreak C: whether C is in COMP_WORDBREAKS (quotes excluded, they
# quote rather than break).
_rx_isbreak() {
  [[ $1 != [\"\'] && ${COMP_WORDBREAKS-$' \t\n"\'><=;|&(:'} == *"$1"* ]]
}

# _rx_words LINE: splits LINE into _rx_cw the way readline builds
# COMP_WORDS: quotes and backslashes keep words together, and runs of
# COMP_WORDBREAKS characters are words of their own (--opt=v is "--opt",
# "=", "v"). After trailing whitespace the last word is empty.
_rx_words() {
  local s=$1 i c w= q= kind=0
  _rx_cw=()
  for (( i = 0; i < ${#s}; i++ )); do
    c=${s:i:1}
    if [[ -n $q ]]; then
      w+=$c
      if [[ $c == \\ && $q == '"' ]]; then
        w+=${s:i+1:1}; (( i++ ))
      elif [[ $c == "$q" ]]; then
        q=
      fi
      continue
    fi
    case $c in
      ' '|$'\t'|$'\n')
        (( kind )) && _rx_cw+=("$w")
        w= kind=0
        continue ;;
    esac
    if [[ $c != [\\\"\'] ]] && _rx_isbreak "$c"; then
      (( kind == 1 )) && { _rx_cw+=("$w"); w=; }
      w+=$c kind=2
      continue
    fi
    (( kind == 2 )) && { _rx_cw+=("$w"); w=; }
    kind=1
    case $c in
      \\) w+=$c${s:i+1:1}; (( i++ )) ;;
      \"|\') q=$c; w+=$c ;;
      *) w+=$c ;;
    esac
  done
  if (( kind )); then
    _rx_cw+=("$w")
  else
    _rx_cw+=("")
  fi
}

# _rx_dequote WORD: WORD with quotes and backslashes removed, without any
# expansion.
_rx_dequote() {
  local s=$1 out= c q= i
  for (( i = 0; i < ${#s}; i++ )); do
    c=${s:i:1}
    if [[ $q == "'" ]]; then
      [[ $c == "'" ]] && q= || out+=$c
    elif [[ $c == \\ ]]; then
      out+=${s:i+1:1}; (( i++ ))
    elif [[ $c == '"' ]]; then
      [[ -n $q ]] && q= || q=$c
    elif [[ $c == "'" && -z $q ]]; then
      q=$c
    else
      out+=$c
    fi
  done
  REPLY=$out
}

# _rx_gen GROUP COMPGEN-ARGS...: appends compgen's matches to _rx_hits.
_rx_gen() {
  local g=$1 v
  shift
  while IFS= read -r v; do
    _rx_hits+=("$v")
    _rx_tags+=("$g")
  done < <(compgen "$@" 2>/dev/null)
}

# Runs the parsed spec (see _rx_loadspec) for the COMP_* variables set by
# _rx_complete. Returns 124 if the function asks for a retry, which
# on-demand loaders do after loading the real spec.
_rx_runspec() {
  local _rx_st=0 _rx_n=${#_rx_hits[@]} _rx_v
  if [[ -n $_rx_fn ]]; then
    COMPREPLY=()
    "$_rx_fn" "$_rx_cmd" "$_rx_cur" "$_rx_prev" >/dev/null 2>&1 </dev/null
    _rx_st=$?
    (( _rx_st == 124 )) && return 124
    for _rx_v in "${COMPREPLY[@]}"; do
      _rx_hits+=("$_rx_v")
      _rx_tags+=("")
    done
  fi
  if [[ -n $_rx_cc ]]; then
    while IFS= read -r _rx_v; do
      _rx_hits+=("$_rx_v")
      _rx_tags+=("")
    done < <(eval "$_rx_cc"' "$_rx_cmd" "$_rx_cur" "$_rx_prev"' 2>/dev/null </dev/null)
  fi
  (( ${#_rx_cg[@]} )) && _rx_gen "" "${_rx_cg[@]}" -- "$_rx_cur"
  [[ $_rx_opts == *plusdirs* ]] && _rx_gen directories -d -- "$_rx_cur"
  if (( ${#_rx_hits[@]} == _rx_n )); then
    case $_rx_opts in
      *dirnames*) _rx_gen directories -d -- "$_rx_cur" ;;
      *default*) _rx_gen files -f -- "$_rx_cur" ;;
    esac
  fi
  return 0
}

# COMPLETE <line>: complete at the end of line and send the matches.
_rx_complete() {
  local _rx_line=$1 _rx_cmd _rx_cur _rx_prev= _rx_i _rx_v _rx_t
  local -a _rx_hits=() _rx_tags=() _rx_out=()
  _rx_words "$_rx_line"
  _rx_cmd=${_rx_cw[0]}
  # Like zsh without complete_aliases, an alias without a spec of its own
  # completes as its expansion.
  if [[ -n $_rx_cmd && -n ${BASH_ALIASES[$_rx_cmd]} ]] && ! _rx_spec "$_rx_cmd"; then
    _rx_line=${BASH_ALIASES[$_rx_cmd]}${_rx_line#*"$_rx_cmd"}
    _rx_words "$_rx_line"
    _rx_cmd=${_rx_cw[0]}
  fi
  local -x COMP_LINE=$_rx_line COMP_POINT=${#_rx_line} COMP_KEY=9
  # 37 is menu-complete: cobra then returns bare values, without the
  # descriptions it pads into listings.
  local -x COMP_TYPE=37
  local -a COMP_WORDS=("${_rx_cw[@]}") COMPREPLY=()
  local COMP_CWORD=$(( ${#_rx_cw[@]} - 1 ))
  _rx_cur=${_rx_cw[COMP_CWORD]}
  (( COMP_CWORD > 0 )) && _rx_prev=${_rx_cw[COMP_CWORD-1]}
  # After a break character such as "=", readline completes an empty word.
  _rx_isbreak "${_rx_cur:0:1}" && _rx_cur=
  _rx_dequote "$_rx_cur"; _rx_cur=$REPLY

  if _rx_loadspec "$_rx_cmd"; then
    _rx_runspec || { _rx_loadspec "$_rx_cmd" && _rx_runspec; }
  elif (( COMP_CWORD == 0 )); then
    _rx_gen commands -c -- "$_rx_cur"
  elif _rx_spec -D; then
    _rx_parsespec
    _rx_runspec || { _rx_loadspec "$_rx_cmd" && _rx_runspec; }
  else
    _rx_gen files -f -- "$_rx_cur"
  fi

  for (( _rx_i = 0; _rx_i < ${#_rx_hits[@]}; _rx_i++ )); do
    _rx_esc "${_rx_hits[_rx_i]}"; _rx_v=$REPLY
    _rx_esc "${_rx_tags[_rx_i]}"; _rx_t=$REPLY
    _rx_out+=("C"$'\t'"$_rx_v"$'\t\t'"$_rx_t")
  done
  _rx_reply "${_rx_out[@]}" END
}

# Shows a message below the prompt.
_rx_msg() {
  printf 'rx: %s\n' "$1" >&2
}

# The protocol loop: start rx, answer its requests, and leave the outcome in
# _rx_done, _rx_ended, _rx_run and _rx_err.
_rx_session() {
  local _rx_d _rx_r= _rx_req _rx_a _rx_pid _rx_point=$READLINE_POINT
  local -a _rx_args
  if (( _rx_bytepoint )); then
    _rx_tochars "$READLINE_LINE" "$READLINE_POINT"
    _rx_point=$REPLY
  fi
  if ! _rx_d=$(mktemp -d "${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/rx.XXXXXX" 2>/dev/null); then
    _rx_err="cannot create a temporary directory" _rx_ended=1
    return
  fi
  if ! mkfifo -m 600 "$_rx_d/in" "$_rx_d/out" 2>/dev/null; then
    _rx_err="mkfifo failed in ${_rx_d%/*}" _rx_ended=1
    rm -rf -- "$_rx_d"
    return
  fi
  # Opening a fifo blocks until the other end is opened too: the order here
  # matches rx's redirections, and neither side opens one read-write, so rx
  # exiting is seen as EOF below. Interactive bash announces background jobs
  # on its stderr even without job control, hence the outer redirection.
  {
    "$_rx_bin" edit --shell bash --cursor "$_rx_point" -- "$READLINE_LINE" \
      <"$_rx_d/in" >"$_rx_d/out" 2>>"$_rx_log_file" &
  } 2>/dev/null
  _rx_pid=$!
  # Writing to rx after it exited must not kill the shell.
  trap '' PIPE
  exec {_rx_w}>"$_rx_d/in" {_rx_r}<"$_rx_d/out"
  # Open descriptors keep working after the names are gone.
  rm -rf -- "$_rx_d"

  while IFS= read -r -u "$_rx_r" _rx_req; do
    _rx_split "$_rx_req"
    case ${_rx_f[0]} in
      INFO)
        _rx_args=()
        for _rx_a in "${_rx_f[@]:1}"; do
          _rx_unesc "$_rx_a"; _rx_args+=("$REPLY")
        done
        _rx_info "${_rx_args[@]}" ;;
      COMPLETE) _rx_unesc "${_rx_f[1]}"; _rx_complete "$REPLY" ;;
      DONE)
        _rx_unesc "${_rx_f[2]}"
        READLINE_LINE=$REPLY
        READLINE_POINT=${_rx_f[1]}
        if (( _rx_bytepoint )); then
          _rx_tobytes "$READLINE_LINE" "$READLINE_POINT"
          READLINE_POINT=$REPLY
        fi
        [[ ${_rx_f[3]} == run ]] && _rx_run=1
        _rx_done=1 _rx_ended=1
        break ;;
      CANCEL) _rx_ended=1; break ;;
      ERROR) _rx_unesc "${_rx_f[1]}"; _rx_err=$REPLY; _rx_ended=1; break ;;
    esac
  done
  exec {_rx_w}>&- {_rx_r}<&-
  _rx_w=
  # rx exits right after its last message; waiting for it means the
  # terminal is restored before readline redraws the prompt.
  wait "$_rx_pid" 2>/dev/null
}

rx-edit() {
  _rx_bindafter redraw-current-line
  if [[ -z ${READLINE_LINE//[[:space:]]/} ]]; then
    _rx_msg "nothing to edit"
    return 0
  fi
  if ! type -P -- "$_rx_bin" >/dev/null; then
    _rx_msg "binary not found: $_rx_bin"
    return 1
  fi
  local _rx_log_file=${RX_LOG:-${_rx_log:-${XDG_CACHE_HOME:-$HOME/.cache}/rx/rx.log}}
  [[ -d ${_rx_log_file%/*} ]] || mkdir -p -- "${_rx_log_file%/*}" 2>/dev/null
  local _rx_orig_line=$READLINE_LINE _rx_orig_point=$READLINE_POINT
  local _rx_done=0 _rx_ended=0 _rx_run=0 _rx_err= _rx_k _rx_al=
  # bash writes history when it exits; flush this session's commands so rx
  # sees them.
  [[ -n $HISTFILE ]] && history -a 2>/dev/null
  for _rx_k in "${!BASH_ALIASES[@]}"; do
    _rx_esc "$_rx_k=${BASH_ALIASES[$_rx_k]}"
    _rx_al+=$REPLY$'\n'
  done
  local -x RX_ALIASES=${_rx_al%$'\n'}
  local -x RX_HISTFILE=${HISTFILE:-$HOME/.bash_history}

  # Without job control rx stays in the shell's process group, so it may
  # read the terminal, and no job notices are printed.
  local _rx_mon=0 _rx_pipe
  [[ $- == *m* ]] && _rx_mon=1
  _rx_pipe=$(trap -p PIPE)
  set +m
  _rx_session
  (( _rx_mon )) && set -m
  if [[ -n $_rx_pipe ]]; then
    eval "$_rx_pipe"
  else
    trap - PIPE
  fi

  if (( ! _rx_done )); then
    READLINE_LINE=$_rx_orig_line
    READLINE_POINT=$_rx_orig_point
  fi
  (( _rx_ended )) || _rx_err="exited unexpectedly (see $_rx_log_file)"
  [[ -n $_rx_err ]] && _rx_msg "$_rx_err"
  # Run the command as if Enter was pressed, so it lands in history and
  # PROMPT_COMMAND runs.
  (( _rx_run )) && _rx_bindafter accept-line
  return 0
}

# _rx_bindafter FUNCTION: what the second key of the macro does.
_rx_bindafter() {
  local km
  for km in emacs vi-insert vi-command; do
    bind -m "$km" "\"$_rx_seq_after\": $1"
  done
}

# _rx_keyseq KEY: KEY in readline syntax. Caret notation as used by the
# config file and zsh (^X^X, ^[e) is converted; anything with a backslash
# is taken to be readline syntax already (\C-x\C-x, \ee).
_rx_keyseq() {
  local s=$1 out= c
  if [[ $s == *\\* ]]; then
    REPLY=$s
    return
  fi
  while [[ -n $s ]]; do
    c=${s:0:1}
    s=${s:1}
    if [[ $c == ^ && -n $s ]]; then
      c=${s:0:1}
      s=${s:1}
      case $c in
        \[) out+='\e' ;;
        \?) out+='\C-?' ;;
        *) out+='\C-'${c,,} ;;
      esac
    elif [[ $c == '"' ]]; then
      out+='\"'
    else
      out+=$c
    fi
  done
  REPLY=$out
}

# rx-bind [key]: bind rx-edit in the emacs, vi-insert and vi-command
# keymaps. Takes caret notation (^X^X) or readline syntax (\C-x\C-x).
rx-bind() {
  local km
  _rx_keyseq "${1:-${RX_KEY:-${_rx_key:-^X^X}}}"
  for km in emacs vi-insert vi-command; do
    bind -m "$km" -x "\"$_rx_seq_edit\": rx-edit"
    bind -m "$km" "\"$REPLY\": \"$_rx_seq_edit$_rx_seq_after\""
  done
  _rx_bindafter redraw-current-line
}

# rx-doctor: rx's own checks plus the shell-side state rx can't see.
rx-doctor() {
  "$_rx_bin" doctor --shell bash
  local km l keys mode
  mode=$(bind -v 2>/dev/null | grep -F 'editing-mode')
  printf '%s\n' "widget:" "  binary       $_rx_bin" "  editing mode ${mode##* }"
  for km in emacs vi-insert vi-command; do
    keys=
    # Only the keys: the macro they type is internal.
    while IFS= read -r l; do
      keys+=" ${l%%: *}"
    done < <(bind -m "$km" -s 2>/dev/null | grep -F "$_rx_seq_edit$_rx_seq_after")
    printf '  %-12s %s\n' "$km" "${keys:- (not bound)}"
  done
  printf '  log          %s\n' "${RX_LOG:-${_rx_log:-${XDG_CACHE_HOME:-$HOME/.cache}/rx/rx.log}}"
}

if (( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 1) )); then
  printf 'rx: bash %s is too old, rx needs 4.1 or newer\n' "$BASH_VERSION" >&2
elif [[ $- == *i* ]]; then
  rx-bind
fi
