# Records assets/demo.gif against the real zygos binary and the local demo stub.
# See demo/README.md for how to regenerate.

Output assets/demo.gif

Set Shell bash
Set FontSize 14
Set Width 1180
Set Height 420
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set BorderRadius 8
Set TypingSpeed 45ms

Hide
Type "export PS1='$ ' HOME=/tmp/zygosdemo ZYGOS_BIN=/tmp/zygos PATH=/tmp:$PATH"
Enter
Sleep 400ms
Type "clear"
Enter
Sleep 600ms
Show

Sleep 800ms

# Three trackers, one config. Secrets are masked on the way out.
Type "zygos config list"
Enter
Sleep 3s

Type "zygos config test"
Enter
Sleep 3500ms

Hide
Type "clear"
Enter
Show
Sleep 500ms

# What the agent actually calls. One tool, every tracker.
Type "zygos-call tasks_list"
Enter
Sleep 6s
