# AI Agent Bridge
# No Go toolchain or luac assumed on the host: service builds/tests run
# inside golang:1.25-alpine via Docker; `lua-check` uses luac5.4 the way
# .github/workflows/ci.yml does, so it needs luac5.4 on PATH (apt install
# lua5.4 on Debian/Ubuntu, or run the ci.yml job).

SERVICE_DIR := service
GO_IMAGE := golang:1.25-alpine
GO_DOCKER := docker run --rm -v "$(CURDIR)/$(SERVICE_DIR)":/src -w /src -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 -v aab-gomod:/go/pkg/mod $(GO_IMAGE)

.PHONY: service test e2e e2e-client e2e-stop lua-check

## Build service/aab through Docker.
service:
	$(GO_DOCKER) go build -o aab ./cmd/aab

## Go unit tests (gofmt -l, go vet, go test), all inside Docker.
test:
	$(GO_DOCKER) sh -c "gofmt -l . ; go vet ./... && go test ./..."

## Full e2e harness: rig server + service, no player.
e2e:
	python3 tests/e2e/run.py

## Full e2e harness with a standalone client, for the player-only scenarios.
e2e-client:
	python3 tests/e2e/run.py --client

## Kill the server/client/service a `python3 tests/e2e/run.py --keep` run
## left behind, reading their PIDs from tests/e2e/.run/pids. Safe to run
## with nothing to stop.
e2e-stop:
	python3 tests/e2e/run.py --stop

## Lua syntax check, mirrors .github/workflows/ci.yml's companion-mod job.
lua-check:
	@files=$$(find companion-mod tests/provider-mod -name '*.lua' 2>/dev/null); \
	if [ -z "$$files" ]; then \
		echo "no .lua files found, skipping"; \
	else \
		luac5.4 -p $$files && echo "luac -p OK ($$(echo "$$files" | wc -l) file(s))"; \
	fi

## Companion protocol tests against the fake game (needs liblua5.4, no Factorio).
## Both files always run: make stops a recipe at its first failing line, so a
## red aab_test.lua used to mean aab_breadth_test.lua was never invoked at all
## and its own failures went unseen. Run both, then fail if either did.
lua-test:
	@rc=0; \
	for f in tests/lua/aab_*test.lua; do \
		echo "== $$f"; \
		python3 tests/lua/luarun.py "$$f" || rc=1; \
	done; \
	exit $$rc
