package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bits-orio/ai-agent-bridge/service/internal/agent"
	"github.com/bits-orio/ai-agent-bridge/service/internal/config"
	"github.com/bits-orio/ai-agent-bridge/service/internal/controlapi"
	"github.com/bits-orio/ai-agent-bridge/service/internal/history"
	"github.com/bits-orio/ai-agent-bridge/service/internal/model/fake"
	"github.com/bits-orio/ai-agent-bridge/service/internal/rpc"
	"github.com/bits-orio/ai-agent-bridge/service/internal/tools"
)

// fakeCompanion answers aab-rpc commands in memory: a question ring the poll op
// reads, a two-step catalog behind the providers and manifest ops, and an answer
// op that can be made to refuse. It is the companion's own contract, not a mock
// of the service: poll offers only questions that have not been answered, with an
// id above after, oldest first, at most limit of them.
type fakeCompanion struct {
	// tooLargeAbove, when set, makes poll refuse any page larger than it, the
	// way the companion refuses a reply over its byte cap.
	tooLargeAbove int
	pending       map[int64]string // question id to text

	// answerErr is the protocol error code every answer is refused with. Empty
	// means answers land.
	answerErr string
	// badShape is a shape the answer op refuses with bad_artifact, the way the
	// companion refuses an artifact it cannot render. Other shapes still land.
	badShape string

	// catalog is what the providers and tools ops list: interface name to the
	// names of the tools on it.
	catalog map[string][]string
	// providersErr and toolsErr refuse those ops with a protocol error code,
	// manifestErr refuses one named provider's manifest with one.
	providersErr string
	toolsErr     string
	manifestErr  map[string]string

	polls     int
	answers   int
	manifests int
	shapes    []string // the shape of every artifact the answer op accepted
}

func newFakeCompanion(texts map[int64]string) *fakeCompanion {
	if texts == nil {
		texts = map[int64]string{}
	}
	return &fakeCompanion{
		pending: texts,
		catalog: map[string][]string{"ai-agent-bridge-tools": {"list_forces"}},
	}
}

