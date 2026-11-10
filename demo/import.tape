
# Find logs files
Type 'find ~/.local/state -name "*.log" -mtime -1'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

Ctrl+n
Sleep 500ms
Ctrl+n
Enter
Sleep 1s
Enter

Sleep 2s

# Git tags
Type 'git tag -a "v0.39.2" -m "v0.39.2"'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

Type v0.40.1
Sleep 500ms
Tab
Type v0.40.1
Enter
Sleep 500ms
Enter

Sleep 2s

# SSH hosts
Type 'ssh -i ~/.ssh/id_ed25519 user@server01'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

Tab
Sleep 500ms
Type amir
Sleep 500ms
Ctrl+N
Sleep 500ms
Ctrl+N
Enter

Sleep 2s
