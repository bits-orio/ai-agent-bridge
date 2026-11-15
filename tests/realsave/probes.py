"""Checks run against a real save. None of them names a force, surface or item
from one particular save: each discovers what the map has and compares a tool's
answer with an independent read of the same game state through the console.

A probe returns (ok, detail, data). ok is True, False, or None for "nothing on
this save to check that with". data is kept in the report so two runs (before
and after a change) can be compared value by value.

Probes that change the map (they ask questions) come last.
"""

from __future__ import annotations

import json
import time
from typing import Any, Callable, Dict, List, Optional, Tuple

TOOLS = "ai-agent-bridge-tools"
ASK = "ai-agent-bridge-v1"

# The service cuts a tool result at 4096 bytes (agent.DefaultMaxToolResultBytes),
# so a reply has to fit under that to reach the model whole.
SERVICE_CUT = 4096

Result = Tuple[Optional[bool], str, Any]


class Game:
    """One RCON connection, speaking aab-rpc-v1 and the Lua console."""

    def __init__(self, rcon):
        self.rcon = rcon
        self._forces: Optional[List[dict]] = None
        self._surfaces: Optional[List[dict]] = None

    def rpc(self, op: str, **fields) -> dict:
        body = dict(fields, v=1, op=op)
        raw = self.rcon.command("/aab-rpc " + json.dumps(body, separators=(",", ":")))
        try:
            reply = json.loads(raw)
        except ValueError:
            return {"ok": False, "e": "not_json", "m": raw[:300], "_bytes": len(raw)}
        reply["_bytes"] = len(raw)
        return reply

    def call(self, fn: str, iface: str = TOOLS, **args) -> dict:
        return self.rpc("call", i=iface, f=fn, a=args)

    def lua(self, code: str) -> Any:
        """Runs code that returns one value; hands it back decoded."""
        raw = self.rcon.command(
            "/sc local ok, v = pcall(function() %s end) "
            "rcon.print(helpers.table_to_json({ok = ok, v = v}))" % code
        )
        reply = json.loads(raw)
        if not reply.get("ok"):
            raise RuntimeError("console Lua failed: %s" % reply.get("v"))
        return reply.get("v")

    def forces(self) -> List[dict]:
        """Forces that have players, with their player and surface facts."""
        if self._forces is None:
            self._forces = self.lua(
                "local out = {} for _, f in pairs(game.forces) do "
                "if #f.players > 0 and f.name ~= 'spectator' then "
                "out[#out + 1] = {name = f.name, players = #f.players} end end "
                "table.sort(out, function(a, b) return a.name < b.name end) return out"
            ) or []
        return self._forces

    def surfaces(self) -> List[dict]:
        if self._surfaces is None:
            self._surfaces = self.lua(
                "local out = {} for _, s in pairs(game.surfaces) do "
                "out[#out + 1] = {name = s.name, platform = s.platform ~= nil, "
                "planet = s.planet and s.planet.name or nil} end return out"
            ) or []
        return self._surfaces

    def a_force(self) -> str:
        forces = self.forces()
        return forces[0]["name"] if forces else "player"


def _rows(reply: dict) -> List[dict]:
    r = reply.get("r")
    if isinstance(r, dict):
        for key in ("rows", "forces", "surfaces", "players", "items"):
            if isinstance(r.get(key), list):
                return r[key]
    return r if isinstance(r, list) else []


def _sweep_values(reply: dict) -> Dict[str, Any]:
    """{name: value} from a sweep reply, whose rows are positional to cols."""
    out = {}
    for row in _rows(reply):
        if isinstance(row, list) and len(row) >= 2:
            out[row[0]] = row[1]
        elif isinstance(row, dict):
            out[row.get("force") or row.get("name")] = row.get("value")
    return out


def _number(value) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return float("nan")


# ---------------------------------------------------------------- tools


def every_tool_answers(g: Game) -> Result:
    """Each tool with no required parameter runs on this map without a Lua error."""
    manifest = g.rpc("manifest", i=TOOLS)
    if not manifest.get("ok"):
        return False, "manifest op failed: %s" % manifest.get("m"), None
    tools = manifest["r"]["tools"]
    failed, ran = [], 0
    for name in sorted(tools):
        params = tools[name].get("params") or {}
        if any("!" in str(spec).split(" ", 1)[0] for spec in params.values()):
            continue
        ran += 1
        reply = g.call(name, force=g.a_force())
        asks_for_more = "required" in str(reply.get("m")) or "is needed" in str(reply.get("m"))
        if not reply.get("ok") and reply.get("e") in ("provider_error", "bad_result") and not asks_for_more:
            failed.append("%s: %s" % (name, str(reply.get("m"))[:160]))
    return (not failed), "%d tools ran, %d failed%s" % (ran, len(failed), ": " + "; ".join(failed) if failed else ""), {
        "ran": ran, "failed": failed, "manifest_bytes": manifest["_bytes"], "tool_count": len(tools),
    }


