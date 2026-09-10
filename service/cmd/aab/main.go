// Command aab is the service half of AI Agent Bridge (CONTEXT.md): one process per
// Factorio server, driving the companion mod's aab-rpc-v1 protocol over RCON.
//
// "aab run" is the service itself. The other four subcommands exercise the transport by
// hand, which is what the Phase 0 checks in TESTING.md use.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rcon"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
)

func main() {
	cfgPath := flag.String("config", "aab.yaml", "path to config file")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]

	// Flags may also follow the subcommand, the way the Go tool takes them, so both
	// "aab -config aab.yaml run" and "aab run -config aab.yaml" mean the same thing.
	subFlags := flag.NewFlagSet(sub, flag.ExitOnError)
	subFlags.StringVar(cfgPath, "config", *cfgPath, "path to config file")
	subFlags.Usage = usage
	_ = subFlags.Parse(rest)
	rest = subFlags.Args()

	// Load a .env sitting next to the config file (the setup wizard writes one there) so
	// the service's secrets are available without the caller having to `source` it first.
	// Real environment variables always win.
	loadDotEnv(filepath.Dir(*cfgPath))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	setupLogFile(cfg)

	rc := rcon.New(cfg.Factorio.RCON.Address, cfg.Factorio.RCON.Password)
	defer rc.Close()
	client := rpc.New(rc)
	ctx := context.Background()

	switch sub {
	case "run":
		runService(cfg, client)
	case "status":
		runStatus(ctx, client)
	case "probe":
		runProbe(ctx, client)
	case "rpc":
		runRPC(ctx, client, rest)
	case "poll":
		runPoll(ctx, client, rest)
	default:
		fmt.Fprintf(os.Stderr, "aab: unknown subcommand %q\n\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: aab [-config path] <subcommand> [args]

subcommands:
  run             run the service: poll for questions, answer them with the agent
                  loop, serve the control API. This is the one you leave running
  status          print the companion's status op reply (protocol/mod version, tick,
                  player count, pending question count)
  probe           print the companion's tools op catalog (every provider's manifest)
  rpc <json>      send one raw aab-rpc-v1 request, a JSON object including its own
                  "op" field, e.g. aab rpc '{"op":"poll","after":0}'; print the reply
  poll [after]    print questions with id greater than after (default: 0, everything
                  still pending)

flags:
`)
	flag.PrintDefaults()
}

func runStatus(ctx context.Context, c *rpc.Client) {
	st, err := c.Status(ctx)
	fatalOnRPCError("status", err)
	printJSON(st)
}

func runProbe(ctx context.Context, c *rpc.Client) {
	tools, err := c.Tools(ctx)
	fatalOnRPCError("probe", err)
	printJSON(tools)
}

// runRPC sends args[0], a JSON object that must carry its own "op" field, as one raw
// aab-rpc-v1 request. This is the escape hatch for exercising ops the typed helpers don't
// cover yet, and for the Phase 0 dev-rig checks in PLAN.md.
func runRPC(ctx context.Context, c *rpc.Client, args []string) {
	if len(args) != 1 {
		log.Fatal(`usage: aab rpc '<json>'  (a JSON object including its own "op" field)`)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(args[0]), &payload); err != nil {
		log.Fatalf("rpc: payload is not a JSON object: %v", err)
	}
	op, _ := payload["op"].(string)
	if op == "" {
		log.Fatal(`rpc: payload must include a string "op" field`)
	}
	raw, err := c.Call(ctx, op, payload)
	fatalOnRPCError(op, err)
	fmt.Println(string(raw))
}

func runPoll(ctx context.Context, c *rpc.Client, args []string) {
	var after int64
	if len(args) > 0 {
		n, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			log.Fatalf("poll: invalid after cursor %q: %v", args[0], err)
		}
		after = n
	}
	qs, err := c.Poll(ctx, after, 0)
	fatalOnRPCError("poll", err)
	printJSON(qs)
}

func fatalOnRPCError(op string, err error) {
	if err != nil {
		log.Fatalf("aab-rpc %s: %v", op, err)
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatalf("encode reply: %v", err)
	}
	fmt.Println(string(b))
}
