"""Runs a real save on a private headless server, for checks against real data.

The e2e harness (tests/e2e) starts a fresh freeplay map, which has one force,
one surface and no factory. Most tool bugs only show on a played map: many
forces, platforms, other planets, modded item names. This rig loads a save
file you give it, with the mods that save was made with, and the companion
from this checkout (or from a git ref, for a before/after comparison).

Nothing here touches your own game data. The save and the mod zips are only
read; the server gets its own write-data folder and mod directory under
tests/realsave/.run/, and its own ports.

Python 3 standard library only.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import zipfile
import zlib
from pathlib import Path
from typing import Dict, List, Optional, Tuple

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "e2e"))
import rig  # noqa: E402  (tests/e2e/rig.py: RCON client, config and log helpers)

REPO_ROOT = rig.REPO_ROOT
RUN_ROOT = REPO_ROOT / "tests" / "realsave" / ".run"

GAME_PORT = 34220
RCON_PORT = 27120
RCON_PASSWORD = "realsave"

# Mods that ship inside the game's own data folder, never as a zip.
BUILT_IN = {"base", "core", "elevated-rails", "quality", "recycler", "space-age"}
COMPANION = "ai-agent-bridge"


def save_header(save: Path) -> bytes:
    """The start of the save's level.dat0, inflated: version and mod list."""
    with zipfile.ZipFile(save) as z:
        name = next(n for n in z.namelist() if n.endswith("/level.dat0"))
        raw = z.read(name)
    try:
        return zlib.decompress(raw)
    except zlib.error:
        return raw


def save_version(header: bytes) -> Tuple[int, int, int]:
    main, major, minor = (int.from_bytes(header[i:i + 2], "little") for i in (0, 2, 4))
    return main, major, minor


def save_mods(header: bytes) -> Dict[str, str]:
    """{mod name: version} as the save recorded them. The list is a count
    byte, then per mod: a length-prefixed name, three version bytes and a
    four-byte checksum. It starts at the entry for base."""
    for match in re.finditer(rb"\x04base", header[:4096]):
        start = match.start()
        count = header[start - 1]
        if count < 1 or count > 250:
            continue
        mods, at = {}, start
        try:
            for _ in range(count):
                length = header[at]
                name = header[at + 1:at + 1 + length].decode("ascii")
                if not re.fullmatch(r"[A-Za-z0-9_\- ]{1,100}", name):
                    raise ValueError(name)
                at += 1 + length
                mods[name] = "%d.%d.%d" % tuple(header[at:at + 3])
                at += 3 + 4
        except (ValueError, IndexError, UnicodeDecodeError):
            continue
        if len(mods) == count:
            return mods
    raise ValueError("could not read the mod list from the save header")


def _version_key(version: str) -> Tuple[int, ...]:
    return tuple(int(p) for p in version.split("."))


def find_mod_zip(mods_from: Path, name: str, wanted: str) -> Optional[Path]:
    """The zip for `name` in mods_from: the save's own version when it is
    there, else the newest one."""
    found = {}
    for path in mods_from.glob(name + "_*.zip"):
        version = path.stem[len(name) + 1:]
        if re.fullmatch(r"\d+\.\d+\.\d+", version):
            found[version] = path
    if not found:
        return None
    if wanted in found:
        return found[wanted]
    return found[max(found, key=_version_key)]


def export_companion(ref: str, dest: Path) -> Path:
    """The companion as it is at a git ref, unpacked under dest. Used for the
    "before" half of a before/after run, so the working tree can keep
    changing underneath."""
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    archive = subprocess.run(
        ["git", "-C", str(REPO_ROOT), "archive", ref, "companion-mod"],
        check=True, capture_output=True,
    ).stdout
    subprocess.run(["tar", "-x", "-C", str(dest)], input=archive, check=True)
    return dest / "companion-mod"