func (f *fakeCompanion) Execute(cmd string) (string, error) {
	body, ok := strings.CutPrefix(cmd, "/aab-rpc ")
	if !ok {
		return "", fmt.Errorf("not an aab-rpc command: %q", cmd)
	}
	var req struct {
		Op       string `json:"op"`
		After    int64  `json:"after"`
		Limit    int    `json:"limit"`
		QID      int64  `json:"qid"`
		I        string `json:"i"`
		Artifact struct {
			Shape string `json:"shape"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return `{"ok":false,"e":"bad_json","m":"unparseable"}`, nil
	}

	switch req.Op {
	case "status":
		return `{"ok":true,"r":{"protocol":1,"mod_version":"test","tick":1,"player_count":1,"pending":0,"ask_command":"ask","last_id":0}}`, nil
	case "providers":
		if f.providersErr != "" {
			return refusal(f.providersErr), nil
		}
		return f.providersReply(), nil
	case "manifest":
		f.manifests++
		if code := f.manifestErr[req.I]; code != "" {
			return refusal(code), nil
		}
		return f.manifestReply(req.I), nil
	case "tools":
		if f.toolsErr != "" {
			return refusal(f.toolsErr), nil
		}
		return f.toolsReply(), nil
	case "call":
		return `{"ok":true,"r":{"ok":true}}`, nil
	case "poll":
		f.polls++
		return f.pollReply(req.After, req.Limit), nil
	case "answer":
		f.answers++
		return f.answerReply(req.QID, req.Artifact.Shape), nil
	}
	return `{"ok":false,"e":"bad_op","m":"unknown op"}`, nil
}

func refusal(code string) string {
	return fmt.Sprintf(`{"ok":false,"e":%q,"m":"refused"}`, code)
}

func (f *fakeCompanion) ifaces() []string {
	names := make([]string, 0, len(f.catalog))
	for iface := range f.catalog {
		names = append(names, iface)
	}
	sort.Strings(names)
	return names
}

func (f *fakeCompanion) providersReply() string {
	rows := make([]string, 0, len(f.catalog))
	for _, iface := range f.ifaces() {
		names, _ := json.Marshal(f.catalog[iface])
		rows = append(rows, fmt.Sprintf(`{"iface":%q,"v":1,"tools":%s}`, iface, names))
	}
	return `{"ok":true,"r":[` + strings.Join(rows, ",") + `]}`
}

func (f *fakeCompanion) manifestReply(iface string) string {
	names, known := f.catalog[iface]
	if !known {
		return refusal(rpc.CodeNoProvider)
	}
	return `{"ok":true,"r":{"v":1,"tools":` + manifestTools(names) + `}}`
}

// toolsReply is the whole catalog in one reply. A provider whose manifest does
// not fit makes the whole reply not fit, which is the failure the two-step
// providers and manifest ops exist for: one verbose mod used to cost every other
// mod on the server every tool it had.
func (f *fakeCompanion) toolsReply() string {
	for iface := range f.catalog {
		if f.manifestErr[iface] != "" {
			return refusal(rpc.CodeTooLarge)
		}
	}
	rows := make([]string, 0, len(f.catalog))
	for _, iface := range f.ifaces() {
		rows = append(rows, fmt.Sprintf(`{"iface":%q,"v":1,"tools":%s}`, iface, manifestTools(f.catalog[iface])))
	}
	return `{"ok":true,"r":[` + strings.Join(rows, ",") + `]}`
}

func manifestTools(names []string) string {
	entries := make([]string, 0, len(names))
	for _, fn := range names {
		entries = append(entries, fmt.Sprintf(`%q:{"desc":"One bounded read."}`, fn))
	}
	return `{` + strings.Join(entries, ",") + `}`
}

func (f *fakeCompanion) pollReply(after int64, limit int) string {
	if f.tooLargeAbove > 0 && limit > f.tooLargeAbove {
		return `{"ok":false,"e":"too_large","m":"result exceeds the reply cap"}`
	}
	ids := make([]int64, 0, len(f.pending))
	for id := range f.pending {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	rows := make([]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"text":%q,"player_index":1,"player_name":"Bob","force":"player","tick":100}`, id, f.pending[id]))
	}
	return `{"ok":true,"r":[` + strings.Join(rows, ",") + `]}`
}

func (f *fakeCompanion) answerReply(qid int64, shape string) string {
	if f.answerErr != "" {
		return refusal(f.answerErr)
	}
	if f.badShape != "" && shape == f.badShape {
		return refusal(rpc.CodeBadArtifact)
	}
	f.shapes = append(f.shapes, shape)
	delete(f.pending, qid)
	return `{"ok":true,"r":true}`
}

func newTestRunner(t *testing.T, companion *fakeCompanion) *runner {
	t.Helper()
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	client := rpc.New(companion)
	return &runner{
		cfg:      &config.Config{},
		rpc:      client,
		caller:   &toolCaller{client: client},
		store:    store,
		stats:    controlapi.NewStats(fake.ModelID, time.Now()),
		agent:    agent.New(fake.New(), agent.Caps{MaxRounds: 2}),
		inFlight: map[int64]*delivery{},
	}
}

// captureLog collects the log for a test to read, because some of what the
// service promises an operator is a line in the log and nothing else.
func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	var written strings.Builder
	flags := log.Flags()
	log.SetOutput(&written)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})
	return &written
}

func toolNames(list []tools.Tool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Name)
	}
	return names
}

func hasToolNamed(list []tools.Tool, fn string) bool {
	for _, name := range toolNames(list) {
		if strings.Contains(name, fn) {
			return true
		}
	}
	return false
}

// questionRing is n pending questions with ids 1..n, the text chosen so the
// scripted model answers at once rather than calling a tool.
func questionRing(n int) map[int64]string {
	texts := map[int64]string{}
	for id := int64(1); id <= int64(n); id++ {
		texts[id] = "ping"
	}
	return texts
}
