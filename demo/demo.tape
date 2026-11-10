Output demo.gif

Set FontSize 14
Set Width 1300
Set Height 650
Set Shell "zsh"

# Initialize everything before recording starts
Hide
Type 'eval "$(/home/amir/Projects/github.com/amimof/rx/bin/rx init zsh)"'
Enter

# Clear the terminal
Type "clear"
Enter
Sleep 500ms

Show

# Find logs files
Type 'find ~/.local/state -name "*.log" -mtime -1'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Type ~/.cache/
Sleep 500ms
Enter

Sleep 2s

# Git tags
Ctrl+u
Type 'git tag -a "v0.39.2" -m "v0.39.2"'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

Type v0.40.1
Sleep 500ms
Tab
Sleep 500ms
Type v0.40.1
Sleep 500ms
Enter

Sleep 2s

# SSH hosts
Ctrl+u
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

# Kubectl
Ctrl+u
Type 'kubectl get pods'
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

Tab
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 500ms
Type '-n'
Sleep 500ms
Tab
Sleep 500ms
Type kube
Sleep 500ms
Enter
Sleep 1s


# kubectl get pods -n kube-system
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Ctrl+A
Sleep 500ms
Enter
Sleep 2s


# kubectl get pods -n capi-system
Sleep 500ms
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 500ms
Ctrl+d
Type '-n'
Sleep 500ms
Tab
Type 'capi'
Sleep 500ms
Enter
Sleep 2s

# kubectl get pods <pod-name> -n capi-system -w -o yaml
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Shift+Tab
Sleep 500ms
Type 'control'
Sleep  500ms
Tab
Sleep 500ms
Ctrl+t
Sleep 500ms
Ctrl+o
Sleep 500ms
Enter
Sleep 2s

# kubectl get pods pod-name -n cert-manager
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Type 'cert'
Sleep 500ms
Ctrl+g
Sleep 500ms
Type 'webhook'
Sleep 500ms
Enter
Sleep 2s

# kubectl describe pods pod-name -n cert-manager
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Ctrl+t
Sleep 500ms
Ctrl+o
Sleep 500ms
Ctrl+d
Sleep 750ms
Ctrl+v
Sleep 500ms
Enter

Sleep 4s

