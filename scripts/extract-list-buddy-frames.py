#!/usr/bin/env python3

from pathlib import Path
import shutil

import numpy as np
from PIL import Image


SOURCE_ROOT = Path.home() / ".codex" / "pets"
OUTPUT_ROOT = Path(__file__).resolve().parents[1] / "assets" / "pets"
PETS = ("qingzhu", "xiaojiaqi")
CELL_WIDTH, CELL_HEIGHT = 192, 208
OUTPUT_WIDTH, OUTPUT_HEIGHT = 80, 96
PET_WIDTH, PET_HEIGHT = 80, 96


def pixel_value(pixels: np.ndarray) -> np.ndarray:
    red, green, blue = pixels[..., 0], pixels[..., 1], pixels[..., 2]
    luminance = 0.299 * red + 0.587 * green + 0.114 * blue
    return np.maximum(0, luminance - 1.2 * np.maximum(0, green - np.maximum(red, blue)))


def tone_profile(atlas: Image.Image) -> tuple[float, float]:
    preview = atlas.copy()
    preview.thumbnail((1024, 1024), Image.Resampling.LANCZOS)
    pixels = np.asarray(preview, dtype=np.float32)
    values = pixel_value(pixels)
    visible = values[pixels[..., 3] > 16]
    return tuple(np.percentile(visible, (2, 98)))


def prepare_frame(cell: Image.Image, profile: tuple[float, float]) -> Image.Image:
    # Render the original character by itself at the stable hardware size.
    sprite = cell.resize((PET_WIDTH, PET_HEIGHT), Image.Resampling.LANCZOS)
    frame = Image.new("RGBA", (OUTPUT_WIDTH, OUTPUT_HEIGHT))
    frame.alpha_composite(sprite, ((OUTPUT_WIDTH - PET_WIDTH) // 2, 0))
    pixels = np.asarray(frame, dtype=np.float32)
    alpha = pixels[..., 3] / 255
    tone = pixel_value(pixels) * alpha
    black_point, white_point = profile
    tone = np.clip((tone - black_point) / max(1, white_point - black_point), 0, 1)
    tone = np.power(tone, 1.3)
    tone = np.clip((tone - 0.5) * 1.05 + 0.5, 0, 1) * 255

    # EvenPet's 3x3 local-contrast pass keeps hair, facial features and folds
    # distinct after 16-level quantization.
    padded = np.pad(tone, 1, mode="edge")
    blurred = sum(
        padded[y:y + OUTPUT_HEIGHT, x:x + OUTPUT_WIDTH]
        for y in range(3) for x in range(3)
    ) / 9
    left = np.concatenate((blurred[:, :1], blurred[:, :-1]), axis=1)
    right = np.concatenate((blurred[:, 1:], blurred[:, -1:]), axis=1)
    top = np.concatenate((blurred[:1, :], blurred[:-1, :]), axis=0)
    bottom = np.concatenate((blurred[1:, :], blurred[-1:, :]), axis=0)
    edge = np.abs(right - left) + np.abs(bottom - top)
    enhanced = np.clip(tone - 0.6 * np.minimum(edge, 130), 0, 255)
    quantized = np.round(enhanced / 17) * 17
    result = Image.fromarray(quantized.astype(np.uint8))
    return result


for pet in PETS:
    source_dir = SOURCE_ROOT / pet
    output_dir = OUTPUT_ROOT / pet
    frames_dir = output_dir / "frames"
    frames_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source_dir / "pet.json", output_dir / "pet.json")
    shutil.copy2(source_dir / "spritesheet.webp", output_dir / "spritesheet.webp")

    atlas = Image.open(source_dir / "spritesheet.webp").convert("RGBA")
    if atlas.size != (CELL_WIDTH * 8, CELL_HEIGHT * 11):
        raise ValueError(f"unexpected v2 atlas size for {pet}: {atlas.size}")
    profile = tone_profile(atlas)
    idle_cells = [
        index for index in range(8)
        if atlas.crop((index * CELL_WIDTH, 0, (index + 1) * CELL_WIDTH, CELL_HEIGHT)).getchannel("A").getbbox()
    ]
    if len(idle_cells) < 3:
        raise ValueError(f"not enough idle frames for {pet}: {idle_cells}")
    frame_indices = (idle_cells[0], idle_cells[len(idle_cells) // 2], idle_cells[-1])
    for output_index, source_index in enumerate(frame_indices):
        box = (source_index * CELL_WIDTH, 0, (source_index + 1) * CELL_WIDTH, CELL_HEIGHT)
        prepare_frame(atlas.crop(box), profile).save(frames_dir / f"{output_index}.png")
