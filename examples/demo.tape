# Records the README GIF off the examples rig, and doubles as the CI
# real-PTY smoke: the .txt sibling captures the final screen, which the
# vhs workflow job asserts on. Golden tests cover the layout core with
# no terminal (DESIGN.md §7); this is the one place bubbletea boots on
# a real PTY. Run from the repo root: `vhs examples/demo.tape`.

Output examples/demo.gif
Output examples/demo.txt

Require go

Set Shell "bash"
Set FontSize 16
Set Width 880
Set Height 520
Set Padding 12

# Compiled by a prior `go tool mage -d examples -l` (CI) or any earlier
# run, so the recording starts hot instead of filming the Go compiler.
Type "go tool mage -d examples demo"
Enter
Sleep 7s