def every_sweep_metric_answers(g: Game) -> Result:
    """Each sweep metric that needs no subject runs, and none ends in a Lua error."""
    listing = g.call("sweep", force=g.a_force(), metric="no-such-metric")
    text = json.dumps(listing.get("r") or listing.get("m") or "")
    names = sorted(set(m for m in (
        "entities", "rockets", "research", "researched", "players", "item_made", "item_used", "item_rate",
        "pollution", "evolution", "robots", "logistic_robots", "construction_robots", "networks", "kills",
        "built", "fluid_rate", "trains") if '"%s"' % m in text or m in text))
    subjects = {"entities": "assembling-machine-1", "item_made": "iron-plate", "item_used": "iron-plate",
                "item_rate": "iron-plate", "fluid_rate": "water"}
    out, errors, refused = {}, [], []
    for name in names:
        args = {"force": g.a_force(), "metric": name}
        if name in subjects:
            args["subject"] = subjects[name]
        reply = g.call("sweep", **args)
        if not reply.get("ok"):
            errors.append("%s: %s" % (name, str(reply.get("m"))[:160]))
            continue
        r = reply["r"] if isinstance(reply["r"], dict) else {}
        if r.get("found") is False or r.get("refused"):
            refused.append(name)
        out[name] = {"bytes": reply["_bytes"], "rows": len(_rows(reply)), "refused": name in refused}
    detail = "%d metrics, %d errors, refused: %s" % (len(names), len(errors), ", ".join(refused) or "none")
    if errors:
        detail += " | " + "; ".join(errors)
    return (not errors and not refused), detail, {"metrics": out, "errors": errors, "listed": names}


WIDE_CALLS: List[Tuple[str, dict]] = [
    ("list_surfaces", {}),
    ("list_surfaces", {"limit": 50}),
    ("list_forces", {"names": True, "limit": 100}),
    ("list_forces", {"include_empty": True, "limit": 100}),
    ("list_players", {"all": True, "connected": False}),
    ("current_research", {"all": True}),
    ("locate_player", {"all": True}),
    ("rockets", {"all": True}),
    ("kills", {"all": True}),
    ("built", {"all": True}),
    ("trains", {"all": True}),
    ("entity_count", {"all": True, "name": "assembling-machine-1"}),
    ("entity_count", {"all": True, "per_surface": True, "name": "assembling-machine-1"}),
]


def wide_replies_fit(g: Game) -> Result:
    """No list reply is longer than what the service passes to the model."""
    sizes, over = {}, []
    for fn, args in WIDE_CALLS:
        reply = g.call(fn, force=g.a_force(), **args)
        label = "%s %s" % (fn, json.dumps(args, sort_keys=True))
        sizes[label] = reply["_bytes"] if reply.get("ok") else "error %s" % reply.get("e")
        if not reply.get("ok") and reply.get("e") == "too_large":
            over.append(label + " refused too_large")
        elif reply.get("ok") and reply["_bytes"] > SERVICE_CUT:
            over.append("%s is %d bytes" % (label, reply["_bytes"]))
    return (not over), "%d calls, %d over %d bytes%s" % (
        len(WIDE_CALLS), len(over), SERVICE_CUT, ": " + "; ".join(over) if over else ""), sizes


def every_platform_is_reachable(g: Game) -> Result:
    """Paging through list_surfaces names every platform the map has."""
    want = sorted(s["name"] for s in g.surfaces() if s["platform"])
    if not want:
        return None, "no platforms on this map", None
    seen, offset, calls = set(), 0, 0
    while calls < 12:
        calls += 1
        reply = g.call("list_surfaces", force=g.a_force(), kind="platforms", offset=offset)
        if not reply.get("ok"):
            return False, "list_surfaces failed: %s" % reply.get("m"), None
        if reply["_bytes"] > SERVICE_CUT:
            return False, "page %d is %d bytes, past the service cut" % (calls, reply["_bytes"]), None
        rows = _rows(reply)
        seen.update(r.get("name") or r.get("surface") for r in rows)
        nxt = reply["r"].get("next_offset") if isinstance(reply["r"], dict) else None
        if not nxt or nxt == offset:
            break
        offset = nxt
    missing = [n for n in want if n not in seen]
    return (not missing), "%d of %d platforms listed in %d call(s)%s" % (
        len(want) - len(missing), len(want), calls, ", missing " + ", ".join(missing) if missing else ""), {
        "platforms": want, "missing": missing, "calls": calls}


def robots_metric_counts_both_kinds(g: Game) -> Result:
    truth = g.lua(
        "local out = {} for _, f in pairs(game.forces) do local n = 0 "
        "for _, nets in pairs(f.logistic_networks) do for _, net in pairs(nets) do "
        "n = n + net.all_logistic_robots + net.all_construction_robots end end "
        "if n > 0 then out[f.name] = n end end return out"
    ) or {}
    if not truth:
        return None, "no robots on this map", None
    reply = g.call("sweep", force=g.a_force(), metric="robots", limit=100)
    if not reply.get("ok"):
        return False, "sweep robots failed: %s" % reply.get("m"), None
    got = _sweep_values(reply)
    wrong = {f: (got.get(f), n) for f, n in truth.items() if _number(got.get(f)) != n}
    return (not wrong), "%d forces with robots, %d wrong (tool, engine): %s" % (
        len(truth), len(wrong), json.dumps(wrong)[:300]), {"engine": truth, "tool": got}


