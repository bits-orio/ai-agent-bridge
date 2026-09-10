"""Headless Factorio test rig for the AI Agent Bridge e2e harness.

Mirrors ~/factorio-dev/rig (start-server.sh, stop-server.sh, rcon.py,
server-settings.json) exactly: own write-data dir per instance holding
config.ini, --server-settings with require_user_verification false,
--map-gen-settings from the headless install's own data dir,
--start-server-load-scenario base/freeplay, wait for "Starting RCON" in the
log. Reimplemented here (not imported from ~/factorio-dev/rig, which is
outside this repo) so the harness is self-contained.

Nothing here is Factorio-version-specific beyond the mod-list shape, which
copies the one already proven to load the companion mod cleanly (TESTING.md
"1.7 the companion's own rpc command, first load"): base, elevated-rails,
quality and space-age all enabled, alongside whatever mods this harness
links in.

Python 3 standard library only.
"""

from __future__ import annotations

import json
import os
import socket
import struct
import subprocess
import time
from pathlib import Path
from typing import List, NamedTuple, Optional

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_RUN_ROOT = REPO_ROOT / "tests" / "e2e" / ".run"

DEFAULT_GAME_PORT = 34210
DEFAULT_RCON_PORT = 27110
DEFAULT_RCON_PASSWORD = "rig"

# Proven-working companion mod-list (TESTING.md 1.7): every mod bundled with
# a Space-Age-capable install, enabled regardless of whether the harness
# actually uses it, plus whatever this run links in.
BASE_MOD_LIST = ["base", "elevated-rails", "quality", "space-age"]


def default_headless_bin() -> str:
    return os.environ.get(
        "FACTORIO_HEADLESS",
        str(Path.home() / "factorio-dev/headless-2.0.77/factorio/bin/x64/factorio"),
    )


def default_client_bin() -> str:
    return os.environ.get("FACTORIO_CLIENT", str(Path.home() / "factorio/bin/x64/factorio"))


class ModLink(NamedTuple):
    name: str
    version: str
    source: Path


def _read_info_json(info_path: Path) -> Optional[ModLink]:
    if not info_path.is_file():
        return None
    info = json.loads(info_path.read_text(encoding="utf-8"))
    return ModLink(name=info["name"], version=info["version"], source=info_path.parent)


def companion_mod() -> ModLink:
    """The mod this harness always links in. Raises if companion-mod/info.json
    is missing -- there is no meaningful e2e run without it."""
    link = _read_info_json(REPO_ROOT / "companion-mod" / "info.json")
    if link is None:
        raise FileNotFoundError("companion-mod/info.json not found; is this repo checked out at REPO_ROOT?")
    return link


def provider_mod() -> Optional[ModLink]:
    """tests/provider-mod/aab-test-provider, if it exists yet. Owned by the
    companion side of this project, not this harness; scenarios that need it
    are skipped (not failed) when it's absent."""
    d = REPO_ROOT / "tests" / "provider-mod"
    if not d.is_dir():
        return None
    for child in sorted(d.iterdir()):
        link = _read_info_json(child / "info.json")
        if link is not None:
            return link
    return None


def ensure_mods_dir(mods_dir: Path, mods: List[ModLink]) -> Path:
    """(Re)builds a Factorio --mod-directory at mods_dir: a symlink per mod
    named "<name>_<version>" (the convention companion-mod/link-mod.sh and
    every mods-* dir under ~/factorio-dev/rig use) plus a mod-list.json
    enabling BASE_MOD_LIST and every name in `mods`. Stale symlinks for a
    name no longer in `mods` are removed first."""
    mods_dir.mkdir(parents=True, exist_ok=True)

    wanted = {m.name: m for m in mods}
    for entry in mods_dir.iterdir():
        if not entry.is_symlink():
            continue
        stem = entry.name.rsplit("_", 1)[0]
        if stem not in wanted or entry.name != f"{wanted[stem].name}_{wanted[stem].version}":
            entry.unlink()

    for m in mods:
        link = mods_dir / f"{m.name}_{m.version}"
        if not link.exists():
            link.symlink_to(m.source)

    names = list(BASE_MOD_LIST) + [m.name for m in mods]
    mod_list = {"mods": [{"name": n, "enabled": True} for n in names]}
    (mods_dir / "mod-list.json").write_text(json.dumps(mod_list, indent=2) + "\n", encoding="utf-8")
    return mods_dir


