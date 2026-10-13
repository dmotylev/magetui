from pathlib import Path

frames = Path("examples/fast-exit.txt").read_text().split("\n" + "─" * 80 + "\n")
screen = next(frame for frame in reversed(frames) if frame.strip())
for marker in ("SUCCESS_SCREEN_MARKER", "FAILURE_SCREEN_MARKER"):
    if marker not in screen.splitlines():
        raise SystemExit(f"Terminal content was lost: {marker}\n{screen}")
for result in ("✓ fastSuccess", "✗ fastFailure", "Error: fast failure"):
    if result not in screen:
        raise SystemExit(f"Target did not finish: {result}\n{screen}")
