"""End-to-end scenarios against a running companion mod + service
(docs/design/phase1-2-spec.md "Harness"). Each scenario is a plain function
(ctx) -> (status, detail) where status is "PASS", "FAIL" or "SKIP"; run.py
turns that into one printed line per scenario and a process exit code.

Every scenario talks to the companion the way the real service does: the
aab-rpc-v1 protocol over RCON (see companion-mod/README.md), plus raw /sc
for the handful of things only a mod itself can do (raising on_console_chat,
killing a character, reading settings.global). Nothing here talks to the Go
service directly. These are black-box checks of the two halves working
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


class RpcError(RuntimeError):
    """An aab-rpc-v1 call replied ok=false while a scenario was polling for
    something else to appear. Distinct from TimeoutError so a scenario can
    fail on the spot with the real error code instead of retrying an
    ok=false reply until the timeout, which reads exactly like "not answered
    yet" (review-fix contract item 13: "poll_for_answer ... fails fast on
    ok: false")."""


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
    events_file: Optional[object] = None  # pathlib.Path to the companion's
    # events.jsonl for this run (rig.Server.events_file), or None if the
    # caller never set one; scenario_last_death falls back to a fixed settle
    # window when it's absent.
    tailer_settle_seconds: float = 0.9  # see scenario_last_death: how long
    # to wait, once events.jsonl carries the death, for the file tailer and
    # the poll loop (independent tickers, both on poll_interval) to both
    # catch up. run.py sets this from the interval it actually configured.


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


def ask_via_remote(conn, text: str, player_index: Optional[int] = None, force: Optional[str] = None,
                   scope_lua: Optional[str] = None) -> int:
    """Submits a question through the ai-agent-bridge-v1 remote interface,
    the same call path any other mod uses (companion-mod/README.md "2.
    Questions by interface"). Returns the new question id. `scope_lua` is a
    Lua table literal for the optional `scope` field (seam 4)."""
    fields = ["text=%s" % lua_str(text)]
    if player_index is not None:
        fields.append("player_index=%d" % player_index)
    if force is not None:
        fields.append("force=%s" % lua_str(force))
    if scope_lua is not None:
        fields.append("scope=%s" % scope_lua)
    lua = 'rcon.print(tostring(remote.call("ai-agent-bridge-v1","ask",{%s})))' % ", ".join(fields)
    reply = conn.command("/sc " + lua)
    return int(reply.strip())


def latest_known_qid(conn) -> int:
    """The highest question id the companion has issued so far, read from
    status.last_id (review-fix contract item 4). Deriving it from a `poll`
    reply instead is wrong now that poll serves only unanswered questions and
    at most a page of them: every question already answered is missing from
    that reply, so the highest id in it is below the real one and a scenario
    using it as a baseline can match a question older than the one it just
    created."""
    env = aab_rpc(conn, "status")
    if not env.get("ok"):
        raise RpcError("status op returned ok=false while reading last_id: %r" % (env,))
    return int((env.get("r") or {}).get("last_id") or 0)


def wait_for_new_question(conn, after: int, timeout: float, interval: float) -> dict:
    """Polls the pure-read `poll` op (never `answers`) for the first question
    with id > after. Scenarios that create a question by some path other
    than ask_via_remote use it, since they don't know its id. Fails fast
    on an ok=false reply instead of retrying it until the timeout (see
    poll_for_answer; the same reasoning applies to `poll`)."""
    deadline = time.monotonic() + timeout
    last_env = None
    while time.monotonic() < deadline:
        env = aab_rpc(conn, "poll", after=after)
        last_env = env
        if not env.get("ok"):
            raise RpcError("poll op returned ok=false while waiting for a new question after id=%d: %r" % (after, env))
        entries = as_list(env.get("r"))
        if entries:
            return entries[0]
        time.sleep(interval)
    raise TimeoutError("no question appeared after id=%d within %ss (last poll reply: %r)" % (after, timeout, last_env))