def researched_metric_matches(g: Game) -> Result:
    truth = g.lua(
        "local out = {} for _, f in pairs(game.forces) do if #f.players > 0 then local n = 0 "
        "for _, t in pairs(f.technologies) do if t.researched then n = n + 1 end end "
        "out[f.name] = n end end return out"
    ) or {}
    reply = g.call("sweep", force=g.a_force(), metric="researched", limit=100)
    r = reply.get("r") if isinstance(reply.get("r"), dict) else {}
    if not reply.get("ok") or r.get("found") is False or not _rows(reply):
        return False, "no researched metric: %s" % (str(reply.get("m") or r.get("reason"))[:160]), None
    got = _sweep_values(reply)
    wrong = {f: (got.get(f), n) for f, n in truth.items() if f in got and _number(got[f]) != n}
    return (not wrong and bool(got)), "%d forces ranked, %d wrong" % (len(got), len(wrong)), {"engine": truth, "tool": got}


def item_used_metric_matches(g: Game) -> Result:
    item = "iron-plate"
    truth = g.lua(
        "local out = {} for _, f in pairs(game.forces) do local n = 0 "
        "for _, s in pairs(game.surfaces) do "
        "local st = f.get_item_production_statistics(s) "
        "for _, q in pairs(prototypes.quality) do "
        "n = n + st.get_output_count({name = '%s', quality = q.name}) end end "
        "if n > 0 then out[f.name] = n end end return out" % item
    ) or {}
    reply = g.call("sweep", force=g.a_force(), metric="item_used", subject=item, limit=100)
    r = reply.get("r") if isinstance(reply.get("r"), dict) else {}
    if not reply.get("ok") or r.get("found") is False or not _rows(reply):
        return False, "no item_used metric: %s" % (str(reply.get("m") or r.get("reason"))[:160]), None
    got = _sweep_values(reply)
    # The server keeps running between the two reads, so a busy force's
    # lifetime total moves by a few items: allow that drift and no more.
    wrong = {f: (got.get(f), n) for f, n in truth.items()
             if f in got and abs(_number(got[f]) - n) > max(20, n * 1e-5)}
    return (not wrong and bool(got)), "%d forces, %d differ from the engine's counters" % (len(got), len(wrong)), {
        "engine": truth, "tool": got}


def _machine_fits_surface(g: Game, machine: str, surface: str) -> Optional[bool]:
    return g.lua(
        "local p = prototypes.entity['%s'] local s = game.surfaces['%s'] "
        "if not p or not s then return nil end "
        "for _, c in pairs(p.surface_conditions or {}) do "
        "local v = s.get_property(c.property) "
        "if v < c.min or v > c.max then return false end end return true" % (machine, surface)
    )


def _walk_strings(value, key_hint, out):
    if isinstance(value, dict):
        for k, v in value.items():
            if k in ("machine", "crafter") and isinstance(v, str):
                out.add(v)
            elif k in ("machine", "crafter") and isinstance(v, dict) and isinstance(v.get("name"), str):
                out.add(v["name"])
            else:
                _walk_strings(v, k, out)
    elif isinstance(value, list):
        for v in value:
            _walk_strings(v, key_hint, out)


def recipe_machines_fit_their_surface(g: Game) -> Result:
    """recipe_needs for a basic plate, asked on each kind of surface the map
    has, never names a machine that cannot run there."""
    picks = g.lua(
        "local seen, out = {}, {} for _, s in pairs(game.surfaces) do "
        "local key = s.platform and 'platform' or (s.planet and s.planet.prototype.name) "
        "if key and not seen[key] then seen[key] = true out[#out + 1] = s.name end end return out") or []
    bad, checked, data = [], 0, {}
    for surface in picks:
        for item in ("iron-plate", "copper-plate", "steel-plate"):
            reply = g.call("recipe_needs", force=g.a_force(), items=[item], per_min=[60], surface=surface)
            if not reply.get("ok"):
                data["%s@%s" % (item, surface)] = "error: %s" % str(reply.get("m"))[:120]
                continue
            machines = set()
            _walk_strings(reply["r"], "", machines)
            approx = reply["r"].get("approx") if isinstance(reply["r"], dict) else None
            data["%s@%s" % (item, surface)] = {"machines": sorted(machines), "approx": approx}
            for m in sorted(machines):
                checked += 1
                if _machine_fits_surface(g, m, surface) is False:
                    said = "not_makeable" in json.dumps(approx or "")
                    if not said:
                        bad.append("%s on %s uses %s" % (item, surface, m))
    if not checked:
        return None, "recipe_needs named no machines on %s" % ", ".join(picks), data
    return (not bad), "%d machine picks on %s, %d cannot run there%s" % (
        checked, ", ".join(picks), len(bad), ": " + "; ".join(bad[:6]) if bad else ""), data


def recipe_needs_without_a_rate(g: Game) -> Result:
    reply = g.call("recipe_needs", force=g.a_force(), items=["iron-plate"])
    ok = bool(reply.get("ok")) and not (isinstance(reply["r"], dict) and reply["r"].get("found") is False)
    return ok, ("answers with a default" if ok else "refused: %s" % str(reply.get("m"))[:160]), {
        "ok": ok, "bytes": reply["_bytes"]}


