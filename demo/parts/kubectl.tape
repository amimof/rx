# Set the namespace to kube-system
Ctrl+u
Type 'kubectl get pods'
Sleep 500ms
# open rx
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Tab@500ms 3
Type '-n'
Sleep 500ms
Tab@500ms
Type kube
Sleep 500ms
Ctrl+n
Sleep 500ms
Enter@1500ms

# Toggle all namespaces (-A)
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Ctrl+A
Sleep 500ms
Enter@2500ms

# Drop -A and set the namespace to capi-system
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 500ms
Tab@500ms 3
Ctrl+d
Type '-n'
Sleep 500ms
Tab
Type 'capi'
Sleep 500ms
Ctrl+n
Sleep 500ms
Enter@2s

# Pick a control-plane pod, then toggle watch and cycle the output
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Shift+Tab
Sleep 500ms
Type 'control'
Sleep 500ms
Ctrl+n
Sleep 500ms
Tab@500ms
Ctrl+t
Sleep 500ms
Ctrl+o
Sleep 500ms
Enter@2s

# Switch to cert-manager, jump to the name and pick the webhook pod
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s
Type 'cert'
Sleep 500ms
Ctrl+n
Sleep 500ms
Ctrl+g
Sleep 500ms
Type 'webhook'
Sleep 500ms
Ctrl+n
Sleep 500ms
Enter@2s

# Turn off watch and output, then cycle the subcommand to describe
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
Enter@2s
