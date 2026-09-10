-- A provider, and nothing else. It exists so the end-to-end harness can prove
-- two things about a mod the companion has never heard of: that its tools turn
-- up in the catalog through the agent_tools_v1 probe, and that an error inside
-- one of its tools comes back as a provider_error rather than taking anything
-- down with it.
--
-- This is the whole of what a provider has to write (CONTEXT.md "Provider"):
-- one zero-argument probe on an interface it already owns, and one plain
-- function per tool. No dependency on the companion in either direction, no
-- registration, nothing stored.

local INTERFACE = "aab-test-provider"

local MANIFEST = {
  hello = {
    desc = "Greets someone by name. A tool that exists only to prove a third mod's tools reach the agent.",
    params = {
      name = "string! who to greet",
    },
  },
  boom = {
    desc = "Always fails. Used to check that an error inside a provider is reported and never crashes the caller.",
  },
}

-- `force` is injected into every tool's argument table by the service and is
-- ignored here, which is exactly what a provider that does not care about
-- force should do.
local function hello(args)
  local name = type(args) == "table" and args.name or nil
  if type(name) ~= "string" or name == "" then
    name = "stranger"
  end
  return { greeting = "hello " .. name .. ", from the test provider" }
end

local function boom()
  error("boom from the test provider")
end

remote.add_interface(INTERFACE, {
  agent_tools_v1 = function() return { v = 1, tools = MANIFEST } end,
  hello = hello,
  boom = boom,
})
