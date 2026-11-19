# Shared setup, sourced by every demo tape. Run vhs from demo/, since VHS
# resolves Source paths and the shell resolves ../bin from there.
Set FontSize 16
Set Width 1300
Set Height 650
Set Shell "zsh"

# Initialize rx and clear the screen before recording starts. The sourcing
# tape calls Show, so it can run its own hidden setup first.
Hide
# Start on the same slots every time; rx would otherwise remember the slot
# last edited in earlier recordings.
Type 'export RX_FOCUS_LAST=false'
Enter
Type 'eval "$(../bin/rx init zsh)"'
Enter
Type "clear"
Enter
Sleep 500ms