def recipe_needs_says_what_it_leaves_out(g: Game) -> Result:
    reply = g.call("recipe_needs", force=g.a_force(), items=["iron-plate"], per_min=[60])
    if not reply.get("ok"):
        return False, "recipe_needs failed: %s" % str(reply.get("m"))[:160], None
    text = json.dumps(reply["r"])
    said = "no_modules" in text
    return said, ("says modules and beacons are left out" if said else "silent about modules and beacons"), {
        "approx": reply["r"].get("approx") if isinstance(reply["r"], dict) else None}


def lists_in_one_call(g: Game) -> Result:
    """production_since and entity_count take several names at once."""
    force = g.a_force()
    many = g.call("production_since", force=force, items=["iron-plate", "copper-plate"], since_tick=0)
    names = g.call("entity_count", force=force, all=True, names=["stone-furnace", "steel-furnace"])
    by_type = g.call("entity_count", force=force, all=True, type="furnace")
    oks = {"production_since items": bool(many.get("ok")) and "copper-plate" in json.dumps(many.get("r")),
           "entity_count names": bool(names.get("ok")) and "steel-furnace" in json.dumps(names.get("r")),
           "entity_count type": bool(by_type.get("ok")) and "furnace" in json.dumps(by_type.get("r"))
           and not (isinstance(by_type.get("r"), dict) and by_type["r"].get("found") is False)}
    failed = [k for k, v in oks.items() if not v]
    return (not failed), ("all three work" if not failed else "not working: " + ", ".join(failed)), oks


def rates_without_a_surface(g: Game) -> Result:
    force = g.a_force()
    rate = g.call("item_rate", force=force, item="iron-plate", window="one_hour")
    top = g.call("top_items", force=force, window="one_hour", n=5)
    oks = {"item_rate": bool(rate.get("ok")) and not (isinstance(rate["r"], dict) and rate["r"].get("found") is False),
           "top_items": bool(top.get("ok")) and not (isinstance(top["r"], dict) and top["r"].get("found") is False)}
    failed = [k for k, v in oks.items() if not v]
    return (not failed), ("both answer for the whole force" if not failed else "need a surface: " + ", ".join(failed)), {
        "item_rate": rate.get("r") if rate.get("ok") else rate.get("m"), "ok": oks}


def tech_status_is_honest(g: Game) -> Result:
    force = g.a_force()
    tech = g.lua(
        "for _, t in pairs(game.forces['%s'].technologies) do "
        "if t.researched and #t.research_unit_ingredients > 0 then return t.name end end return nil" % force)
    if not tech:
        return None, "%s has researched nothing" % force, None
    reply = g.call("tech_status", force=force, tech=tech)
    if not reply.get("ok"):
        return False, "tech_status failed: %s" % str(reply.get("m"))[:160], None
    r = reply["r"]
    problems = []
    if r.get("available") is True:
        problems.append("available=true for a researched tech")
    if "ingredients" not in r:
        problems.append("no ingredients")
    everyone = g.call("tech_status", force=force, tech=tech, all=True)
    if not everyone.get("ok") or len(_rows(everyone)) < 2:
        problems.append("all=true does not list forces")
    return (not problems), ("%s: " % tech) + ("; ".join(problems) or "researched, not available, ingredients listed, all forces listed"), {
        "tech": tech, "reply": r}


def long_window_says_partial(g: Game) -> Result:
    """A rate over a window longer than the map has existed says so."""
    age = g.lua("return game.ticks_played")
    windows = [("one_thousand_hours", 216000000), ("two_hundred_fifty_hours", 54000000), ("fifty_hours", 10800000)]
    window = next((w for w, ticks in windows if ticks > age), None)
    if window is None:
        return None, "the map is older than every window", None
    surface = next((s["name"] for s in g.surfaces() if not s["platform"]), "nauvis")
    reply = g.call("item_rate", force=g.a_force(), item="iron-plate", window=window, surface=surface)
    if not reply.get("ok"):
        return False, "item_rate %s failed: %s" % (window, str(reply.get("m"))[:120]), None
    said = reply["r"].get("partial_window") is True
    return said, "map age %d ticks, window %s: %s" % (age, window, "marked partial" if said else "not marked"), reply["r"]


def evolution_force_facts(g: Game) -> Result:
    """Not a pass or fail: records whose evolution the tool reports, beside
    the enemy force's, so the question can be settled from real numbers."""
    surface = g.lua(
        "local best, most = 'nauvis', -1 for _, s in pairs(game.surfaces) do "
        "local p = s.get_total_pollution() if p > most then best, most = s.name, p end end return best")
    force = g.lua(
        "local s = game.surfaces['%s'] local best, most = nil, -1 for _, f in pairs(game.forces) do "
        "if #f.players > 0 then local n = s.count_entities_filtered{force = f, limit = 500} "
        "if n > most then best, most = f.name, n end end end return best" % surface) or g.a_force()
    truth = g.lua(
        "local s = game.surfaces['%s'] local out = {} "
        "for _, name in pairs({'enemy', '%s'}) do local f = game.forces[name] "
        "out[name] = {total = f.get_evolution_factor(s), time = f.get_evolution_factor_by_time(s), "
        "pollution = f.get_evolution_factor_by_pollution(s), spawners = f.get_evolution_factor_by_killing_spawners(s)} end "
        "return out" % (surface, force))
    reply = g.call("evolution", force=force, surface=surface)
    return None, "recorded for %s on %s" % (force, surface), {"engine": truth, "tool": reply.get("r")}


