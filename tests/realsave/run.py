#!/usr/bin/env python3
"""Loads a real save on a private server and runs tests/realsave/probes.py.

    python3 tests/realsave/run.py --save ~/saves/my-map.zip
    python3 tests/realsave/run.py --save my-map.zip --companion HEAD --label before
    python3 tests/realsave/run.py --save my-map.zip --label after
    python3 tests/realsave/run.py --compare before after

Exit code: 0 when no probe failed and the server log holds no Lua or engine
error, 1 otherwise. A probe with nothing to check on this save is a SKIP.
With --expect-failures the run only records, for the "before" half of a
comparison, and exits 0 unless the server itself failed.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import probes  # noqa: E402
import saverig  # noqa: E402


def default_binary() -> str:
    return os.environ.get("FACTORIO_FULL", str(Path.home() / "factorio-2.1/bin/x64/factorio"))


def report_path(label: str) -> Path:
    return saverig.RUN_ROOT / ("report-%s.json" % label)


def run(args) -> int:
    save = Path(args.save).expanduser().resolve()
    if not save.is_file():
        print("save not found: %s" % save)
        return 1
    if args.companion == "worktree":
        companion = saverig.REPO_ROOT / "companion-mod"
    else:
        companion = saverig.export_companion(args.companion, saverig.RUN_ROOT / ("companion-" + args.label))
    mods_dir = saverig.RUN_ROOT / "mods"
    for note in saverig.stage_mods(save, Path(args.mods_from).expanduser(), companion, mods_dir):
        print("note: " + note)

    server = saverig.SaveServer(args.factorio, save, mods_dir)
    results, crashed = [], None
    try:
        server.start()
        rcon = server.rcon()
        game = probes.Game(rcon)
        status = game.rpc("status")
        print("companion %s on %s, tick %s, %d forces with players, %d surfaces" % (
            status["r"].get("mod_version"), save.name, status["r"].get("tick"),
            len(game.forces()), len(game.surfaces())))
        wanted = set(args.only.split(",")) if args.only else None
        for finding, title, fn in probes.PROBES:
            if wanted and finding not in wanted:
                continue
            try:
                ok, detail, data = fn(game)
            except Exception as exc:  # a probe that cannot run is a failure, not a crash of the run
                ok, detail, data = False, "probe raised %s: %s" % (type(exc).__name__, str(exc)[:300]), None
            word = "SKIP" if ok is None else ("PASS" if ok else "FAIL")
            print("%-4s %-7s %s: %s" % (word, finding, title, detail))
            results.append({"finding": finding, "title": title, "status": word, "detail": detail, "data": data})
        rcon.close()
    except Exception as exc:
        crashed = "%s: %s" % (type(exc).__name__, str(exc)[-1500:])
        print("server run failed: " + crashed)
    finally:
        server.stop()

    errors = server.log_errors()
    for line in errors[:20]:
        print("server log: " + line)
    failed = [r for r in results if r["status"] == "FAIL"]
    summary = {
        "label": args.label, "save": save.name, "companion": args.companion,
        "results": results, "log_errors": errors, "crashed": crashed,
    }
    report_path(args.label).write_text(json.dumps(summary, indent=1) + "\n", encoding="utf-8")
    print("%d passed, %d failed, %d skipped; report in %s" % (
        sum(r["status"] == "PASS" for r in results), len(failed),
        sum(r["status"] == "SKIP" for r in results), report_path(args.label)))
    if crashed or errors:
        return 1
    if failed and not args.expect_failures:
        return 1
    return 0


def compare(a: str, b: str) -> int:
    left = {r["title"]: r for r in json.loads(report_path(a).read_text())["results"]}
    right = {r["title"]: r for r in json.loads(report_path(b).read_text())["results"]}
    worse = 0
    print("%-8s %-6s %-6s %s" % ("finding", a[:6], b[:6], "check"))
    for title in list(left) + [t for t in right if t not in left]:
        l, r = left.get(title), right.get(title)
        ls, rs = (l or {}).get("status", "-"), (r or {}).get("status", "-")
        if ls == "PASS" and rs == "FAIL":
            worse += 1
        print("%-8s %-6s %-6s %s" % ((l or r)["finding"], ls, rs, title))
        if ls != rs or rs == "FAIL":
            print("           %s: %s" % (a, (l or {}).get("detail", "")))
            print("           %s: %s" % (b, (r or {}).get("detail", "")))
    return 1 if worse else 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("--save", help="the save file (.zip) to load; it is copied, never changed")
    p.add_argument("--mods-from", default=str(Path.home() / ".factorio/mods"),
                   help="folder holding the mod zips the save was made with (read only)")
    p.add_argument("--factorio", default=default_binary(),
                   help="a full Factorio install of the save's version (env FACTORIO_FULL)")
    p.add_argument("--companion", default="worktree", help="'worktree', or a git ref such as HEAD")
    p.add_argument("--label", default="run", help="name of the report file")
    p.add_argument("--only", help="comma-separated finding ids to run")
    p.add_argument("--expect-failures", action="store_true", help="record only; failed probes do not fail the run")
    p.add_argument("--compare", nargs=2, metavar=("A", "B"), help="compare two saved reports")
    args = p.parse_args()
    if args.compare:
        return compare(*args.compare)
    if not args.save:
        p.error("--save is required")
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
