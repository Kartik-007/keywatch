# Records `keywatch --demo` (the first scan) for the README hero.
# Run from the repo root after installing the binary:
#   go install ./cmd/keywatch && vhs docs/tapes/scan.tape
# Demo mode is offline and uses obviously fake keys.

Output docs/scan.gif

Require keywatch

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 860
Set Padding 24
Set Theme "GitHub Dark"
Set TypingSpeed 45ms
Set WindowBar Colorful

Hide
Type "clear"
Enter
Show

Sleep 500ms
Type "keywatch --demo"
Sleep 600ms
Enter
Sleep 2s
# The scan asks once before reading Railway, Vercel and GitHub. Answer it on
# camera. If the final scan doesn't prompt in demo mode, delete these 3 lines.
Type "y"
Sleep 400ms
Enter
Sleep 6s
