"""End-to-end scenarios against a running companion mod + service
(docs/design/phase1-2-spec.md "Harness"). Each scenario is a plain function
(ctx) -> (status, detail) where status is "PASS", "FAIL" or "SKIP"; run.py
turns that into one printed line per scenario and a process exit code.

Every scenario talks to the companion the way the real service does: the
aab-rpc-v1 protocol over RCON (see companion-mod/README.md), plus raw /sc
for the handful of things only a mod itself can do (raising on_console_chat,
killing a character, reading settings.global). Nothing here talks to the Go
service directly -- these are black-box checks of the two halves working
together, driven the same way an operator's server would be.

Several scenarios exercise surface that does not exist yet as this file is
written (the `run` subcommand, the `answers` op, chat-prefix wiring, the
history tools, per-player quota): PLAN.md's companion and service are being
built in parallel with this harness. That is expected; a scenario fails
loudly with the observed reply rather than silently, so it starts passing
the moment its dependency lands, with no change needed here.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass
from typing import Callable, List, Optional, Tuple

# rig.RCON is imported lazily by callers; scenarios only need the instance,
# not the class, so no import of rig here keeps this module runnable on its
# own (e.g. from a unit test that fakes the RCON connection).

Status = str  # "PASS" | "FAIL" | "SKIP"


@dataclass
class Result:
    name: str
    status: Status
    detail: str

    def line(self) -> str:
        return "%s %s: %s" % (self.status, self.name, self.detail)


@dataclass
class Ctx:
    server_rcon: object  # rig.RCON, connected
    client_player_index: Optional[int] = None
    provider_iface: Optional[str] = None
    answer_timeout: float = 25.0
    question_timeout: float = 10.0
    poll_interval: float = 0.5


# ---------------------------------------------------------------------------
# aab-rpc-v1 and /sc helpers


def lua_str(s: str) -> str:
    """Renders s as a double-quoted Lua string literal. Only escapes what a
    plain question or chat message can contain; scenarios never pass
    attacker-chosen Lua, only their own literals."""
    escaped = s.replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")
    return '"%s"' % escaped


def sc(conn, lua: str) -> str:
    return conn.command("/sc " + lua)


def aab_rpc(conn, op: str, **payload) -> dict:
    req = {"v": 1, "op": op}
    req.update(payload)
    reply = conn.command("/aab-rpc " + json.dumps(req))
    return json.loads(reply)


def as_list(r) -> list:
    # The companion's JSON encoder cannot tell an empty Lua array from an
    # empty object and emits {} for both (TESTING.md 1.7); the Go client's
    # unmarshalList works around the same thing.
    if isinstance(r, dict):
        return []
    return r or []


def ask_via_remote(conn, text: str, player_index: Optional[int] = None, force: Optional[str] = None) -> int:
    """Submits a question through the ai-agent-bridge-v1 remote interface,
    the same call path any other mod uses (companion-mod/README.md "2.
    Questions by interface"). Returns the new question id."""
    fields = ["text=%s" % lua_str(text)]
    if player_index is not None:
        fields.append("player_index=%d" % player_index)
    if force is not None:
        fields.append("force=%s" % lua_str(force))
    lua = 'rcon.print(tostring(remote.call("ai-agent-bridge-v1","ask",{%s})))' % ", ".join(fields)
    reply = conn.command("/sc " + lua)
    return int(reply.strip())


def latest_known_qid(conn) -> int:
    env = aab_rpc(conn, "poll", after=0)
    entries = as_list(env.get("r")) if env.get("ok") else []
    return max((e.get("id", 0) for e in entries), default=0)


def wait_for_new_question(conn, after: int, timeout: float, interval: float) -> dict:
    """Polls the pure-read `poll` op (never `answers`) for the first question
    with id > after -- used by scenarios that create a question by some path
    other than ask_via_remote, so they don't already know its id."""
    deadline = time.monotonic() + timeout
    last_env = None
    while time.monotonic() < deadline:
        env = aab_rpc(conn, "poll", after=after)
        last_env = env
        if env.get("ok"):
            entries = as_list(env.get("r"))
            if entries:
                return entries[0]
        time.sleep(interval)
    raise TimeoutError("no question appeared after id=%d within %ss (last poll reply: %r)" % (after, timeout, last_env))


def poll_for_answer(conn, qid: int, timeout: float, interval: float) -> dict:
    """Polls the `answers` op (docs/design/phase1-2-spec.md: new pure-read op
    `answers {after}` -> `[{id, shape, lines, player_index}]`) until qid is
    answered."""
    deadline = time.monotonic() + timeout
    last_env = None
    while time.monotonic() < deadline:
        env = aab_rpc(conn, "answers", after=0)
        last_env = env
        if env.get("ok"):
            for entry in as_list(env.get("r")):
                if entry.get("id") == qid:
                    return entry
        time.sleep(interval)
    raise TimeoutError("no answer for qid=%d within %ss (last `answers` reply: %r)" % (qid, timeout, last_env))


def find_provider_iface(conn) -> Optional[str]:
    """Scans the live tools catalog (the `tools` op) for a provider exposing
    both `hello` and `boom` -- tests/provider-mod/aab-test-provider's shape
    per docs/design/phase1-2-spec.md -- without hardcoding its interface
    name, since that mod is owned and built outside this harness."""
    env = aab_rpc(conn, "tools")
    if not env.get("ok"):
        return None
    for provider in as_list(env.get("r")):
        tools = provider.get("tools") or {}
        if "hello" in tools and "boom" in tools:
            return provider.get("iface")
    return None


# ---------------------------------------------------------------------------
# Scenarios. Each returns (status, detail).


def scenario_status(ctx: Ctx) -> Tuple[Status, str]:
    env = aab_rpc(ctx.server_rcon, "status")
    if not env.get("ok"):
        return "FAIL", "status op returned not-ok: %r" % (env,)
    r = env.get("r") or {}
    if r.get("protocol") != 1:
        return "FAIL", "expected protocol=1, got reply %r" % (r,)
    return "PASS", (
        "protocol=%r mod_version=%r tick=%r player_count=%r pending=%r ask_command=%r"
        % (r.get("protocol"), r.get("mod_version"), r.get("tick"), r.get("player_count"), r.get("pending"), r.get("ask_command"))
    )


def scenario_ask_forces(ctx: Ctx) -> Tuple[Status, str]:
    qid = ask_via_remote(ctx.server_rcon, "What forces are there?", force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    lines = " | ".join(entry.get("lines") or [])
    if "player" in lines.lower():
        return "PASS", "qid=%d shape=%s lines=%r" % (qid, entry.get("shape"), entry.get("lines"))
    return "FAIL", "answer for qid=%d did not mention 'player': %r" % (qid, entry)


def common_suffix(a: str, b: str) -> str:
    n = 0
    while n < min(len(a), len(b)) and a[-1 - n] == b[-1 - n]:
        n += 1
    return a[len(a) - n:] if n else ""


def scenario_ask_hello(ctx: Ctx) -> Tuple[Status, str]:
    if ctx.provider_iface is None:
        return "SKIP", "no provider exposing hello+boom in the tools catalog (tests/provider-mod not present or not registered yet)"

    # hello greets whatever name it is given, and the model picks that name
    # itself, so the whole reply string is not predictable from here. Call the
    # tool twice with two names of this scenario's own choosing and keep the
    # part both replies share: that part is the provider's fingerprint, and it
    # is what an answer built from this tool has to carry.
    # The two names share no character, so nothing of the name itself can
    # survive into the shared part.
    greetings = []
    for name in ("xxxxxx", "yyyyyy"):
        probe = aab_rpc(ctx.server_rcon, "call", i=ctx.provider_iface, f="hello", a={"name": name})
        if not probe.get("ok"):
            return "FAIL", "direct call %s.hello{name=%s} failed: %r" % (ctx.provider_iface, name, probe)
        greeting = (probe.get("r") or {}).get("greeting")
        if not greeting:
            return "FAIL", "%s.hello returned no 'greeting' field: %r" % (ctx.provider_iface, probe.get("r"))
        greetings.append(greeting)
    marker = common_suffix(*greetings)
    if len(marker) < 8:
        return "FAIL", "%s.hello's replies %r share too little to key on" % (ctx.provider_iface, greetings)

    qid = ask_via_remote(ctx.server_rcon, "hello", force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    lines = " | ".join(entry.get("lines") or [])
    if marker in lines:
        return "PASS", "qid=%d answer carried %s.hello's greeting %r" % (qid, ctx.provider_iface, marker)
    return "FAIL", "qid=%d answer did not carry %r: %r" % (qid, marker, entry)


def scenario_ask_table_of_players(ctx: Ctx) -> Tuple[Status, str]:
    qid = ask_via_remote(ctx.server_rcon, "table of players", force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    if entry.get("shape") == "table":
        return "PASS", "qid=%d shape=table lines=%r" % (qid, entry.get("lines"))
    return "FAIL", "qid=%d expected shape=table, got %r: %r" % (qid, entry.get("shape"), entry)


def scenario_chat_prefix(ctx: Ctx) -> Tuple[Status, str]:
    if ctx.client_player_index is None:
        return "SKIP", "needs --client (a connected player)"

    question_text = "what forces are there"
    # A runtime-global setting cannot be written from a console command over
    # RCON: the engine allows only the owning player or the mod that made the
    # setting, and the console exemption in the Lua docs covers player
    # settings only (measured on 2.0.77). run.py seeds aab-chat-prefix into
    # mod-settings.dat before the map is made; this reads back what took.
    prefix = sc(ctx.server_rcon, 'rcon.print(settings.global["aab-chat-prefix"].value)').rstrip("\n")
    if not prefix:
        return "SKIP", "aab-chat-prefix is empty; the chat trigger is off for this run"

    baseline = latest_known_qid(ctx.server_rcon)
    # on_console_chat is one of the handful of events LuaBootstrap::raise_event
    # explicitly allows raising (verified in runtime-api.json; PLAN.md and
    # docs/design/phase1-2-spec.md both script this exact call).
    sc(
        ctx.server_rcon,
        "script.raise_event(defines.events.on_console_chat, {player_index=%d, message=%s})"
        % (ctx.client_player_index, lua_str(prefix + question_text)),
    )

    try:
        q = wait_for_new_question(ctx.server_rcon, baseline, ctx.question_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", "chat message with the prefix did not create a question: %s" % e
    if q.get("text") != question_text:
        return "FAIL", "expected question text %r (prefix stripped), got %r" % (question_text, q.get("text"))

    try:
        entry = poll_for_answer(ctx.server_rcon, q["id"], ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    return "PASS", "chat prefix %r from player_index=%d created qid=%d, answered shape=%s" % (
        prefix, ctx.client_player_index, q["id"], entry.get("shape"),
    )


def scenario_last_death(ctx: Ctx) -> Tuple[Status, str]:
    if ctx.client_player_index is None:
        return "SKIP", "needs --client (a connected player)"

    idx = ctx.client_player_index
    # LuaEntity::die(force?, cause?) takes positional args, not a table
    # (runtime-api.json format.takes_table: false); no-args kills attributing
    # to the "neutral" force, which is all this scenario needs.
    reply = sc(
        ctx.server_rcon,
        'local p = game.get_player(%d); local had = p ~= nil and p.character ~= nil; '
        'if had then p.character.die() end; rcon.print(tostring(had))' % idx,
    )
    if reply.strip() != "true":
        return "FAIL", "player_index=%d had no character to kill (reply=%r)" % (idx, reply)

    qid = ask_via_remote(ctx.server_rcon, "when did I last die", player_index=idx, force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    lines = " | ".join(entry.get("lines") or [])
    if "player_died" in lines:
        return "PASS", "qid=%d mentioned player_died: %r" % (qid, entry.get("lines"))
    return "FAIL", "qid=%d answer did not mention player_died: %r" % (qid, entry)


def scenario_quota(ctx: Ctx) -> Tuple[Status, str]:
    # A synthetic, never-connected player_index: ask() stores whatever
    # player_index it's given without validating a real player exists
    # (companion-mod/scripts/questions.lua), and quota is keyed on that
    # asker identity server-side, not on an actual connection.
    synthetic_index = 900001
    qids = []
    for i in range(21):
        qids.append(ask_via_remote(ctx.server_rcon, "quota probe %d" % i, player_index=synthetic_index, force="player"))

    try:
        entry = poll_for_answer(ctx.server_rcon, qids[-1], ctx.answer_timeout, ctx.poll_interval)
    except TimeoutError as e:
        return "FAIL", str(e)
    if entry.get("shape") == "notice":
        return "PASS", "21st question in an hour for one player (qid=%d) was refused with shape=notice: %r" % (
            qids[-1], entry.get("lines"),
        )
    return "FAIL", "expected the 21st question (qid=%d) to be refused with shape=notice, got %r" % (qids[-1], entry)


def scenario_provider_error(ctx: Ctx) -> Tuple[Status, str]:
    iface = ctx.provider_iface
    fallback = iface is None
    if fallback:
        # Ships with the companion today (Phase 0 selftest interface,
        # companion-mod/scripts/rpc_selftest.lua) -- exercises the same
        # pcall(remote.call, ...) error path tests/provider-mod's `boom`
        # would, just against a provider that already exists.
        iface = "ai-agent-bridge-selftest"

    env = aab_rpc(ctx.server_rcon, "call", i=iface, f="boom", a={})
    if env.get("ok"):
        return "FAIL", "expected %s.boom to fail with provider_error, got an ok reply: %r" % (iface, env)
    if env.get("e") != "provider_error":
        return "FAIL", "expected error code provider_error from %s.boom, got %r (message=%r)" % (iface, env.get("e"), env.get("m"))
    note = " (tests/provider-mod not found yet; used the companion's own selftest interface instead)" if fallback else ""
    return "PASS", "call %s.boom -> e=provider_error m=%r%s" % (iface, env.get("m"), note)


@dataclass
class Scenario:
    name: str
    func: Callable[[Ctx], Tuple[Status, str]]
    needs_client: bool = False


SCENARIOS: List[Scenario] = [
    Scenario("status", scenario_status),
    Scenario("ask via remote interface: what forces are there", scenario_ask_forces),
    Scenario("ask hello: provider greeting", scenario_ask_hello),
    Scenario("ask: table of players", scenario_ask_table_of_players),
    Scenario("chat prefix creates a question", scenario_chat_prefix, needs_client=True),
    Scenario("history: last death", scenario_last_death, needs_client=True),
    Scenario("per-player quota", scenario_quota),
    Scenario("provider error -> provider_error", scenario_provider_error),
]


def run_all(ctx: Ctx, with_client: bool) -> List[Result]:
    results = []
    for scenario in SCENARIOS:
        if scenario.needs_client and not with_client:
            results.append(Result(scenario.name, "SKIP", "needs --client (a connected player)"))
            continue
        try:
            status, detail = scenario.func(ctx)
        except Exception as e:  # a scenario bug must not kill the rest of the run
            status, detail = "FAIL", "scenario raised %s: %s" % (type(e).__name__, e)
        results.append(Result(scenario.name, status, detail))
    return results
