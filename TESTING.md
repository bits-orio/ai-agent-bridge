# Verification Checklist

A concrete pass to run against a live server. Tick as you go, and record what actually
happened, not what you expect to happen (see `feedback_empirical_diagnosis` in the
house rules: verify with in-game console tests, don't theorize about engine internals).

## 1. Phase 0, transport checks (PLAN.md)

The six checks PLAN.md's Phase 0 rests the whole design on. None of them need the
companion mod built: they test what the bare engine does over RCON. Run them against
the dev rig (`~/factorio-dev/rig`, see its `README.md`), for example:

```sh
~/factorio-dev/rig/start-server.sh 2.0 aab-p0 34199 27099
```

`start-server.sh` prints the RCON port once it's up; substitute it for `<rcon-port>`
below. Every command is sent with the rig's own client, whose first argument is that
port and whose password is always `rig`:

```sh
python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig '<command>'
```

- [x] **1.1 `rcon.print` returns to the caller**

  ```sh
  python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig '/sc rcon.print("aab-test-1 ok")'
  ```

  Expected: the reply body is exactly `aab-test-1 ok`.

  Result: PASS 2026-09-10 on 2.0.77 headless (rig `aab-p0`): reply body was exactly `aab-test-1 ok`.

- [x] **1.2 `player_index` is nil for RCON-invoked commands**

  ```sh
  python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig \
    '/sc commands.add_command("aab_test_pi", nil, function(cmd) rcon.print("player_index=" .. tostring(cmd.player_index)) end)' \
    '/aab_test_pi'
  ```

  Expected: the reply to the second command is `player_index=nil`. The Lua API docs
  document this for the server console only; this confirms it also holds for a command
  invoked directly by name over RCON, which is how the `aab-rpc` command will be called.

  Result: PASS 2026-09-10 on 2.0.77: reply was `player_index=nil name=aab_test_pi tick=62`; `name` and `tick` are populated, `player_index` is nil.

- [x] **1.3 a storage write and `raise_event` inside an RCON command replicate to a second client**

  Connect two Factorio clients to the rig server first (`--mp-connect 127.0.0.1:34199`
  or Play > Multiplayer > Connect to address), then run:

  ```sh
  python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig \
    '/sc storage.aab_test3 = (storage.aab_test3 or 0) + 1; local id = storage.aab_test3_event or script.generate_event_name(); storage.aab_test3_event = id; script.on_event(id, function(e) game.print("aab-test-3 replicated, counter=" .. storage.aab_test3) end); script.raise_event(id, {}); rcon.print("raised, counter=" .. storage.aab_test3)'
  ```

  Expected: `aab-test-3 replicated, counter=1` appears in **both** connected clients'
  chat, not only the one nearest the server, and the RCON reply reads
  `raised, counter=1`. Re-running the same command should show `counter=2` on both.

  Result: PASS 2026-09-10 on 2.0.77 with one standalone client connected (player_count 1): the /sc command above ran twice (counter 1, then 2) and the companion's own `write` op ran twice (counter 1, then 2), all four raising an event from inside an RCON command. Fifteen seconds later the client log had no Desync line, the client was still InGame and the server still reported it connected. A state that did not replicate would have produced a desync report. Still to see with human eyes: the `aab-test-3 replicated` chat line on a second client while it runs.

  Procedure finding: editing any companion Lua file while a server or client is up makes the next join fail with `multiplayer.script-mismatch` (the control.lua checksum differs). After any edit, restart the server and every client.

- [x] **1.4 `pcall(remote.call, ...)` catches a provider error**

  ```sh
  python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig \
    '/sc remote.add_interface("aab_test4", { boom = function() error("boom") end }); local ok, err = pcall(remote.call, "aab_test4", "boom"); rcon.print("ok=" .. tostring(ok) .. " err=" .. tostring(err))'
  ```

  Expected: the reply reads `ok=false err=...` with `boom` somewhere in the message,
  and the RCON connection and the server both stay up (a provider's error must not
  crash the `call` operation).

  Result: PASS 2026-09-10 on 2.0.77: `pcall(remote.call, "aab_t4", "boom")` returned false with the message `Error when running interface function aab_t4.boom: ... boom from provider` followed by a Lua traceback. The message carries a full traceback, so the rpc layer should keep only its first line for the `m` field.

- [x] **1.5 the reply size at which RCON truncates**

  Run with increasing `N` until the reply comes back shorter than requested, start
  with 4096, then double each time (8192, 16384, 32768, 65536, 131072):

  ```sh
  python3 ~/factorio-dev/rig/rcon.py <rcon-port> rig '/sc rcon.print(string.rep("a", N))'
  ```

  (substitute the number for `N`). Compare the reply's byte length against `N` for
  each run to bisect the cutoff.

  Expected/record: the byte count at which the reply first comes back short, and
  whether it's silently truncated, dropped entirely, or the connection errors. PLAN.md
  sets the RCON cap at half whatever this measures.

  Result: PASS 2026-09-10 on 2.0.77: replies of 4,000, 16,384, 65,536, 262,144, 1,048,576 and 4,194,304 bytes all arrived complete in a single RCON packet, byte-exact. No ceiling found up to 4 MB, so the companion's byte cap is a token-budget choice, not a transport limit.

- [x] **1.7 the companion's own rpc command, first load**

  Server: rig `aab-p0` on 2.0.77 with `mods-aab` (base + ai-agent-bridge 0.1.0).

  Result: PASS 2026-09-10. The mod loaded on the headless server and on the Steam
  client with the same checksum (4053880571). `status`, `ping`, `pcall_test`, `write`,
  `poll`, `tools`, `call` (list_forces, current_research), `no_provider`, `bad_version`
  and `bad_json` all replied as PLAN.md specifies. `big` with kb=100 returned 102,455
  bytes intact. Two findings: an empty `poll` list encodes as `{}` (Lua cannot tell an
  empty array from an empty object), so the service must accept `{}` as empty; and
  `provider_error` messages carried a full traceback, now trimmed to the first line.

- [ ] **1.6 single-player RCON access**

  `local-rcon-socket` is not a launch flag: `factorio --help` on both the 2.0.72 and the
  2.1.17 binaries in `~/factorio-dev` lists only `--rcon-port`, `--rcon-bind` and
  `--rcon-password`. Another portal mod's setup notes describe `local-rcon-socket` and
  `local-rcon-password` as entries in the client's settings under "The rest". First
  check: open that tab in the graphical client and record whether the entries exist.

  What actually answers the underlying question, whether an operator with no dedicated
  server can still drive the protocol locally, is whether a normal graphical client
  launch (not `--start-server`) accepts the same RCON flags:

  ```sh
  factorio --rcon-port <rcon-port> --rcon-password rig
  ```

  launched as the normal client (loading or starting a single-player game), then
  checking its log for a `Starting RCON interface` line the way the headless server's
  log shows one, and confirming `rcon.py` can reach it while the single-player game is
  running.

  Expected/record: whether RCON comes up for a plain single-player client launch, and
  under what flags. Note explicitly that `local-rcon-socket` itself was not found.

  Result:
