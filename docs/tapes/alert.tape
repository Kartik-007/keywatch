# Records `keywatch --demo watch run` (the hourly check firing an alert on
# the …a91f spike) for the README.
# Run from the repo root after installing the binary:
#   go install ./cmd/keywatch && vhs docs/tapes/alert.tape
# Demo mode is offline and uses obviously fake keys.

Output docs/alert.gif

Require keywatch

Set Shell "bash"
Set FontSize 16
Set Width 1360
Set Height 620
Set Padding 24
Set Theme "GitHub Dark"
Set TypingSpeed 45ms
Set WindowBar Colorful

Hide
Type "clear"
Enter
Show

Sleep 500ms
Type "keywatch --demo watch run"
Sleep 600ms
Enter
Sleep 7s
