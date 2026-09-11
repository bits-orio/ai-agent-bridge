#!/usr/bin/env python3
"""Generate companion-mod/thumbnail.png, the mod-portal card.

Same card as the rest of the family (multi-team-support, brave-new-mts,
open-discord-bridge): a 512 by 512 charcoal ground, the dark inset frame,
big DejaVu Sans Bold initials with a soft black halo beneath them, and a
grey uppercase subtitle near the bottom. Two letters here rather than
three, "AI", each filled with a vertical gradient in the colours people
read as AI, violet for the A and cyan-to-blue for the I, and a sparkle
mark, the four-point star that has come to stand for AI, sitting off the
top right of the I with two smaller ones trailing it.

Run from the repo root:  python3 tools/gen_thumbnail.py
"""

import math
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

# Pillow 9 spells the resampling filter one way and Pillow 10 another.
LANCZOS = getattr(getattr(Image, "Resampling", Image), "LANCZOS")

SIZE = 512
BG = (43, 43, 43)
FRAME = (26, 26, 26)
FRAME_INSET = 16
FRAME_WIDTH = 12

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
LETTER_SIZE = 200          # cap height about 145 px, a touch over the siblings' 138
LETTER_GAP = 14            # transparent space between the trimmed glyphs
LETTER_TOP = 150           # top of the letter band, as on the siblings
LETTER_SHIFT = -28         # the row sits a little left so the sparkles balance it

# Top and bottom of each letter's gradient.
A_GRADIENT = ((216, 180, 254), (124, 58, 237))    # light violet to violet
I_GRADIENT = ((103, 232, 249), (59, 130, 246))    # cyan to blue
SPARK_GRADIENT = ((240, 171, 252), (34, 211, 238))  # pink to cyan, across the star

HALO = (0, 0, 0)
HALO_RADIUS = 8
HALO_STRENGTH = 1.5

SUBTITLE = "AGENT BRIDGE"
SUBTITLE_COLOR = (146, 146, 146)
SUBTITLE_WIDTH = 330
SUBTITLE_TOP = 408


def ground_image():
    img = Image.new("RGBA", (SIZE, SIZE), BG + (255,))
    d = ImageDraw.Draw(img)
    far = SIZE - FRAME_INSET
    d.rounded_rectangle([FRAME_INSET, FRAME_INSET, far, far], radius=6, outline=FRAME, width=FRAME_WIDTH)
    return img


def glyph_mask(text, size):
    """The glyph as an L mask, trimmed to its ink."""
    font = ImageFont.truetype(FONT, size)
    left, top, right, bottom = font.getbbox(text)
    mask = Image.new("L", (right - left + 4, bottom - top + 4), 0)
    ImageDraw.Draw(mask).text((2 - left, 2 - top), text, font=font, fill=255)
    return mask.crop(mask.getbbox())


def gradient(size, top, bottom, vertical=True):
    """An RGBA gradient image, top colour to bottom colour (or left to right)."""
    w, h = size
    img = Image.new("RGBA", size)
    px = img.load()
    span = (h if vertical else w) - 1 or 1
    for y in range(h):
        for x in range(w):
            t = (y if vertical else x) / span
            px[x, y] = tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)) + (255,)
    return img


def coloured(mask, top, bottom, vertical=True):
    """The mask filled with a gradient."""
    fill = gradient(mask.size, top, bottom, vertical)
    fill.putalpha(mask)
    return fill


def letter_row():
    """The two letters, trimmed and kerned, as one RGBA layer."""
    glyphs = [
        coloured(glyph_mask("A", LETTER_SIZE), *A_GRADIENT),
        coloured(glyph_mask("I", LETTER_SIZE), *I_GRADIENT),
    ]
    width = sum(g.width for g in glyphs) + LETTER_GAP * (len(glyphs) - 1)
    height = max(g.height for g in glyphs)
    row = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    x = 0
    for g in glyphs:
        row.alpha_composite(g, (x, height - g.height))
        x += g.width + LETTER_GAP
    return row


def sparkle_mask(radius, waist=0.26):
    """A four-point star: outer points at radius, concave sides pulled in to
    waist times radius on the diagonals. Drawn at 4x and shrunk, for
    smooth edges."""
    scale = 4
    r = radius * scale
    c = r + 2 * scale
    pts = []
    for k in range(8):
        angle = math.pi / 4 * k - math.pi / 2
        rr = r if k % 2 == 0 else r * waist
        pts.append((c + rr * math.cos(angle), c + rr * math.sin(angle)))
    big = Image.new("L", (2 * c, 2 * c), 0)
    ImageDraw.Draw(big).polygon(pts, fill=255)
    return big.resize((2 * c // scale, 2 * c // scale), LANCZOS)


def sparkle(radius):
    mask = sparkle_mask(radius)
    return coloured(mask, *SPARK_GRADIENT, vertical=False)


def halo_under(layer):
    """A blurred black copy of a layer's coverage, to sit beneath it."""
    alpha = layer.getchannel("A").filter(ImageFilter.GaussianBlur(HALO_RADIUS))
    alpha = alpha.point(lambda v: min(255, int(v * HALO_STRENGTH)))
    halo = Image.new("RGBA", layer.size, HALO + (0,))
    halo.putalpha(alpha)
    return halo


def glow_under(layer, colour, radius=10, strength=0.9):
    """A soft coloured glow beneath a sparkle, so it reads as light."""
    alpha = layer.getchannel("A").filter(ImageFilter.GaussianBlur(radius))
    alpha = alpha.point(lambda v: min(255, int(v * strength)))
    glow = Image.new("RGBA", layer.size, colour + (0,))
    glow.putalpha(alpha)
    return glow


def paste_with_halo(canvas, layer, xy, glow=None):
    """Composite a layer at xy with its halo (or glow) beneath it, padding
    the halo so the blur is not clipped at the layer's edge."""
    pad = 3 * HALO_RADIUS
    padded = Image.new("RGBA", (layer.width + 2 * pad, layer.height + 2 * pad), (0, 0, 0, 0))
    padded.alpha_composite(layer, (pad, pad))
    under = glow_under(padded, glow) if glow else halo_under(padded)
    canvas.alpha_composite(under, (xy[0] - pad, xy[1] - pad))
    canvas.alpha_composite(padded, (xy[0] - pad, xy[1] - pad))


def subtitle():
    mask = glyph_mask(SUBTITLE, 60)
    scale = SUBTITLE_WIDTH / mask.width
    mask = mask.resize((SUBTITLE_WIDTH, round(mask.height * scale)), LANCZOS)
    layer = Image.new("RGBA", mask.size, SUBTITLE_COLOR + (0,))
    layer.putalpha(mask)
    return layer


def build():
    canvas = ground_image()

    row = letter_row()
    row_x = (SIZE - row.width) // 2 + LETTER_SHIFT
    paste_with_halo(canvas, row, (row_x, LETTER_TOP))

    # The sparkles: a big one off the I's top right, two smaller ones
    # trailing down and right, the way the mark is usually drawn.
    right = row_x + row.width
    for radius, (dx, dy) in ((44, (8, -34)), (20, (74, 44)), (12, (50, 100))):
        star = sparkle(radius)
        paste_with_halo(canvas, star, (right + dx, LETTER_TOP + dy), glow=SPARK_GRADIENT[1])

    sub = subtitle()
    canvas.alpha_composite(sub, ((SIZE - sub.width) // 2, SUBTITLE_TOP))
    return canvas.convert("RGB")


def main():
    root = Path(__file__).resolve().parent.parent
    out = root / "companion-mod" / "thumbnail.png"
    build().save(out, optimize=True)
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
