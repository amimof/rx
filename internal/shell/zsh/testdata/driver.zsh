# Runs an interactive zsh under zpty with ZDOTDIR=<dir> and plays steps
# against it. The terminal output is written to <dir>/transcript. Output is
# drained during every step: a full pty buffer blocks the programs writing
# to the terminal, like rx redrawing its editor.
#
# Usage: zsh -f driver.zsh <dir> <step>...
#
# Steps:
#   key:<bytes>     type bytes (no newline is added)
#   expect:<text>   wait until the output contains text, fail after 5s
#   file:<path>     wait until path exists, fail after 5s
#   sleep:<secs>    sleep
zmodload zsh/zpty zsh/datetime || exit 2
dir=$1
shift
export ZDOTDIR=$dir TERM=xterm-256color

typeset -g seen=
drain() {
  local chunk
  while zpty -rt z chunk 2>/dev/null; do
    seen+=$chunk
  done
}
finish() {
  drain
  print -rn -- "$seen" > $dir/transcript
  zpty -d z 2>/dev/null
  exit $1
}

zpty -b z zsh -i
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