def stage_mods(save: Path, mods_from: Path, companion_dir: Path, mods_dir: Path) -> List[str]:
    """Builds mods_dir for this save: a link per mod zip the save names, the
    companion from companion_dir, a mod-list enabling exactly the save's
    mods, and the mod settings the zips were used with. Returns the notes a
    run should print (missing mods, version differences)."""
    notes: List[str] = []
    wanted = save_mods(save_header(save))
    if mods_dir.exists():
        shutil.rmtree(mods_dir)
    mods_dir.mkdir(parents=True)

    enabled = []
    for name, version in sorted(wanted.items()):
        if name in BUILT_IN:
            enabled.append(name)
            continue
        if name == COMPANION:
            continue
        zip_path = find_mod_zip(mods_from, name, version)
        if zip_path is None:
            notes.append("mod %s %s is not in %s: left out" % (name, version, mods_from))
            continue
        if not zip_path.stem.endswith("_" + version):
            notes.append("mod %s: save has %s, using %s" % (name, version, zip_path.stem.rsplit("_", 1)[1]))
        (mods_dir / zip_path.name).symlink_to(zip_path)
        enabled.append(name)

    info = json.loads((companion_dir / "info.json").read_text(encoding="utf-8"))
    (mods_dir / ("%s_%s" % (COMPANION, info["version"]))).symlink_to(companion_dir)
    enabled.append(COMPANION)
    if COMPANION in wanted and wanted[COMPANION] != info["version"]:
        notes.append("companion: save has %s, loading %s" % (wanted[COMPANION], info["version"]))

    mod_list = {"mods": [{"name": n, "enabled": True} for n in enabled]}
    (mods_dir / "mod-list.json").write_text(json.dumps(mod_list, indent=2) + "\n", encoding="utf-8")
    settings = mods_from / "mod-settings.dat"
    if settings.is_file():
        shutil.copyfile(settings, mods_dir / "mod-settings.dat")
    else:
        notes.append("no mod-settings.dat in %s: startup settings are the defaults" % mods_from)
    return notes


class SaveServer(rig.Server):
    """A headless server that loads one save file. Space Age needs the full
    game's data folder, so `binary` is a full install, run with
    --start-server; a headless-only download has no expansion data."""

    def __init__(self, binary: str, save: Path, mods_dir: Path, run_root: Path = RUN_ROOT):
        super().__init__(
            name="server", run_root=run_root, headless_bin=binary,
            game_port=GAME_PORT, rcon_port=RCON_PORT, rcon_password=RCON_PASSWORD, mods_dir=mods_dir,
        )
        self.save = Path(save)

    def start(self, timeout: float = 420) -> None:
        if not self.headless_bin.is_file():
            raise FileNotFoundError("Factorio binary not found at %s" % self.headless_bin)
        self.write_data.mkdir(parents=True, exist_ok=True)
        saves = self.write_data / "saves"
        saves.mkdir(parents=True, exist_ok=True)
        # A private copy: the server autosaves over what it loaded.
        local_save = saves / self.save.name
        shutil.copyfile(self.save, local_save)
        events = self.events_file
        if events.exists():
            events.unlink()
        rig._write_config_ini(self.write_data)
        settings_path = self.run_root / "server-settings.json"
        rig._write_server_settings(settings_path, name="aab-realsave")

        cmd = [
            str(self.headless_bin),
            "--config", str(self.write_data / "config.ini"),
            "--start-server", str(local_save),
            "--server-settings", str(settings_path),
            "--port", str(self.game_port),
            "--rcon-port", str(self.rcon_port),
            "--rcon-password", self.rcon_password,
            "--mod-directory", str(self.mods_dir),
        ]
        self._log_file = open(self.log_path, "w", encoding="utf-8")
        env = dict(os.environ)
        # A build copied out of a Steam library must not reach for the Steam
        # client: the owner may be playing their own game through it.
        for key in ("SteamAppId", "SteamGameId", "SteamOverlayGameId"):
            env.pop(key, None)
        self.proc = subprocess.Popen(
            cmd, stdout=self._log_file, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, env=env,
        )
        rig._wait_for_log(self.log_path, "Starting RCON", timeout=timeout, proc=self.proc)

    def log_errors(self) -> List[str]:
        """Lua and engine error lines in the server log so far."""
        if not self.log_path.exists():
            return []
        text = self.log_path.read_text(encoding="utf-8", errors="replace")
        bad = []
        for line in text.splitlines():
            if "InterruptibleStdioStream" in line:
                continue
            if " Error " in line or "stack traceback" in line or "Failed to load mods" in line:
                bad.append(line)
        return bad
