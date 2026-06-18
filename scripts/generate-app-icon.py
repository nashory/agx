#!/usr/bin/env python3
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
FONT_DIR = Path("/Users/craigstarr/github/youtube-visuals-remotion/public/fonts")
SIZE = 1024
SCALE = 3
CANVAS = SIZE * SCALE
TEXT = "AGX"


def load_font(size: int) -> ImageFont.FreeTypeFont:
    candidates = [
        FONT_DIR / "JetBrainsMono-ExtraBold.ttf",
        FONT_DIR / "JetBrainsMono-Bold.ttf",
        "/System/Library/Fonts/SFNS.ttf",
        "/System/Library/Fonts/HelveticaNeue.ttc",
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
    ]
    for path in candidates:
        font_path = Path(path)
        if font_path.exists():
            return ImageFont.truetype(font_path, size=size)
    return ImageFont.load_default(size=size)


def text_width(draw: ImageDraw.ImageDraw, font: ImageFont.ImageFont, tracking: int) -> int:
    width = 0
    for index, letter in enumerate(TEXT):
        box = draw.textbbox((0, 0), letter, font=font)
        width += box[2] - box[0]
        if index < len(TEXT) - 1:
            width += tracking
    return width


def draw_tracked_text(draw: ImageDraw.ImageDraw, xy: tuple[int, int], font: ImageFont.ImageFont, tracking: int) -> None:
    x, y = xy
    for letter in TEXT:
        draw.text((x, y), letter, font=font, fill=(246, 248, 251, 255))
        box = draw.textbbox((0, 0), letter, font=font)
        x += box[2] - box[0] + tracking


def tracked_text_bounds(font: ImageFont.ImageFont, tracking: int) -> tuple[int, int, int, int]:
    mask = Image.new("L", (CANVAS, CANVAS), 0)
    draw = ImageDraw.Draw(mask)
    x = CANVAS // 4
    y = CANVAS // 4
    for letter in TEXT:
        draw.text((x, y), letter, font=font, fill=255)
        box = draw.textbbox((0, 0), letter, font=font)
        x += box[2] - box[0] + tracking
    bounds = mask.getbbox()
    if bounds is None:
        return (0, 0, 0, 0)
    left, top, right, bottom = bounds
    return (left - CANVAS // 4, top - CANVAS // 4, right - CANVAS // 4, bottom - CANVAS // 4)


def main() -> None:
    image = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)

    margin = 56 * SCALE
    radius = 196 * SCALE
    rect = (margin, margin, CANVAS - margin, CANVAS - margin)
    draw.rounded_rectangle(rect, radius=radius, fill=(5, 5, 6, 255))
    draw.rounded_rectangle(rect, radius=radius, outline=(38, 40, 46, 255), width=5 * SCALE)

    font = load_font(258 * SCALE)
    tracking = 14 * SCALE
    width = text_width(draw, font, tracking)
    left, top, right, bottom = tracked_text_bounds(font, tracking)
    x = (CANVAS - width) // 2 - left
    y = (CANVAS - (bottom - top)) // 2 - top

    shadow_offset = 5 * SCALE
    for alpha, offset in ((52, shadow_offset * 2), (82, shadow_offset)):
        sx = x + offset
        sy = y + offset
        for letter in TEXT:
            draw.text((sx, sy), letter, font=font, fill=(0, 0, 0, alpha))
            letter_box = draw.textbbox((0, 0), letter, font=font)
            sx += letter_box[2] - letter_box[0] + tracking

    draw_tracked_text(draw, (x, y), font, tracking)

    image = image.resize((SIZE, SIZE), Image.Resampling.LANCZOS)
    for path in (ROOT / "build" / "appicon.png", ROOT / "desktop" / "appicon.png"):
        path.parent.mkdir(parents=True, exist_ok=True)
        image.save(path)


if __name__ == "__main__":
    main()
