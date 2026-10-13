Output examples/fast-exit.txt

Require go

Set Shell "bash"
Set FontSize 14
Set Width 1100
Set Height 700
Set Padding 12
Set TypingSpeed 0

Env MAGETUI_OSC_PROGRESS "off"

Hide
Type "PS1='> '; clear"
Enter
Sleep 500ms
Show

Type "printf '\123\125\103\103\105\123\123\137\123\103\122\105\105\116\137\115\101\122\113\105\122\n'; go tool mage -d examples fastSuccess"
Enter
Sleep 1s

Type "printf '\106\101\111\114\125\122\105\137\123\103\122\105\105\116\137\115\101\122\113\105\122\n'; go tool mage -d examples fastFailure"
Enter
Sleep 1s