def kills_says_what_it_counts(g: Game) -> Result:
    reply = g.call("kills", force=g.a_force())
    if not reply.get("ok"):
        return False, "kills failed: %s" % str(reply.get("m"))[:120], None
    has = "enemies" in json.dumps(reply["r"])
    return has, ("splits enemies from trees and rocks" if has else "one undivided total"), reply["r"]


# ---------------------------------------------------------------- core


def diagnostic_ops_are_off(g: Game) -> Result:
    states = {op: g.rpc(op).get("e") or "ok" for op in ("big", "write")}
    on = [op for op, state in states.items() if state != "bad_op"]
    return (not on), ("off by default" if not on else "still answering: " + ", ".join(on)), states


def _remote_ask(g: Game, lua_spec: str) -> Any:
    return g.lua("return remote.call('%s', 'ask', %s)" % (ASK, lua_spec))


def bad_audience_does_not_block_an_answer(g: Game) -> Result:
    """A private audience holding player indexes the engine refuses still gets
    its answer recorded; the fault is not blamed on the artifact."""
    qid = _remote_ask(g, "{text = 'audience probe', scope = {key = 'probe-a', private = true, "
                         "audience = {players = {-1, 100000}}}}")
    if not qid:
        return False, "ask returned nothing", None
    reply = g.rpc("answer", qid=qid, artifact={"shape": "summary", "lines": ["audience probe answer"]})
    ok = bool(reply.get("ok"))
    return ok, ("answered" if ok else "refused as %s: %s" % (reply.get("e"), str(reply.get("m"))[:140])), {
        "qid": qid, "code": reply.get("e")}


def answer_head_has_no_control_characters(g: Game) -> Result:
    qid = _remote_ask(g, "{text = 'head probe'}")
    art = {"shape": "summary", "lines": ["head probe answer"], "session": {"name": "x)\n[forged]: hi", "fresh": True}}
    reply = g.rpc("answer", qid=qid, artifact=art)
    if not reply.get("ok"):
        return True, "refused the artifact (%s), nothing printed" % reply.get("e"), {"code": reply.get("e")}
    rows = g.rpc("answers", after=qid - 1, limit=1)
    lines = rows["r"][0]["lines"] if rows.get("ok") and rows["r"] else []
    dirty = [l for l in lines if any(ord(c) < 32 for c in l) or "forged" in l]
    return (not dirty), ("clean" if not dirty else "printed: %r" % dirty[0][:120]), {"lines": lines}


def remote_asks_are_rate_limited_and_ring_keeps_pending(g: Game) -> Result:
    """Two things in one pass, because both need many asks: a burst of remote
    asks is capped per minute, and a question still waiting for its answer is
    not pushed out of the ring by answered ones."""
    first = _remote_ask(g, "{text = 'ring probe: the one left unanswered'}")
    if not first:
        return False, "the first ask returned nothing", None
    asked, refused_at = 0, None
    deadline = time.monotonic() + 260
    ids = []
    while len(ids) < 70 and time.monotonic() < deadline:
        qid = _remote_ask(g, "{text = 'ring filler %d'}" % asked)
        asked += 1
        if not qid:
            if refused_at is None:
                refused_at = asked
            time.sleep(5)
            continue
        ids.append(qid)
        g.rpc("answer", qid=qid, artifact={"shape": "notice", "text": "filler"})
    pending = g.rpc("poll", after=first - 1, limit=1)
    still_there = bool(pending.get("ok")) and bool(pending["r"]) and pending["r"][0].get("id") == first
    limited = refused_at is not None
    detail = "%d fillers asked and answered; burst %s; the unanswered question %s" % (
        len(ids), "capped at ask %d" % refused_at if limited else "never capped",
        "is still pending" if still_there else "was evicted")
    return (still_there and limited and len(ids) >= 65), detail, {
        "first": first, "fillers": len(ids), "refused_at": refused_at, "still_pending": still_there}


def hostile_remote_ask_does_not_block_poll(g: Game) -> Result:
    """Run last: on a companion without the fix this leaves the queue stuck."""
    before = g.rpc("status")["r"]["last_id"]
    qid = _remote_ask(
        g, "{text = 'poison probe', force = string.rep('f', 100000), "
           "scope = {key = string.rep('k', 100000), tag = string.rep('t', 100000), audience = 'x'}}")
    reply = g.rpc("poll", after=before, limit=4)
    ok = bool(reply.get("ok"))
    detail = "poll returns %d bytes" % reply["_bytes"] if ok else "poll fails with %s" % reply.get("e")
    row = reply["r"][0] if ok and reply["r"] else {}
    if ok and row:
        oversize = [k for k in ("force", "scope", "tag") if isinstance(row.get(k), str) and len(row[k]) > 200]
        if oversize:
            ok, detail = False, "poll row keeps oversized %s" % ", ".join(oversize)
    return ok, detail, {"qid": qid, "poll_bytes": reply["_bytes"], "code": reply.get("e")}


def _json_literal(value) -> str:
    """A Lua string literal for plain names, for building console code."""
    return json.dumps(value)