def poll_for_answer(conn, qid: int, timeout: float, interval: float, after: Optional[int] = None) -> dict:
    """Polls the `answers` op (docs/design/phase1-2-spec.md: new pure-read op
    `answers {after}` -> `[{id, shape, lines, player_index}]`) until qid is
    answered.

    `after` is the cursor passed to `answers`; it defaults to qid - 1, i.e.
    "only entries at or after the question this call is waiting for". Every
    caller used to pass `after=0` unconditionally, which asks the companion
    to re-encode every answered question still in its 64-slot ring on every
    single poll. Once enough answers accumulate in one run (the quota
    scenario alone answers 21) that reply crosses the companion's 8000-byte
    cap and comes back {"ok":false,"e":"too_large"} for the rest of the run
    (review-fix contract item 13). Defaulting the cursor to qid - 1 keeps
    the reply to "this question and whatever was answered around the same
    time" regardless of how much history the ring is holding, with no change
    needed at any call site.

    Fails fast (RpcError) on an ok=false reply rather than looping on it
    until timeout, where it would look identical to "not answered yet"."""
    if after is None:
        after = qid - 1
    deadline = time.monotonic() + timeout
    last_env = None
    while time.monotonic() < deadline:
        env = aab_rpc(conn, "answers", after=after)
        last_env = env
        if not env.get("ok"):
            raise RpcError("answers op returned ok=false while waiting for qid=%d: %r" % (qid, env))
        for entry in as_list(env.get("r")):
            if entry.get("id") == qid:
                return entry
        time.sleep(interval)
    raise TimeoutError("no answer for qid=%d within %ss (last `answers` reply: %r)" % (qid, timeout, last_env))


def real_answer_problem(entry: dict) -> Optional[str]:
    """None when `entry`'s lines look like they came from a real tool call;
    otherwise the reason they don't (second review-fix contract item 10,
    tools-review findings 11, 12 and 14).

    Two ways an answer can carry no real game data while still looking like
    a normal reply to code that only checks a keyword or a shape:

    - A line starting with "You asked: " is the fake model's echo()
      (service/internal/model/fake/fake.go), used whenever no keyword
      matches the tools the model was actually offered. That happens not
      only for an off-topic question but whenever the tools catalog failed
      to build: defsFor(nil) leaves only submit_answer, so nothing can ever
      match and every question is answered from the system prompt alone
      (finding 11). A scenario that only checks for a bare word the question
      text itself contains (finding 12: "queue" is in "what is in the
      research queue" too) can't tell this apart from a real answer.
    - A line carrying "aab-rpc:" or "provider_error" is a Go-side rpc.Error
      (service/internal/rpc/rpc.go's Error.Error(): "aab-rpc: <code>[: <msg>]",
      and CodeProviderError is literally "provider_error") that leaked into
      an artifact as if it were tool data, because agent.go's failed() puts
      a failed tool call's error text in the same field a successful result
      would occupy and the round loop never short-circuits on it. A
      scenario that only checks an artifact's shape, not its content, still
      passes when the tool behind it errored (finding 14).
    """
    for line in entry.get("lines") or []:
        if line.startswith("You asked:"):
            return "line %r is the echo fallback: no tool was ever called" % (line,)
        if "aab-rpc:" in line:
            return "line %r carries an 'aab-rpc:' client error instead of tool data" % (line,)
        if "provider_error" in line:
            return "line %r carries a provider_error code instead of tool data" % (line,)
    return None


def wait_for_needle_in_file(path, needle: str, timeout: float, interval: float = 0.05) -> bool:
    """Polls a plain text file (events.jsonl) for a substring, without
    assuming it exists yet. Used to confirm the companion actually wrote an
    event before a scenario relies on the service having ingested it; see
    scenario_last_death."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path is not None and path.exists():
            text = path.read_text(encoding="utf-8", errors="replace")
            if needle in text:
                return True
        time.sleep(interval)
    return False


def find_provider_iface(conn) -> Optional[str]:
    """Scans the live tools catalog (the `tools` op) for a provider exposing
    both `hello` and `boom`, the shape tests/provider-mod/aab-test-provider
    has per docs/design/phase1-2-spec.md, without hardcoding its interface
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
        "protocol=%r mod_version=%r tick=%r player_count=%r pending=%r last_id=%r ask_command=%r"
        % (
            r.get("protocol"), r.get("mod_version"), r.get("tick"), r.get("player_count"),
            r.get("pending"), r.get("last_id"), r.get("ask_command"),
        )
    )


