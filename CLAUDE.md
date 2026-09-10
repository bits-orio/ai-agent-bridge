# AI Agent Bridge: working notes for agents

Read `CONTEXT.md` first, then `PLAN.md`. Use the domain words exactly as
`CONTEXT.md` defines them.

- The companion never names another mod. There is no team concept here; say
  force, and keep multi-force generic.
- The protocol `aab-rpc-v1`, the interface `ai-agent-bridge-v1` and the probe
  `agent_tools_v1` are frozen. Add, never change.
- Lua: small files under `companion-mod/scripts/`, one concern each. No
  `on_tick`. Events over polling. Verify every Factorio API name against the
  official docs or a sibling mod before using it. `luac5.4 -p` must pass.
- Go: `service/` is a module; `go vet ./... && go test ./... && go build ./...`
  must pass. Packages copied from Open Discord Bridge keep their shape.
- Docs voice: second person, concrete, short sentences, no em-dashes, at most
  three emoji on the portal page, the AI disclosure sentence only in the
  Development section.
- Version lives in `companion-mod/info.json`; the release tag is `v<version>`.
  Do not bump it unless asked.