# machine_status reads at most this many entities (SCAN_CAP in
# companion-mod/scripts/tools/machine_status.lua).
MACHINE_STATUS_CAP = 4000
MACHINE_STATUS_TYPES = ["assembling-machine", "furnace", "mining-drill", "lab", "inserter"]


def _status_counts_lua(surface: str, force: str, kind: str) -> str:
    """Console Lua: the engine's own per-status count, named as the tool names them."""
    return ("local names = {} for k, v in pairs(defines.entity_status) do names[v] = k end "
            "local by = {} for _, e in ipairs(game.surfaces[%s].find_entities_filtered{force = %s, type = %s}) do "
            "local key = e.status ~= nil and names[e.status] or 'unknown' by[key] = (by[key] or 0) + 1 end return by"
            % (_json_literal(surface), _json_literal(force), _json_literal(kind)))


def machine_status_matches_engine(g: Game) -> Result:
    """machine_status per-status counts match the engine's on a scan under the cap,
    and a scan over the cap says it is truncated."""
    found = g.lua(
        "local types = {%s} local under, over = nil, nil "
        "for _, f in pairs(game.forces) do if #f.players > 0 and f.name ~= 'spectator' then "
        "for _, s in pairs(game.surfaces) do if not s.platform then for _, t in ipairs(types) do "
        "local n = s.count_entities_filtered{force = f.name, type = t} "
        "local one = {force = f.name, surface = s.name, type = t, n = n} "
        "if n >= 20 and n < %d and (not under or n > under.n) then under = one end "
        "if n >= %d and (not over or n > over.n) then over = one end "
        "end end end end end return {under = under, over = over}"
        % (",".join(_json_literal(t) for t in MACHINE_STATUS_TYPES), MACHINE_STATUS_CAP, MACHINE_STATUS_CAP)) or {}
    under, over = found.get("under"), found.get("over")
    if not under and not over:
        return None, "no force has 20 or more machines of one type on a planet", None
    problems, notes, data = [], [], {"found": found}
    if under:
        reply = g.call("machine_status", force=under["force"], surface=under["surface"], type=under["type"])
        if not reply.get("ok"):
            return False, "machine_status failed: %s" % str(reply.get("m"))[:160], None
        r = reply["r"]
        tool = {row.get("status"): int(row.get("count", 0)) for row in r.get("statuses", [])}
        engine = g.lua(_status_counts_lua(under["surface"], under["force"], under["type"])) or {}
        data.update(tool=tool, engine=engine)
        where = "%s %s on %s" % (under["force"], under["type"], under["surface"])
        if r.get("truncated"):
            problems.append("%s: %d entities, under the cap, yet truncated" % (where, under["n"]))
        if r.get("scanned") != sum(engine.values()):
            problems.append("%s: scanned %s, engine %d" % (where, r.get("scanned"), sum(engine.values())))
        if (r.get("total") or 0) > (r.get("shown") or 0):
            notes.append("%s of %s statuses shown" % (r.get("shown"), r.get("total")))
        # Statuses move between the two reads (working <-> no_ingredients), so
        # each may drift by 3 or 10 percent, whichever is more.
        for status in sorted(set(tool) | set(engine)):
            t, e = tool.get(status, 0), engine.get(status, 0)
            if abs(t - e) > max(3, 0.1 * e):
                problems.append("%s: %s tool %d, engine %d" % (where, status, t, e))
        notes.append("%s: %d scanned, statuses %s" % (where, r.get("scanned", 0),
                     ", ".join("%s %d/%d" % (k, tool.get(k, 0), engine.get(k, 0)) for k in sorted(set(tool) | set(engine)))))
    if over:
        reply = g.call("machine_status", force=over["force"], surface=over["surface"], type=over["type"])
        r = reply.get("r") or {}
        total = sum(int(row.get("count", 0)) for row in r.get("statuses", []))
        where = "%s %s on %s" % (over["force"], over["type"], over["surface"])
        if not reply.get("ok") or r.get("truncated") is not True:
            problems.append("%s: %d entities, over the cap, not marked truncated" % (where, over["n"]))
        elif total != r.get("scanned") or (r.get("matching") is not None and r.get("matching") != over["n"]
                                            and abs(r.get("matching") - over["n"]) > 2):
            problems.append("%s: truncated, counted %d of %s scanned, matching %s, engine %d"
                            % (where, total, r.get("scanned"), r.get("matching"), over["n"]))
        else:
            notes.append("%s: truncated honestly, %s of %s" % (where, r.get("scanned"), r.get("matching", over["n"])))
        data["over_reply"] = {k: r.get(k) for k in ("scanned", "truncated", "matching")}
    return (not problems), "; ".join(problems or notes), data