def scenario_providers_and_manifest(ctx: Ctx) -> Tuple[Status, str]:
    """Calls the two-step catalog ops directly (second review-fix contract
    item 1): `providers {}` -> `[{iface, v, tools: [names...]}]` for every
    probe found, then `manifest {i}` -> one provider's manifest verbatim.

    Confirms the companion's own tool provider shows up through `providers`
    with its tool names, and that `manifest` then returns real `desc` text
    for those same names, without hardcoding the companion's own interface
    name (an internal wiring detail of companion-mod/scripts/tools/engine.lua,
    not part of the frozen protocol): CONTEXT.md says "the companion is
    itself a provider of the engine tools and is discovered the same way as
    everyone else", so it is found here the same way `find_provider_iface`
    finds tests/provider-mod above, by the tool names only the companion's
    own provider carries (`list_forces`, `research_queue`;
    companion-mod/scripts/tools/basics.lua and tools/research.lua)."""
    env = aab_rpc(ctx.server_rcon, "providers")
    if not env.get("ok"):
        return "FAIL", "providers op returned not-ok: %r" % (env,)
    entries = as_list(env.get("r"))
    own = None
    for entry in entries:
        if "list_forces" in (entry.get("tools") or []):
            own = entry
            break
    if own is None:
        return "FAIL", "no provider in the `providers` reply carries list_forces (the companion's own tool provider): %r" % (entries,)

    iface = own.get("iface")
    own_tools = own.get("tools") or []
    for name in ("list_forces", "research_queue"):
        if name not in own_tools:
            return "FAIL", "companion's own provider (iface=%r) is missing %r from its `providers` tool list: %r" % (iface, name, own)

    man = aab_rpc(ctx.server_rcon, "manifest", i=iface)
    if not man.get("ok"):
        return "FAIL", "manifest op for iface=%r returned not-ok: %r" % (iface, man)
    manifest_tools = (man.get("r") or {}).get("tools") or {}
    for name in ("list_forces", "research_queue"):
        tool_entry = manifest_tools.get(name)
        if not isinstance(tool_entry, dict) or not tool_entry.get("desc"):
            return "FAIL", "manifest for iface=%r has no usable desc entry for %r: %r" % (iface, name, tool_entry)

    return "PASS", "providers listed iface=%r with %d tools including list_forces/research_queue; manifest returned desc text for both" % (
        iface, len(own_tools),
    )