# The chat trigger the harness runs with. A runtime-global mod setting cannot
# be written from a console command over RCON: the engine answers "Settings can
# only be changed by the owning player or the mod that made the setting", and
# the Lua docs exempt only *player* settings from that when the console is used
# (measured on 2.0.77, 2026-09-10). So the value has to be in place before the
# map is created, which is what mod-settings.dat is for.
CHAT_PREFIX = "!ask "


def binary_version(binary: Path) -> tuple:
    """(main, major, minor) of a Factorio binary, from its own --version line
    ("Version: 2.0.77 (build ...)"). Falls back to 2.0.0 if that line ever
    changes shape, which only affects the header mod-settings.dat is stamped
    with."""
    try:
        out = subprocess.run([str(binary), "--version"], capture_output=True, text=True, timeout=60).stdout
        first = out.splitlines()[0]
        parts = first.split("Version:", 1)[1].split("(", 1)[0].strip().split(".")
        return tuple(int(x) for x in parts[:3])
    except Exception:
        return (2, 0, 0)


def _pt_uint(n: int) -> bytes:
    """Factorio's space-optimised uint32: one byte, or 0xff plus four."""
    if n < 255:
        return bytes([n])
    return b"\xff" + struct.pack("<I", n)


def _pt_string(s: str) -> bytes:
    b = s.encode("utf-8")
    if not b:
        return b"\x01"  # the "empty" flag, and nothing after it
    return b"\x00" + _pt_uint(len(b)) + b


def _pt(value) -> bytes:
    """One PropertyTree node: a type byte, an any-type flag, then the payload.
    Types as read off a real 2.0.77 mod-settings.dat: 1 bool, 2 double,
    3 string, 5 dictionary, 6 signed integer."""
    if isinstance(value, bool):
        return b"\x01\x00" + (b"\x01" if value else b"\x00")
    if isinstance(value, int):
        return b"\x06\x00" + struct.pack("<q", value)
    if isinstance(value, float):
        return b"\x02\x00" + struct.pack("<d", value)
    if isinstance(value, str):
        return b"\x03\x00" + _pt_string(value)
    if isinstance(value, dict):
        out = b"\x05\x00" + struct.pack("<I", len(value))
        for key, item in value.items():
            out += _pt_string(key) + _pt(item)
        return out
    raise TypeError("no PropertyTree encoding for %r" % (value,))


def write_mod_settings(mods_dir: Path, runtime_global: dict, version: tuple = (2, 0, 0)) -> Path:
    """Writes mods_dir/mod-settings.dat with runtime_global's {name: value}
    pairs, the file a fresh map takes its runtime-global mod settings from.
    Startup and per-user sections are written empty; every setting not named
    here keeps its prototype default."""
    main, major, minor = (list(version) + [0, 0, 0])[:3]
    tree = {
        "startup": {},
        "runtime-global": {name: {"value": value} for name, value in runtime_global.items()},
        "runtime-per-user": {},
    }
    path = mods_dir / "mod-settings.dat"
    path.write_bytes(struct.pack("<HHHH", main, major, minor, 0) + b"\x00" + _pt(tree))
    return path


def _write_config_ini(write_data: Path) -> Path:
    path = write_data / "config.ini"
    path.write_text(
        "[path]\nread-data=__PATH__executable__/../../data\nwrite-data=%s\n" % write_data,
        encoding="utf-8",
    )
    return path


def _write_server_settings(path: Path, name: str) -> Path:
    # Same shape as ~/factorio-dev/rig/server-settings.json, trimmed to the
    # fields that matter for an unattended local test server: no public
    # listing, no account needed to join (require_user_verification false),
    # RCON does its own auth regardless of allow_commands.
    settings = {
        "name": name,
        "description": "AI Agent Bridge e2e harness",
        "tags": ["ai-agent-bridge", "e2e"],
        "max_players": 0,
        "visibility": {"public": False, "lan": False},
        "username": "",
        "password": "",
        "token": "",
        "game_password": "",
        "require_user_verification": False,
        "max_upload_in_kilobytes_per_second": 0,
        "max_upload_slots": 5,
        "minimum_latency_in_ticks": 0,
        "max_heartbeats_per_second": 60,
        "ignore_player_limit_for_returning_players": False,
        "allow_commands": "admins-only",
        "autosave_interval": 0,
        "autosave_slots": 1,
        "afk_autokick_interval": 0,
        "auto_pause": False,
        "auto_pause_when_players_connect": False,
        "only_admins_can_pause_the_game": True,
        "autosave_only_on_server": True,
        "non_blocking_saving": False,
    }
    path.write_text(json.dumps(settings, indent=1) + "\n", encoding="utf-8")
    return path