def power_finds_the_networks(g: Game) -> Result:
    """power answers for a surface where the force has electric poles, within the reply budget."""
    # The most poles: the network most likely to carry real production.
    found = g.lua(
        "local best = nil for _, f in pairs(game.forces) do if #f.players > 0 then "
        "for _, s in pairs(game.surfaces) do if not s.platform then "
        "local n = s.count_entities_filtered{force = f.name, type = 'electric-pole'} "
        "if n > 0 and (not best or n > best.poles) then best = {force = f.name, surface = s.name, poles = n} end "
        "end end end end return best")
    if not found:
        return None, "no force has an electric pole on a planet", None
    reply = g.call("power", force=found["force"], surface=found["surface"])
    if not reply.get("ok"):
        return False, "power failed: %s" % str(reply.get("m"))[:160], None
    r = reply["r"]
    problems = []
    if r.get("found") is not True or not r.get("networks"):
        problems.append("no network found with %d poles on the console count" % found["poles"])
    if reply["_bytes"] > 3800:
        problems.append("reply is %d bytes, over 3800" % reply["_bytes"])
    # Production is the network's output side (measured on 2.1.23 and 2.1.24):
    # a machine that only draws power named as a producer means the sides
    # are swapped.
    producers = sorted(set(p.get("name") for n in r.get("networks") or [] for p in n.get("producers") or []))
    if producers:
        drawing = g.lua(
            "local only = {['assembling-machine'] = true, furnace = true, inserter = true, lab = true, beacon = true, "
            "roboport = true, ['mining-drill'] = true, radar = true} local bad = {} "
            "for _, n in ipairs({%s}) do local p = prototypes.entity[n] if p and only[p.type] then bad[#bad + 1] = n end end return bad"
            % ",".join(_json_literal(n) for n in producers)) or []
        if drawing:
            problems.append("consumers reported as producers: %s" % ", ".join(drawing))
    return (not problems), ("; ".join(problems) or "%d networks, %d bytes, producers %s" % (
        r.get("networks_total", 0), reply["_bytes"], ", ".join(producers) or "none")), {
        "found": found, "reply": r}


def research_cost_starts_with_the_queue(g: Game) -> Result:
    """research_cost lists the engine's first queued technology, with real packs."""
    queued = g.lua(
        "for _, f in pairs(game.forces) do if #f.players > 0 and #f.research_queue > 0 then "
        "local first = f.research_queue[1] return {force = f.name, tech = first.name or first} end end return nil")
    if not queued:
        return None, "no force has a research queue", None
    reply = g.call("research_cost", force=queued["force"])
    if not reply.get("ok"):
        return False, "research_cost failed: %s" % str(reply.get("m"))[:160], None
    r = reply["r"]
    techs = r.get("technologies") or []
    if not techs or techs[0].get("tech") != queued["tech"]:
        return False, "first technology %s, engine says %s" % (techs[0].get("tech") if techs else None, queued["tech"]), r
    names = sorted(set(i["name"] for t in techs for i in t.get("ingredients", [])))
    # A Lua table of strings: a JSON array is not Lua, and the console
    # printed nothing for it, which was the JSONDecodeError.
    bad = g.lua("local bad = {} for _, n in ipairs({%s}) do if not prototypes.item[n] then bad[#bad + 1] = n end end return bad"
                % ",".join(_json_literal(n) for n in names)) if names else []
    return (not bad), ("not item prototypes: %s" % bad) if bad else "%s first, %d packs all real items" % (queued["tech"], len(names)), {
        "queued": queued, "reply": r}


def mods_matches_active_mods(g: Game) -> Result:
    """mods count equals script.active_mods, and the companion is in the rows."""
    engine = g.lua("local n = 0 for _ in pairs(script.active_mods) do n = n + 1 end return n")
    reply = g.call("mods", force=g.a_force())
    if not reply.get("ok"):
        return False, "mods failed: %s" % str(reply.get("m"))[:160], None
    r = reply["r"]
    names = [row.get("name") for row in r.get("mods", [])]
    problems = []
    if r.get("count") != engine:
        problems.append("count %s, engine %s" % (r.get("count"), engine))
    if "ai-agent-bridge" not in names and not r.get("next_offset"):
        problems.append("companion missing from the rows")
    return (not problems), ("; ".join(problems) or "%d mods, companion listed" % engine), {"count": r.get("count"), "engine": engine}


def resources_needs_a_player(g: Game) -> Result:
    """resources refuses to run without near (found=false, no error) and, with a player, matches the engine."""
    surface = next((s["name"] for s in g.surfaces() if not s["platform"]), None)
    if surface is None:
        return None, "no planet surface", None
    bare = g.call("resources", force=g.a_force(), surface=surface)
    r = bare.get("r") or {}
    if not bare.get("ok") or r.get("found") is not False or not r.get("reason"):
        return False, "without near expected found=false with a reason, got %s" % str(bare)[:200], None
    player = g.lua("local p = game.connected_players[1] return p and {name = p.name, surface = p.surface.name} or nil")
    if not player:
        return None, "SKIP: resources needs a connected player and none is connected (without near: found=false, reason given)", {"bare": r}
    reply = g.call("resources", force=g.a_force(), surface=player["surface"], near=player["name"], radius=64)
    rr = reply.get("r") or {}
    if not reply.get("ok") or rr.get("found") is not True:
        return False, "with near expected found=true, got %s" % str(reply)[:200], None
    engine = g.lua("local p = game.get_player(%s) return p.surface.count_entities_filtered{type = 'resource', position = p.position, radius = 64}"
                   % _json_literal(player["name"]))
    if rr.get("truncated"):
        return True, "truncated at %s scanned (engine counts %s)" % (rr.get("scanned"), engine), rr
    return abs(rr.get("scanned", 0) - engine) <= 2, "tool scanned %s, engine %s" % (rr.get("scanned"), engine), rr


