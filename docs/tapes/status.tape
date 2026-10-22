# Records `keywatch --demo status` (one row per key) for the README.
# Run from the repo root after installing the binary:
#   go install ./cmd/keywatch && vhs docs/tapes/status.tape
# Demo mode is offline and uses obviously fake keys.

Output docs/status.gif

Require keywatch

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 560
Set Padding 24
Set Theme "GitHub Dark"
Set TypingSpeed 45ms
Set WindowBar Colorful

Hide
Type "clear"
Enter
Show

Sleep 500ms
Type "keywatch --demo status"
Sleep 600ms
Enter
Sleep 6s