def _map_gen_settings_path(headless_bin: Path) -> Path:
    # headless_bin is .../factorio/bin/x64/factorio; the install root is
    # three levels up (matches ~/factorio-dev/rig/start-server.sh).
    install_root = headless_bin.parent.parent.parent
    return install_root / "data" / "map-gen-settings.example.json"


class LogTimeout(RuntimeError):
    pass


class LogError(RuntimeError):
    pass


def _wait_for_log(log_path: Path, needle: str, timeout: float, proc: Optional[subprocess.Popen] = None) -> None:
    """Polls log_path for `needle`, mirroring start-server.sh's own wait loop.
    Also fails fast if the process exited, or an " Error " line appears
    first (the pattern start-server.sh greps for)."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if proc is not None and proc.poll() is not None:
            text = log_path.read_text(encoding="utf-8", errors="replace") if log_path.exists() else ""
            raise LogError("process exited (code %s) before %r appeared in %s\n%s" % (proc.returncode, needle, log_path, text[-2000:]))
        if log_path.exists():
            text = log_path.read_text(encoding="utf-8", errors="replace")
            if needle in text:
                return
            if " Error " in text:
                raise LogError("error in %s:\n%s" % (log_path, text[-2000:]))
        time.sleep(1)
    raise LogTimeout("timed out after %ss waiting for %r in %s" % (timeout, needle, log_path))


def _stop_process(proc: Optional[subprocess.Popen], timeout: float) -> None:
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=timeout)


class RCON:
    """Minimal Source-RCON client for Factorio, mirroring
    ~/factorio-dev/rig/rcon.py: sends a warmup command right after auth
    because the first command on a fresh RCON session is swallowed by the
    achievements prompt."""

    SERVERDATA_AUTH = 3
    SERVERDATA_EXECCOMMAND = 2

    def __init__(self, port: int, password: str, host: str = "127.0.0.1"):
        self.host = host
        self.port = port
        self.password = password
        self._sock: Optional[socket.socket] = None
        self._next_id = 10

    def __enter__(self) -> "RCON":
        self.connect()
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def connect(self, timeout: float = 30) -> None:
        self._sock = socket.create_connection((self.host, self.port), timeout=timeout)
        self._send(1, self.SERVERDATA_AUTH, self.password)
        rid, _typ, _body = self._recv()
        if rid == -1:
            self.close()
            raise ConnectionRefusedError("RCON auth failed on %s:%s" % (self.host, self.port))
        self.command('/sc rcon.print("warmup")')

    def close(self) -> None:
        if self._sock is not None:
            self._sock.close()
            self._sock = None

    def command(self, cmd: str) -> str:
        if self._sock is None:
            raise RuntimeError("RCON not connected; call connect() first")
        rid = self._next_id
        self._next_id += 1
        self._send(rid, self.SERVERDATA_EXECCOMMAND, cmd)
        _rid, _typ, body = self._recv()
        return body

    def _send(self, rid: int, typ: int, body: str) -> None:
        payload = struct.pack("<ii", rid, typ) + body.encode("utf-8") + b"\x00\x00"
        self._sock.sendall(struct.pack("<i", len(payload)) + payload)

    def _recv(self):
        raw = b""
        while len(raw) < 4:
            chunk = self._sock.recv(4 - len(raw))
            if not chunk:
                raise ConnectionError("RCON connection closed while reading length")
            raw += chunk
        (length,) = struct.unpack("<i", raw)
        data = b""
        while len(data) < length:
            chunk = self._sock.recv(length - len(data))
            if not chunk:
                raise ConnectionError("RCON connection closed mid-packet")
            data += chunk
        rid, typ = struct.unpack("<ii", data[:8])
        return rid, typ, data[8:-2].decode("utf-8", errors="replace")


class Server:
    """A headless Factorio server with its own write-data dir and RCON,
    started fresh on base/freeplay every time (no save file, deterministic
    starting state for a test run)."""

    def __init__(
        self,
        name: str = "server",
        run_root: Optional[Path] = None,
        headless_bin: Optional[str] = None,
        game_port: int = DEFAULT_GAME_PORT,
        rcon_port: int = DEFAULT_RCON_PORT,
        rcon_password: str = DEFAULT_RCON_PASSWORD,
        mods_dir: Optional[Path] = None,
    ):
        self.name = name
        self.run_root = Path(run_root) if run_root else DEFAULT_RUN_ROOT
        self.headless_bin = Path(headless_bin or default_headless_bin())
        self.game_port = game_port
        self.rcon_port = rcon_port
        self.rcon_password = rcon_password
        self.mods_dir = Path(mods_dir) if mods_dir else self.run_root / "mods"
        self.write_data = self.run_root / ("w-" + name)
        self.log_path = self.run_root / (name + ".log")
        self.proc: Optional[subprocess.Popen] = None
        self._log_file = None

    @property
    def events_file(self) -> Path:
        # helpers.write_file in the companion writes under script-output/ of
        # the active write-data dir (docs/design/phase1-2-spec.md, the
        # aab.yaml.example events_file comment).
        return self.write_data / "script-output" / "ai-agent-bridge" / "events.jsonl"

    def start(self, timeout: float = 90) -> None:
        if not self.headless_bin.is_file():
            raise FileNotFoundError(
                "FACTORIO_HEADLESS binary not found at %s (set FACTORIO_HEADLESS to override)" % self.headless_bin
            )
        self.run_root.mkdir(parents=True, exist_ok=True)
        self.write_data.mkdir(parents=True, exist_ok=True)
        (self.write_data / "saves").mkdir(parents=True, exist_ok=True)
        _write_config_ini(self.write_data)

        settings_path = self.run_root / ("%s-server-settings.json" % self.name)
        _write_server_settings(settings_path, name="aab-e2e-%s" % self.name)
        map_gen = _map_gen_settings_path(self.headless_bin)

        cmd = [
            str(self.headless_bin),
            "--config", str(self.write_data / "config.ini"),
            "--start-server-load-scenario", "base/freeplay",
            "--server-settings", str(settings_path),
            "--port", str(self.game_port),
            "--rcon-port", str(self.rcon_port),
            "--rcon-password", self.rcon_password,
            "--mod-directory", str(self.mods_dir),
            "--map-gen-settings", str(map_gen),
        ]
        self._log_file = open(self.log_path, "w", encoding="utf-8")
        self.proc = subprocess.Popen(cmd, stdout=self._log_file, stderr=subprocess.STDOUT)
        _wait_for_log(self.log_path, "Starting RCON", timeout=timeout, proc=self.proc)

    def stop(self, timeout: float = 20) -> None:
        _stop_process(self.proc, timeout)
        self.proc = None
        if self._log_file is not None:
            self._log_file.close()
            self._log_file = None

    def rcon(self) -> RCON:
        c = RCON(self.rcon_port, self.rcon_password)
        c.connect()
        return c


class Client:
    """An optional standalone graphical client, connecting as a real player
    (needed by the scenarios that simulate chat or a player death). Needs a
    display; there is no headless client mode in Factorio."""

    def __init__(
        self,
        server: Server,
        name: str = "client",
        run_root: Optional[Path] = None,
        client_bin: Optional[str] = None,
        mods_dir: Optional[Path] = None,
    ):
        self.server = server
        self.name = name
        self.run_root = Path(run_root) if run_root else DEFAULT_RUN_ROOT
        self.client_bin = Path(client_bin or default_client_bin())
        self.mods_dir = Path(mods_dir) if mods_dir else self.run_root / "mods"
        self.write_data = self.run_root / ("w-" + name)
        self.log_path = self.run_root / (name + ".log")
        self.proc: Optional[subprocess.Popen] = None
        self._log_file = None

    def start(self, timeout: float = 90) -> None:
        if not self.client_bin.is_file():
            raise FileNotFoundError(
                "FACTORIO_CLIENT binary not found at %s (set FACTORIO_CLIENT to override)" % self.client_bin
            )
        self.run_root.mkdir(parents=True, exist_ok=True)
        self.write_data.mkdir(parents=True, exist_ok=True)
        _write_config_ini(self.write_data)

        cmd = [
            str(self.client_bin),
            "--config", str(self.write_data / "config.ini"),
            "--mod-directory", str(self.mods_dir),
            "--mp-connect", "127.0.0.1:%d" % self.server.game_port,
        ]
        self._log_file = open(self.log_path, "w", encoding="utf-8")
        self.proc = subprocess.Popen(cmd, stdout=self._log_file, stderr=subprocess.STDOUT)
        # "to(InGame)" is the client's own multiplayer state-machine reaching
        # the playable state (verified against a real client-vs-rig log).
        _wait_for_log(self.log_path, "to(InGame)", timeout=timeout, proc=self.proc)

    def stop(self, timeout: float = 20) -> None:
        _stop_process(self.proc, timeout)
        self.proc = None
        if self._log_file is not None:
            self._log_file.close()
            self._log_file = None
