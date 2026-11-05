---
name: rexshow
description: Show the user the screenshots and screen recordings you make, in a Rex pane beside yours, drawn with the kitty image protocol. Use in a Rex terminal whenever you capture a screenshot, a browser or desktop recording, or any image or video the user should see (UI checks, before/after, test runs, bug repros), or when the user says "show me", "let me see it", "show the screenshot", or "play the recording". Drive it with the `rexshow` CLI.
---

# rexshow: show screenshots and recordings in Rex

The user cannot see images or videos in the chat. `rexshow` opens a pane next
to yours that shows them: the newest item large, a strip of thumbnails under
it, and videos that play in a loop. Each agent pane gets its own viewer
(keyed by `$REX_BLOCK`).

Only use it in Rex (`$REX_BLOCK` is set). Elsewhere, give the file paths.

## Start

Watch the folder you save media into, then open the pane once:

```bash
rexshow watch "$ARTIFACTS_DIR"   # or any folder you save screenshots and videos to
rexshow show
```

Every image or video that lands in a watched folder appears on its own,
newest last. A video appears a few seconds after the recording stops.

## Add one file

Add a file from anywhere, with a caption that says what it shows:

```bash
rexshow add -c "Pitch page after the cover upload" /path/to/p8-covered.png
rexshow add -c "Create-pitch flow, start to finish" /path/to/pitch-flow.mp4
```

- `add` takes several files; the viewer jumps to the newest.
- Adding a file again moves it to the end and updates its caption.
- Captions are short and plain: what the user looks at and why.

## Good habits

- Show the result as soon as you have it, not in a batch at the end.
- For a before/after, add both with captions "Before: …" and "After: …".
- Mention in chat what the user should look for: "The card on the right
  now shows 4 of 4."
- Leave the pane open when you finish. Close it with `rexshow hide` only
  when the user asks.

## Other commands

```bash
rexshow list              # what the viewer shows, oldest first
rexshow unwatch DIR       # stop watching a folder
rexshow clear             # forget all files and folders
rexshow show --below      # split downward; --ratio PCT sets the pane's share (default 40)
```

Several agents can share one viewer: pass the same `--list NAME` (or set
`REXSHOW_LIST=NAME`) on every command, including `show`.

Formats: png, jpg, gif (first frame), webp, bmp; mp4, webm, mov, mkv (needs
`ffmpeg` and `ffprobe`). Videos play at 12 fps, up to five minutes.

In the pane the user can press ← → to move between items, g / G for the
first and newest, space to pause, , and . to step a frame, and q to close.
To zoom: + and − or the mouse wheel (toward the pointer), z for 2×, 0 to fit.
While zoomed, the arrows or h j k l pan, and n / p change the item. When you
point the user at a small detail, tell them to zoom: "press z, then pan
right to the card".

On a phone (Rex on iOS sends taps, not keys, swipes, or pinches) the user
uses the buttons at the bottom (◀ ▶ − + fit, and play/pause for a video),
taps a thumbnail to open it, taps the image to zoom to 2× at that point, and
taps again to move the tapped point to the middle. Give phone directions in
taps: "tap the card on the right to zoom in on it".
