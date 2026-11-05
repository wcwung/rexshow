# rexshow

Show screenshots and screen recordings in a [Rex](https://superlogical.com) pane, drawn
with the kitty graphics protocol. Coding agents cannot show images in their
chat; `rexshow` gives them a pane beside theirs that does.

- The newest item shows large, with a strip of thumbnails under it.
- Videos (mp4, webm, mov, mkv) play in a loop at 12 fps, with pause and frame step.
- Zoom and pan, with keys, the mouse wheel, or taps.
- Works with Rex on iOS, which sends taps only: every control is a button.
- Watch a folder, and each new image or video shows by itself.

## Install

Needs Go 1.27 or later, and `ffmpeg` and `ffprobe` for video.

```bash
git clone https://github.com/wcwung/rexshow
cd rexshow
make install    # builds ~/.local/bin/rexshow and links the skill into ~/.claude/skills
```

Or, without the clone:

```bash
go install github.com/wcwung/rexshow@latest
rexshow skill install
```

## Use

```bash
rexshow watch ./screenshots                # show every new image or video in a folder
rexshow add -c "After the fix" shot.png    # add one file, with a caption
rexshow show                               # open the pane beside yours
rexshow hide | list | clear | unwatch DIR
```

Each Rex pane gets its own viewer (keyed by `$REX_BLOCK`). To share one viewer
between several agents, pass the same `--list NAME` on every command.

## Controls

| Keys | Mouse or touch | Action |
|---|---|---|
| ← → | ◀ ▶ buttons | previous or next item |
| g / G | tap a thumbnail | first or newest item, or that item |
| + − | − + buttons, wheel | zoom (the wheel zooms toward the pointer) |
| z | tap the image | zoom to 2× (a tap zooms at that point) |
| 0 | fit button | back to the full image |
| arrows, h j k l (zoomed) | tap the image (zoomed) | pan (a tap moves that point to the middle) |
| space | ❚❚ ▶ button | pause or play a video |
| , . | | step a video one frame |
| q | | close |

## The agent skill

`skills/rexshow/SKILL.md` tells an agent to watch the folder where it saves
media, open the pane, add captions, and show results as soon as it has them.
`rexshow skill show` prints it; `rexshow skill install` writes it to
`~/.claude/skills/rexshow`.

## How it works

Images are decoded, scaled to at most 4096 px, compressed, and sent once.
Zoom and pan only move the placement window over the sent image. `ffmpeg`
decodes a video in the background to compressed frames, so a loop, a pause,
or a step back needs no new decode. Playback takes its frame from the wall
clock, so a slow link skips frames and does not play slow.
