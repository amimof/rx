# Runs an interactive shell under zpty and plays steps against it: zsh with
# ZDOTDIR=<dir>, or bash with <dir>/.bashrc. The terminal output is written
# to <dir>/transcript. Output is drained during every step: a full pty
# buffer blocks the programs writing to the terminal, like rx redrawing its
# editor.
#
# Usage: zsh -f driver.zsh <dir> <path to zsh or bash> <step>...
#
# The shell is given by path since zsh may reset PATH at startup.
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
  done
}
finish() {
  drain
  print -rn -- "$seen" > $dir/transcript
  zpty -d z 2>/dev/null
  exit $1
}

case ${sh:t} in
  (zsh) zpty -b z $sh -i ;;
  (bash) zpty -b z $sh --noprofile --rcfile $dir/.bashrc -i ;;
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
