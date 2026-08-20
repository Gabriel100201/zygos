# Produces assets/screenshot.png — a still of Zygos aggregating two trackers,
# used as the repo's social preview and as the case-study visual in the
# portfolio. Regenerate with ./demo/record.sh (it runs this after the GIF).

Output demo/.screenshot-scratch.gif
# The still is the last frame of this short recording; vhs's own Screenshot
# command writes nothing on 0.11, so record.sh extracts the frame instead.

Set Shell bash
Set FontSize 16
Set Width 1160
Set Height 720
Set Padding 28
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 1ms

Hide
Type "export PS1='$ ' ZYGOS_CONFIG=/tmp/zygosdemo/config.yaml ZYGOS_BIN=/tmp/zygos PATH=/tmp:$PATH"
Enter
Sleep 400ms
Type "clear"
Enter
Sleep 600ms
Type "zygos config list"
Enter
Sleep 2s
Type "zygos-call tasks_list"
Enter
Sleep 4s
Show
Sleep 300ms