def force_card_matches_engine(g: Game) -> Result:
    """force_card for the force with the most players: researched, rockets and robots equal the engine's own counts, and the reply fits."""
    name = g.lua("local best, n = nil, -1 for _, f in pairs(game.forces) do if #f.players > n then best, n = f.name, #f.players end end return best")
    if not name:
        return None, "no force to summarise", None
    truth = g.lua(
        "local f = game.forces[%s] local r = 0 for _, t in pairs(f.technologies) do if t.researched then r = r + 1 end end "
        "local bots = 0 for _, nets in pairs(f.logistic_networks) do for _, net in pairs(nets) do "
        "bots = bots + net.all_logistic_robots + net.all_construction_robots end end "
        "return {researched = r, rockets = f.rockets_launched, robots = bots}" % _json_literal(name)
    ) or {}
    reply = g.call("force_card", force=name)
    if not reply.get("ok"):
        return False, "force_card failed: %s" % str(reply.get("m"))[:160], None
    r = reply["r"]
    size = len(json.dumps(reply, separators=(",", ":")))
    problems = []
    if r.get("researched") != truth.get("researched"):
        problems.append("researched %s, engine %s" % (r.get("researched"), truth.get("researched")))
    if r.get("rockets") != truth.get("rockets"):
        problems.append("rockets %s, engine %s" % (r.get("rockets"), truth.get("rockets")))
    # A cut card sums only the busiest surfaces, so its robots are allowed to be fewer than the engine's.
    if r.get("robots_total") != truth.get("robots") and not (r.get("surfaces_cut") and r.get("robots_total", 0) <= truth.get("robots", 0)):
        problems.append("robots_total %s, engine %s" % (r.get("robots_total"), truth.get("robots")))
    if size >= 3800:
        problems.append("reply is %d bytes, the budget is 3800" % size)
    return (not problems), ("; ".join(problems) or "%s: %d researched, %d bytes" % (name, r.get("researched", 0), size)), {"tool": r, "engine": truth}


def say_accepts_a_summary_and_refuses_an_empty_one(g: Game) -> Result:
    """say takes a summary (one line printed to anyone online) and refuses an artifact with no lines as bad_artifact."""
    good = g.rpc("say", artifact={"shape": "summary", "lines": ["real-save probe: say works"]})
    if not good.get("ok") or good.get("r") is not True:
        if good.get("e") == "bad_op":
            return None, "SKIP: aab-allow-say is off on this save", {"code": "bad_op"}
        return False, "a summary was refused: %s %s" % (good.get("e"), str(good.get("m"))[:120]), None
    empty = g.rpc("say", artifact={"shape": "summary", "lines": []})
    if empty.get("ok") or empty.get("e") != "bad_artifact":
        return False, "an empty summary got %s" % str(empty)[:160], None
    return True, "summary accepted, empty lines refused as bad_artifact", None


PROBES: List[Tuple[str, str, Callable[[Game], Result]]] = [
    ("smoke", "every tool answers on this map", every_tool_answers),
    ("smoke", "every sweep metric answers", every_sweep_metric_answers),
    ("T2", "list replies fit under the service cut", wide_replies_fit),
    ("T2", "every platform is reachable by paging", every_platform_is_reachable),
    ("T3", "robots metric counts both kinds", robots_metric_counts_both_kinds),
    ("T10", "researched metric matches the engine", researched_metric_matches),
    ("F6", "item_used metric matches the engine", item_used_metric_matches),
    ("T1", "recipe machines can run on the asked surface", recipe_machines_fit_their_surface),
    ("F7", "recipe_needs answers without a rate", recipe_needs_without_a_rate),
    ("T9", "recipe_needs says what it leaves out", recipe_needs_says_what_it_leaves_out),
    ("F4", "several names in one call", lists_in_one_call),
    ("F9", "rates without naming a surface", rates_without_a_surface),
    ("T11", "tech_status on a researched technology", tech_status_is_honest),
    ("T6", "a window longer than the map says partial", long_window_says_partial),
    ("T8", "evolution: tool against engine (recorded)", evolution_force_facts),
    ("T12", "kills says what it counts", kills_says_what_it_counts),
    ("NEW", "machine_status counts add up to the engine's", machine_status_matches_engine),
    ("NEW", "power finds the electric networks", power_finds_the_networks),
    ("NEW", "research_cost starts with the queue's first technology", research_cost_starts_with_the_queue),
    ("NEW", "mods matches script.active_mods", mods_matches_active_mods),
    ("NEW", "resources needs near; with a player it matches the engine", resources_needs_a_player),
    ("FUN", "force_card matches the engine for the busiest force", force_card_matches_engine),
    ("FUN", "say accepts a summary and refuses empty lines", say_accepts_a_summary_and_refuses_an_empty_one),
    ("C9", "diagnostic ops are off", diagnostic_ops_are_off),
    ("C1", "a bad audience index does not block an answer", bad_audience_does_not_block_an_answer),
    ("C8", "answer head carries no control characters", answer_head_has_no_control_characters),
    ("C7+C11", "ring keeps a pending question; remote asks capped", remote_asks_are_rate_limited_and_ring_keeps_pending),
    ("C2", "a hostile remote ask does not block poll", hostile_remote_ask_does_not_block_poll),
]
