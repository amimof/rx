# ssh has no plugin: rx learns from history (ssh-history.zsh) which flags
# take a value, and gets hosts from zsh's own ssh completion.
Ctrl+u
Type 'ssh -o StrictHostKeyChecking=no -L 5432:db-01:5432 -J ops@bastion deploy@web-01.prod'
Sleep 500ms
# open rx, on the first flag: -o
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

# Zoom in on key=value and pick another value used with -o before
Ctrl+f
Sleep 500ms
Tab
Sleep 500ms
Type 'acc'
Sleep 500ms
Ctrl+n
Sleep 500ms
Ctrl+f
Sleep 1s

# Zoom in on the forward and change only the local port
Tab
Sleep 500ms
Ctrl+f
Sleep 500ms
Type '15432'
Sleep 500ms
Ctrl+f
Sleep 1s

# The jump host: switch to zsh's completions, then back to all sources
Tab
Sleep 500ms
Ctrl+S
Sleep 750ms
Ctrl+S
Sleep 1500ms
Ctrl+S
Sleep 500ms

# Zoom in on user@host: pick a user, then edit the host in place with
# insert mode, after "web"
Tab
Sleep 500ms
Ctrl+f
Sleep 500ms
Ctrl+n
Sleep 500ms
Tab
Sleep 500ms
Alt+i
Sleep 500ms
Ctrl+a
Sleep 300ms
Alt+f
Sleep 500ms
Type '-cluster'
Sleep 500ms
# Apply the part, then zoom out
Enter
Sleep 500ms
Enter
Sleep 1s

# Drop -o, then undo
Shift+Tab
Sleep 300ms
Shift+Tab
Sleep 300ms
Shift+Tab
Sleep 500ms
Ctrl+d
Sleep 1s
Ctrl+z
Sleep 1s

Enter@2s
