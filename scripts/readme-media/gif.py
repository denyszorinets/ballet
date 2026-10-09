# /// script
# requires-python = ">=3.11"
# dependencies = ["pillow>=11"]
# ///
"""Turns the frames record.mjs saved into GIFs for the README.

Usage: uv run scripts/readme-media/gif.py <frames dir> <output dir>
"""

import json
import sys
from pathlib import Path

from PIL import Image

WIDTH = 900  # GitHub shows README images at most ~880 px wide.


def frames(dir: Path) -> tuple[list[Image.Image], list[int]]:
    manifest = json.loads((dir / "frames.json").read_text())
    images, durations = [], []
    for f in manifest:
        im = Image.open(dir / f["file"]).convert("RGB")
        im = im.resize((WIDTH, round(im.height * WIDTH / im.width)), Image.LANCZOS)
        images.append(im)
        durations.append(max(int(f["ms"]), 40))
    return images, durations


def gif(dir: Path, out: Path) -> None:
    images, durations = frames(dir)
    # One palette for the whole animation keeps colors stable between frames.
    sample = images[:: max(1, len(images) // 8)]
    sheet = Image.new("RGB", (WIDTH, sum(im.height for im in sample)))
    y = 0
    for im in sample:
        sheet.paste(im, (0, y))
        y += im.height
    palette = sheet.quantize(colors=160, method=Image.Quantize.MEDIANCUT)
    quantized = [im.quantize(palette=palette, dither=Image.Dither.NONE) for im in images]
    quantized[0].save(out, save_all=True, append_images=quantized[1:], duration=durations,
                      loop=0, optimize=True, disposal=1)
    print(f"{out}: {len(images)} frames, {sum(durations) / 1000:.1f} s, {out.stat().st_size // 1024} KiB")


def main() -> None:
    src, dst = Path(sys.argv[1]), Path(sys.argv[2])
    dst.mkdir(parents=True, exist_ok=True)
    for d in sorted(p for p in src.iterdir() if (p / "frames.json").exists()):
        gif(d, dst / f"{d.name}.gif")
    for still in src.glob("*.png"):
        im = Image.open(still).convert("RGB")
        im.save(dst / still.name, optimize=True)


if __name__ == "__main__":
    main()
