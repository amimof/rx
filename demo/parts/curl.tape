# Recall a curl command and open it on the URL
Ctrl+u
Type 'curl https://localhost:7878/compute/v2.1/servers/detail -k'
Sleep 1s
# open rx
Ctrl+X
Sleep 75ms
Ctrl+X
Sleep 1s

# Zoom in on URL and change port and path
Ctrl+f
Sleep 500ms
Tab@500ms 2
Sleep 500ms
Type '8443'
Sleep 500ms
Tab@500ms 3
Type 'instances'
Sleep 1s

# Switch to POST with the curl plugin's shortcut. The post-adds-body rule
# adds -d '{}' and shows a notice under the list.
Ctrl+X
Sleep 2s

# Add --max-time and pick a value from history. Tab twice: past the -d the
# rule added, onto "+ flag".
Tab@500ms 2
Type 'max'
Sleep 500ms
Tab@500ms
Ctrl+N
Sleep 750ms
Enter@2s
