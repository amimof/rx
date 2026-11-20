# Fills the demo shell's history with ssh logins through a bastion, for
# demo-ssh.tape. Source it after `rx init zsh`.
#
# ssh has no plugin, so rx learns from this history that -o, -L and -J take
# a value (each is used at least 3 times with one), and zooming in on a
# value lists the same part of earlier values: -o keys and values, -L ports,
# users of user@host. The zstyles pin the hosts and users zsh completes, so
# the gif doesn't show the hosts of the machine that renders it.
#
# The history goes to a file under /tmp, never ~/.zsh_history. Timestamps
# are relative to now, so rx's recency ranking looks the same whenever the
# gif is rendered.
() {
  zmodload zsh/datetime
  HISTFILE=/tmp/rx-demo-ssh.hist
  HISTSIZE=1000 SAVEHIST=1000
  setopt extended_history

  # rx's shell source is zsh's completion system, which a shell without a
  # .zshrc hasn't loaded.
  if (( ! $+functions[compdef] )); then
    autoload -Uz compinit
    compinit -u -D
  fi
  zstyle ':completion:*' hosts bastion bastion-02 web-01.prod web-02.prod db-01.prod db-02.prod
  zstyle ':completion:*' users deploy admin ops

  # <minutes ago>|<command>, oldest first.
  local age cmd
  while IFS='|' read -r age cmd; do
    print -r -- ": $(( EPOCHSECONDS - age * 60 )):0;$cmd"
  done > $HISTFILE <<'EOF'
20100|ssh -J ops@bastion admin@web-01.prod
20080|ssh -o StrictHostKeyChecking=accept-new -J ops@bastion deploy@web-02.prod
20050|ssh -L 8080:localhost:80 -J ops@bastion admin@web-02.prod
14500|ssh -o ServerAliveInterval=30 -J ops@bastion-02 ops@db-01.prod
14480|ssh -L 5432:db-01:5432 -J ops@bastion ops@db-01.prod
14400|ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null -J ops@bastion admin@web-01.prod
10100|ssh -L 15432:db-02:5432 -J ops@bastion-02 ops@db-02.prod
10080|ssh -o ServerAliveInterval=30 -L 8443:localhost:443 -J ops@bastion deploy@web-01.prod
10020|ssh -o StrictHostKeyChecking=no -J ops@bastion admin@db-02.prod
4400|ssh -L 9090:localhost:9090 -J ops@bastion admin@web-02.prod
4380|ssh -o StrictHostKeyChecking=accept-new -J ops@bastion-02 admin@web-01.prod
4300|ssh -L 15432:db-01:5432 -J ops@bastion deploy@db-01.prod
1500|ssh -o ServerAliveInterval=30 -J ops@bastion admin@web-02.prod
1450|ssh -o StrictHostKeyChecking=accept-new -L 8080:localhost:80 -J ops@bastion admin@web-01.prod
240|ssh -J ops@bastion admin@web-01.prod
120|ssh -L 5432:db-01:5432 -J ops@bastion deploy@db-01.prod
45|ssh -o StrictHostKeyChecking=no -L 5432:db-01:5432 -J ops@bastion deploy@web-01.prod
EOF

  # Load it into the shell too, so Up recalls the last command.
  fc -R $HISTFILE
}