def scenario_ask_forces(ctx: Ctx) -> Tuple[Status, str]:
    qid = ask_via_remote(ctx.server_rcon, "What forces are there?", force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    problem = real_answer_problem(entry)
    if problem:
        return "FAIL", "qid=%d answer is not real tool data: %s: %r" % (qid, problem, entry)
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
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    problem = real_answer_problem(entry)
    if problem:
        return "FAIL", "qid=%d answer is not real tool data: %s: %r" % (qid, problem, entry)
    lines = " | ".join(entry.get("lines") or [])
    if marker in lines:
        return "PASS", "qid=%d answer carried %s.hello's greeting %r" % (qid, ctx.provider_iface, marker)
    return "FAIL", "qid=%d answer did not carry %r: %r" % (qid, marker, entry)


def scenario_ask_table_of_players(ctx: Ctx) -> Tuple[Status, str]:
    """Asks "table of players" and requires both the shape and the content
    (second review-fix contract item 10, finding 14): the fake model derives
    the table shape from the word "table" in the question text alone, with
    no reference to whether list_players actually succeeded, so a shape
    check by itself still passes when the tool errored and its error text
    ended up as the table's one cell. Requiring '"players":' (the JSON key
    list_players's own reply carries, review-fix contract item 10) ties the
    check to the tool having actually run; real_answer_problem() below also
    rules out the error text landing there."""
    qid = ask_via_remote(ctx.server_rcon, "table of players", force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    problem = real_answer_problem(entry)
    if problem:
        return "FAIL", "qid=%d answer is not real tool data: %s: %r" % (qid, problem, entry)
    if entry.get("shape") != "table":
        return "FAIL", "qid=%d expected shape=table, got %r: %r" % (qid, entry.get("shape"), entry)
    lines = " | ".join(entry.get("lines") or [])
    if '"players":' not in lines:
        return "FAIL", "qid=%d shape=table but did not carry list_players's own \"players\": key: %r" % (qid, entry)
    return "PASS", "qid=%d shape=table carried \"players\": %r" % (qid, entry.get("lines"))


# ---------------------------------------------------------------------------
# Breadth addendum (docs/design/phase1-2-spec.md "Breadth addendum"): one ask
# per new engine tool. Each of these questions is worded to hit that tool's
# fake-model keyword rule (service/internal/model/fake/keywords.go) and the
# fake model's default shape, when the question carries none of "table",
# "list", "compare" or "notice", is `summary`, whose single line carries the
# compacted tool result JSON clipped to 160 characters (see PLAN.md's Fake
# model section). So the tool's own JSON key shows up literally in `lines`,
# and that key is what each of these checks for: real data from a real tool
# call, not a guess at prose the model might produce. All server-only, no
# --client and no tests/provider-mod needed.


def _ask_and_expect_field(ctx: Ctx, question: str, field: str, force: str = "player") -> Tuple[Status, str]:
    """Shared body for every breadth-addendum scenario below: ask `question`,
    wait for its answer, and require the JSON key `field` to appear somewhere
    in the answer's lines.

    `field` must be the quote and colon of a real JSON key, e.g. '"queued":',
    never a bare word (second review-fix contract item 10). A bare word can
    slip through two ways that have nothing to do with the tool ever being
    called: it can already be a substring of the echoed question text itself
    (finding 12: "queue" is in "what is in the research queue"), and it can
    turn up by accident in a Lua error's traceback (finding 13:
    "entity_count.lua" contains "count"). Neither text contains a literal
    `"<key>":` fragment, so quoting the key is what actually ties the
    assertion to the tool's own JSON reply."""
    qid = ask_via_remote(ctx.server_rcon, question, force=force)
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    problem = real_answer_problem(entry)
    if problem:
        return "FAIL", "qid=%d answer is not real tool data: %s: %r" % (qid, problem, entry)
    lines = " | ".join(entry.get("lines") or [])
    if field in lines:
        return "PASS", "qid=%d shape=%s carried %r: %r" % (qid, entry.get("shape"), field, entry.get("lines"))
    return "FAIL", "qid=%d answer did not carry %r: %r" % (qid, field, entry)


def scenario_research_queue(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "what is in the research queue", '"queued":')


def scenario_tech_status(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "tech status of automation", '"researched":')


def scenario_logistics_summary(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "logistic bots on nauvis", '"networks":')


def scenario_entity_count(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "how many character on nauvis", '"count":')


def scenario_evolution(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "evolution on nauvis", '"evolution_factor":')


def scenario_rockets(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "rockets launched", '"rockets_launched":')


def scenario_game_time(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "how long have we played", '"hours":')


def scenario_pollution(ctx: Ctx) -> Tuple[Status, str]:
    # total_pollution, not the bare word: the sibling evolution tool returns a
    # by_pollution key, so "pollution" alone would also pass on a mis-route.
    return _ask_and_expect_field(ctx, "pollution on nauvis", '"total_pollution":')


def scenario_production_since_start(ctx: Ctx) -> Tuple[Status, str]:
    return _ask_and_expect_field(ctx, "iron plate production since the start", '"produced":')


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
        # run.py seeds aab-chat-prefix into mod-settings.dat before the map
        # exists on every run this scenario actually executes (needs_client
        # gates entry on --client already), so an empty read-back here means
        # the seed did not take, not that the trigger is deliberately off.
        # SKIP would hide a real regression in the seeding step (review-fix
        # contract item 13).
        return "FAIL", "aab-chat-prefix read back empty; run.py always seeds it before the map exists, so empty means the seed did not take"

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
    except (TimeoutError, RpcError) as e:
        return "FAIL", "chat message with the prefix did not create a question: %s" % e
    if q.get("text") != question_text:
        return "FAIL", "expected question text %r (prefix stripped), got %r" % (question_text, q.get("text"))

    try:
        entry = poll_for_answer(ctx.server_rcon, q["id"], ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
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

    # The death only reaches history once two independent tickers both fire:
    # the service's file tailer (events.jsonl -> SQLite) and its poll loop
    # (RCON -> the question this scenario is about to ask). Asking right away
    # races them. Confirm the companion actually wrote the event first (that
    # part is synchronous, inside the /sc call above, so it should already be
    # there), then give both tickers one full interval to catch up before
    # asking, rather than relying on their fixed startup offset happening to
    # land in the right order (review-fix contract item 13).
    if ctx.events_file is not None:
        if not wait_for_needle_in_file(ctx.events_file, '"player_died"', timeout=5.0):
            return "FAIL", "player.character.die() did not produce a player_died line in events.jsonl within 5s (%s)" % ctx.events_file
    time.sleep(ctx.tailer_settle_seconds)

    qid = ask_via_remote(ctx.server_rcon, "when did I last die", player_index=idx, force="player")
    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
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

    # Check the 1st question too, not only the 21st: a quota implementation
    # that refuses every question (not just the ones past the cutoff) would
    # otherwise still satisfy "the 21st came back as a notice" (review-fix
    # contract item 13 / the finding this guards against).
    try:
        first = poll_for_answer(ctx.server_rcon, qids[0], ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", "1st of 21 questions (qid=%d): %s" % (qids[0], e)
    if first.get("shape") != "summary":
        return "FAIL", (
            "expected the 1st of 21 questions (qid=%d, within the hourly cap) to be answered normally "
            "(shape=summary) before checking that the 21st is refused, got %r"
        ) % (qids[0], first)

    try:
        last = poll_for_answer(ctx.server_rcon, qids[-1], ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", "21st of 21 questions (qid=%d): %s" % (qids[-1], e)
    if last.get("shape") == "notice":
        return "PASS", "1st question (qid=%d) got shape=summary; 21st in an hour for one player (qid=%d) was refused with shape=notice: %r" % (
            qids[0], qids[-1], last.get("lines"),
        )
    return "FAIL", "expected the 21st question (qid=%d) to be refused with shape=notice, got %r" % (qids[-1], last)


def scenario_provider_error(ctx: Ctx) -> Tuple[Status, str]:
    iface = ctx.provider_iface
    if iface is not None:
        env = aab_rpc(ctx.server_rcon, "call", i=iface, f="boom", a={})
        if env.get("ok"):
            return "FAIL", "expected %s.boom to fail with provider_error, got an ok reply: %r" % (iface, env)
        if env.get("e") != "provider_error":
            return "FAIL", "expected error code provider_error from %s.boom, got %r (message=%r)" % (iface, env.get("e"), env.get("m"))
        return "PASS", "call %s.boom -> e=provider_error m=%r" % (iface, env.get("m"))

    # tests/provider-mod not present yet. The companion's own Phase 0
    # selftest interface (companion-mod/scripts/rpc_selftest.lua) has a
    # `boom` function but no agent_tools_v1 probe, so it is not a real
    # provider: routing through the `call` op the way the branch above does
    # hits probe.lua's "no agent_tools_v1" check and comes back no_provider,
    # never provider_error. That is a different check than this scenario
    # exists for, so it always failed here regardless of whether pcall
    # actually caught the error. Drive the same pcall(remote.call, ...) path
    # directly through the selftest's own `pcall_test` diagnostic op
    # instead, which exists for exactly this
    # (companion-mod/scripts/rpc_selftest.lua).
    env = aab_rpc(ctx.server_rcon, "pcall_test")
    if not env.get("ok"):
        return "FAIL", "pcall_test op returned not-ok: %r" % (env,)
    r = env.get("r") or {}
    if not r.get("caught"):
        return "FAIL", "pcall_test reported caught=%r (expected true): %r" % (r.get("caught"), r)
    if "boom" not in str(r.get("message", "")):
        return "FAIL", "pcall_test caught an error but its message did not mention 'boom': %r" % (r,)
    return "PASS", "pcall_test caught the selftest provider's error (tests/provider-mod not found yet): %r" % (r,)


def scenario_answer_bad_artifact(ctx: Ctx) -> Tuple[Status, str]:
    """Submits a deliberately malformed artifact directly through the answer
    op and requires bad_artifact (review-fix contract item 5). CONTEXT.md
    says "any client that speaks [aab-rpc-v1] can drive the companion", and
    the reference service can never produce this shape itself
    (service/internal/agent/validate.go only ever emits comparison rows as
    []Pair, never bare numbers). So this scenario is the harness standing
    in for a non-reference client, the case the two findings this guards
    against (a table leaf reaching tostring(), and a render error stranding
    the question as answered-with-nothing) were both found through."""
    qid = ask_via_remote(ctx.server_rcon, "bad artifact probe (answered directly, not by the model)", force="player")
    # A known shape (comparison) whose rows are bare numbers instead of
    # {label, a, b} objects: the exact reproduction the two findings above
    # were filed against.
    artifact = {"shape": "comparison", "columns": ["a", "b"], "rows": [1, 2]}
    env = aab_rpc(ctx.server_rcon, "answer", qid=qid, artifact=artifact)
    if env.get("ok"):
        return "FAIL", "expected a malformed comparison artifact (rows of bare numbers, not {label,a,b} objects) to be refused, got an ok reply: %r" % (env,)
    if env.get("e") != "bad_artifact":
        return "FAIL", "expected error code bad_artifact for a malformed artifact, got %r (message=%r)" % (env.get("e"), env.get("m"))
    return "PASS", "answer op refused the malformed artifact with e=bad_artifact m=%r" % (env.get("m"),)


def scenario_answer_large_table(ctx: Ctx) -> Tuple[Status, str]:
    """Submits a table artifact at roughly the shape caps (5 columns x 8 rows
    x 150-char cells) directly through the answer op for a fresh question,
    and requires ok=true. TESTING.md 1.5 measured Factorio's RCON carrying
    commands well past 1,000,000 bytes intact, and finding 1 measured the
    shape caps alone producing a ~7.4 KB table artifact. The 1000-byte
    ceiling several findings ran into (review-fix contract item 1) turned
    out to be gorcon's own MaxCommandLen, not the engine's or the
    companion's. This proves the transport this harness actually speaks
    (its own RCON client, and the companion's command handling) carries an
    artifact well past 1000 bytes with nothing special-cased on either
    side.

    Reads the question back through `answers` afterwards and requires the
    recorded shape and first line to be exactly the table this scenario sent
    (second review-fix contract item 10, finding 15): the service polls
    every ctx.poll_interval too, and this question is created through the
    same ai-agent-bridge-v1 path any other asker uses, so if the service's
    own poll lands between ask_via_remote() and this scenario's own `answer`
    call below, the service answers it first with the fake model's echo (no
    keyword in "size probe ..." matches anything), and this scenario's
    `answer` call then only hits rpc.lua's already-answered short-circuit
    ("if question.answered then return ok_reply(true) end") without ever
    rendering anything. That race can only turn a FAIL into a silent PASS
    when the only assertion is `ok`, which is exactly why it was invisible
    in CI; reading the companion's own recorded shape and title back closes
    it, and real_answer_problem() below also catches the echo directly."""
    qid = ask_via_remote(ctx.server_rcon, "size probe (answered directly, not by the model)", force="player")
    columns = ["col-%d" % i for i in range(5)]
    cell = "x" * 150
    rows = [[cell] * 5 for _ in range(8)]
    title = "size probe"
    artifact = {"shape": "table", "title": title, "columns": columns, "rows": rows}
    req = {"v": 1, "op": "answer"}
    req.update({"qid": qid, "artifact": artifact})
    command_len = len("/aab-rpc " + json.dumps(req))
    if command_len <= 1000:
        return "FAIL", "test bug: constructed only a %d-byte command, too small to prove anything past the 1000-byte figure" % command_len
    env = aab_rpc(ctx.server_rcon, "answer", qid=qid, artifact=artifact)
    if not env.get("ok"):
        return "FAIL", "a %d-byte answer command (well past 1000 bytes) was refused: %r" % (command_len, env)

    try:
        entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    except (TimeoutError, RpcError) as e:
        return "FAIL", "answer op returned ok=true but the question never showed up answered through `answers`: %s" % e
    problem = real_answer_problem(entry)
    if problem:
        return "FAIL", (
            "qid=%d answer op returned ok=true, but the recorded answer is not the table this scenario sent: %s "
            "(the service's own poll likely answered qid=%d first, and this scenario's `answer` call only hit "
            "the already-answered short-circuit): %r"
        ) % (qid, problem, qid, entry)
    if entry.get("shape") != "table":
        return "FAIL", "qid=%d answer op returned ok=true, but the recorded shape is %r, not table: %r" % (qid, entry.get("shape"), entry)
    lines = entry.get("lines") or []
    if not lines or lines[0] != title:
        return "FAIL", "qid=%d recorded shape=table, but the first line is %r, not the title %r this scenario sent: %r" % (qid, lines[0] if lines else None, title, entry)
    return "PASS", (
        "answer op accepted a %d-byte command (qid=%d), past the 1000-byte figure that turned out to be gorcon's "
        "limit, not the transport's, and the companion's own record confirms it actually rendered this table "
        "(shape=table, first line=%r)"
    ) % (command_len, qid, title)


@dataclass
class Scenario:
    name: str
    func: Callable[[Ctx], Tuple[Status, str]]
    needs_client: bool = False


def _answer_lines(ctx: Ctx, qid: int) -> List[str]:
    entry = poll_for_answer(ctx.server_rcon, qid, ctx.answer_timeout, ctx.poll_interval)
    return list(entry.get("lines") or [])


def _ask_lines(ctx: Ctx, text: str, force: str = "enemy", scope_lua: Optional[str] = None) -> Tuple[int, List[str]]:
    """Asks as the enemy force: the per-player quota is keyed by force for a
    question with no player, and scenario_quota spends the player force's
    allowance for the hour. Sessions are keyed by scope, not force, so the
    session checks read the same either way."""
    qid = ask_via_remote(ctx.server_rcon, text, force=force, scope_lua=scope_lua)
    return qid, _answer_lines(ctx, qid)


def scenario_session_recall(ctx: Ctx) -> Tuple[Status, str]:
    """Phase 3 sessions (docs/design/phase3-spec.md part 1). The fake model
    answers "recall" with how many earlier exchanges the prompt carried and
    the first of them, which is the session seen from outside: a second
    question sees the first, "new" starts over, and "sessions" lists what is
    open without calling the model."""
    try:
        _ask_lines(ctx, "new what forces are there")
        _, recalled = _ask_lines(ctx, "recall")
        if "earlier=1" not in recalled or not any(l.startswith("first=what forces are there") for l in recalled):
            return "FAIL", "the follow-up did not see the first exchange: %r" % recalled
        _, listed = _ask_lines(ctx, "sessions")
        if not any("(default)" in l and "exchange" in l for l in listed):
            return "FAIL", "sessions did not list the default session: %r" % listed
        _, fresh = _ask_lines(ctx, "new recall")
        if "earlier=0" not in fresh:
            return "FAIL", "new did not start a fresh session: %r" % fresh
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    return "PASS", "follow-up saw 1 earlier exchange, sessions listed it, new started clean: %r" % listed


def scenario_named_session(ctx: Ctx) -> Tuple[Status, str]:
    """A #named session is its own transcript, shared by whoever uses the
    name, and the default session never sees it."""
    try:
        _ask_lines(ctx, "new #iron what forces are there")
        _, again = _ask_lines(ctx, "#iron recall")
        if "earlier=1" not in again or not any(l.startswith("first=what forces are there") for l in again):
            return "FAIL", "#iron did not carry its own exchange: %r" % again
        _, default = _ask_lines(ctx, "new recall")
        if "earlier=0" not in default:
            return "FAIL", "the default session saw #iron's exchanges: %r" % default
        _, listed = _ask_lines(ctx, "sessions")
        if not any("#iron" in l for l in listed):
            return "FAIL", "sessions did not list #iron: %r" % listed
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    return "PASS", "#iron carried 1 exchange, the default carried none: %r" % listed


def scenario_private_scope(ctx: Ctx) -> Tuple[Status, str]:
    """A question with a private scope (handed in through the interface's
    scope field, as a privacy mod would) polls with scope and private set,
    and its session never meets the global one."""
    private = '{key="team-x", private=true, audience={force="player"}, tag="[TEAM]"}'
    try:
        qid = ask_via_remote(ctx.server_rcon, "new what forces are there", force="enemy", scope_lua=private)
        row = aab_rpc(ctx.server_rcon, "poll", after=qid - 1, limit=1)
        rows = as_list(row.get("r"))
        if not rows or rows[0].get("scope") != "team-x" or rows[0].get("private") is not True:
            return "FAIL", "poll row lacks the private scope: %r" % row
        _answer_lines(ctx, qid)
        _, private_recall = _ask_lines(ctx, "recall", scope_lua=private)
        if "earlier=1" not in private_recall:
            return "FAIL", "the private session did not carry its exchange: %r" % private_recall
        _, global_recall = _ask_lines(ctx, "new recall")
        if "earlier=0" not in global_recall:
            return "FAIL", "the global session saw the private exchange: %r" % global_recall
        _, listed = _ask_lines(ctx, "sessions")
        if any("team" in l for l in listed):
            return "FAIL", "the global listing showed a private session: %r" % listed
    except (TimeoutError, RpcError) as e:
        return "FAIL", str(e)
    return "PASS", "qid=%d polled as scope=team-x private=true; its session stayed private" % qid


def scenario_labels(ctx: Ctx) -> Tuple[Status, str]:
    """The labels op (docs/design/phase3-spec.md, "Force labels") lists what
    players call each force, from any mod exposing force_labels_v1. The test
    provider labels the player force, with rich text the companion strips."""
    if ctx.provider_iface is None:
        return "SKIP", "no provider mod in this run"
    env = aab_rpc(ctx.server_rcon, "labels")
    if not env.get("ok"):
        return "FAIL", "labels op failed: %r" % env
    rows = as_list(env.get("r"))
    hit = [r for r in rows if r.get("name") == "player"]
    if not hit or hit[0].get("label") != "The Engineers":
        return "FAIL", "expected player labelled 'The Engineers', got %r" % rows
    forces = aab_rpc(ctx.server_rcon, "call", i="ai-agent-bridge-tools", f="list_forces", a={"force": "player"})
    listed = [f for f in as_list((forces.get("r") or {}).get("forces")) if f.get("name") == "player"]
    if not listed or listed[0].get("label") != "The Engineers":
        return "FAIL", "list_forces did not carry the label: %r" % forces
    return "PASS", "labels op and list_forces both say player is The Engineers"


SCENARIOS: List[Scenario] = [
    Scenario("status", scenario_status),
    Scenario("providers + manifest ops: companion's own provider", scenario_providers_and_manifest),
    Scenario("ask via remote interface: what forces are there", scenario_ask_forces),
    Scenario("ask hello: provider greeting", scenario_ask_hello),
    Scenario("ask: table of players", scenario_ask_table_of_players),
    Scenario("ask: what is in the research queue", scenario_research_queue),
    Scenario("ask: tech status of automation", scenario_tech_status),
    Scenario("ask: logistic bots on nauvis", scenario_logistics_summary),
    Scenario("ask: how many character on nauvis", scenario_entity_count),
    Scenario("ask: evolution on nauvis", scenario_evolution),
    Scenario("ask: rockets launched", scenario_rockets),
    Scenario("ask: how long have we played", scenario_game_time),
    Scenario("ask: pollution on nauvis", scenario_pollution),
    Scenario("ask: iron plate production since the start", scenario_production_since_start),
    Scenario("answer op: malformed artifact -> bad_artifact", scenario_answer_bad_artifact),
    Scenario("answer op: large table artifact accepted", scenario_answer_large_table),
    Scenario("chat prefix creates a question", scenario_chat_prefix, needs_client=True),
    Scenario("history: last death", scenario_last_death, needs_client=True),
    Scenario("per-player quota", scenario_quota),
    Scenario("provider error -> provider_error", scenario_provider_error),
    Scenario("sessions: follow-up, sessions, new", scenario_session_recall),
    Scenario("sessions: #named session", scenario_named_session),
    Scenario("chat scope: private question stays private", scenario_private_scope),
    Scenario("force labels: labels op and list_forces", scenario_labels),
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
