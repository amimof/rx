# Runs an interactive shell under zpty and plays steps against it: zsh with
# ZDOTDIR=<dir>, bash with <dir>/.bashrc, or fish with <dir>/config.fish.
# The terminal output is written to <dir>/transcript. Output is drained
# during every step: a full pty buffer blocks the programs writing to the
# terminal, like rx redrawing its editor.
#
# Usage: zsh -f driver.zsh <dir> <path to zsh, bash or fish> <step>...
#
# The shell is given by path since zsh may reset PATH at startup. The
# shells start without the system rc files (zsh -d, bash --rcfile, fish
# --no-config), so the tests depend only on the rc file they write:
# Debian's /etc/zsh/zshrc, for one, runs compinit, which stops at a prompt
# on insecure fpath directories. fish keeps its data under <dir>.
#
# Steps:
#   key:<bytes>     type bytes (no newline is added)
#   expect:<text>   wait until the output contains text, fail after 5s
#   file:<path>     wait until path exists, fail after 5s
#   sleep:<secs>    sleep
zmodload zsh/zpty zsh/datetime || exit 2
dir=$1 sh=$2
shift 2
export ZDOTDIR=$dir TERM=xterm-256color
# Keep the user's readline settings out of bash.
export INPUTRC=$dir/inputrc

typeset -g seen=
drain() {
  local chunk
  while zpty -rt z chunk 2>/dev/null; do
    seen+=$chunk
    # fish 4 waits for the answer to its Primary Device Attributes query
    # before it shows a prompt; answer as a VT220-class terminal.
    [[ $chunk == *$'\e[0c'* || $chunk == *$'\e[c'* ]] && zpty -w -n z $'\e[?62c'
  done
}
finish() {
  drain
  print -rn -- "$seen" > $dir/transcript
  zpty -d z 2>/dev/null
  # zpty -d hangs up the shell without waiting for it. Wait until it has
  # exited (the rc file writes its pid), so it is done writing to <dir>,
  # e.g. bash saving its history on SIGHUP, before the test removes it.
  local pid end=$(( EPOCHREALTIME + 2 ))
  [[ -r $dir/pid ]] && pid=$(<$dir/pid)
  while [[ -n $pid ]] && kill -0 $pid 2>/dev/null && (( EPOCHREALTIME < end )); do
    sleep 0.02
  done
  exit $1
}

case ${sh:t} in
  (zsh) zpty -b z $sh -d -i ;;
  (bash) zpty -b z $sh --noprofile --rcfile $dir/.bashrc -i ;;
  (fish)
    export XDG_CONFIG_HOME=$dir/config XDG_DATA_HOME=$dir/data
    # zpty evals its command, hence (q).
    zpty -b z $sh --no-config -C ${(q):-"source $dir/config.fish"} -i ;;
  (*) print -u2 "bad shell: $sh"; exit 2 ;;
esac
local step deadline
for step in "$@"; do
  deadline=$(( EPOCHREALTIME + 5 ))
  case $step in
    (key:*)
      zpty -w -n z "${step#key:}"
      sleep 0.05
      drain ;;
    (expect:*)
      until drain; [[ $seen == *"${step#expect:}"* ]]; do
        (( EPOCHREALTIME > deadline )) && { print -u2 "timeout: $step"; finish 1 }
        sleep 0.05
      done
      # Only match output produced after this point next time.
      seen= ;;
    (file:*)
      until [[ -e ${step#file:} ]]; do
        (( EPOCHREALTIME > deadline )) && { print -u2 "timeout: $step"; finish 1 }
        drain
        sleep 0.05
      done ;;
    (sleep:*)
      deadline=$(( EPOCHREALTIME + ${step#sleep:} ))
      while (( EPOCHREALTIME < deadline )); do
        drain
        sleep 0.05
      done ;;
    (*)
      print -u2 "bad step: $step"
      finish 2 ;;
  esac
done
finish 0
