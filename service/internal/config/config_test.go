// Adapted from open-discord-bridge/bridge/internal/config/config_test.go: same coverage
// shape (env-mode load, defaults, path expansion, validation failure), retargeted at
// AAB's field set (no Discord routes/commands/admins; adds Anthropic model/rounds/tokens).

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setEnvMode(t *testing.T) {
	t.Helper()
	t.Setenv("AAB_RCON_ADDRESS", "game:27015")
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
	t.Setenv("AAB_EVENTS_FILE", "/tmp/events.jsonl")
}

func TestLoadFromEnvDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.Transport != "local" || c.Factorio.RCON.Address != "game:27015" {
		t.Fatalf("unexpected: %+v", c.Factorio)
	}
	if c.Factorio.RCON.Password != "pw" {
		t.Fatalf("secret not resolved")
	}
	if c.Model.ID != "deepseek/deepseek-v4-pro-0813" {
		t.Errorf("Anthropic.Model = %q, want default deepseek/deepseek-v4-pro-0813", c.Model.ID)
	}
	if c.Agent.MaxRounds != defaultMaxRounds {
		t.Errorf("MaxRounds = %d, want default %d", c.Agent.MaxRounds, defaultMaxRounds)
	}
	if c.Agent.MaxTokensPerQuestion != defaultMaxTokensPerQuestion {
		t.Errorf("MaxTokensPerQuestion = %d, want default %d", c.Agent.MaxTokensPerQuestion, defaultMaxTokensPerQuestion)
	}
	if c.Interval().String() != "1s" {
		t.Errorf("Interval() = %s, want 1s", c.Interval())
	}
}

func TestLoadFromEnvOverrides(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_MODEL", "deepseek/deepseek-v4-pro-0813")
	t.Setenv("AAB_MAX_ROUNDS", "12")
	t.Setenv("AAB_MAX_TOKENS_PER_QUESTION", "8000")
	t.Setenv("AAB_POLL_INTERVAL", "500ms")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.Model.ID != "deepseek/deepseek-v4-pro-0813" {
		t.Errorf("Model = %q, want override", c.Model.ID)
	}
	if c.Agent.MaxRounds != 12 {
		t.Errorf("MaxRounds = %d, want 12", c.Agent.MaxRounds)
	}
	if c.Agent.MaxTokensPerQuestion != 8000 {
		t.Errorf("MaxTokensPerQuestion = %d, want 8000", c.Agent.MaxTokensPerQuestion)
	}
	if c.Interval().String() != "500ms" {
		t.Errorf("Interval() = %s, want 500ms", c.Interval())
	}
	if c.Anthropic.APIKey != "sk-ant-test" {
		t.Errorf("APIKey not resolved from ANTHROPIC_API_KEY")
	}
}

func TestLoadFromEnvInvalidMaxRounds(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_MAX_ROUNDS", "not-a-number")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected error for invalid AAB_MAX_ROUNDS")
	}
}

func TestLoadFromEnvMissingRCONPasswordFails(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AAB_RCON_ADDRESS", "game:27015")
	t.Setenv("AAB_EVENTS_FILE", "/tmp/events.jsonl")
	t.Setenv("FACTORIO_RCON_PASSWORD", "") // missing

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected error for missing RCON password")
	}
}

func TestLoadFromEnvMissingEventsFileFails(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AAB_RCON_ADDRESS", "game:27015")
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
	t.Setenv("AAB_EVENTS_FILE", "") // missing

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected error for missing events_file")
	}
}

func TestLoadExpandsSFTPAndLogPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AAB_TEST_HOME", dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := `
transport: sftp
factorio:
  rcon:
    address: "game:27015"
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /tmp/events.jsonl
  sftp:
    host: "example.com:22"
    user: bob
    key_path: "${AAB_TEST_HOME}/id_rsa"
    known_hosts_path: "${AAB_TEST_HOME}/known_hosts"
log_file: "${AAB_TEST_HOME}/aab.log"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := filepath.Join(dir, "id_rsa"); c.Factorio.SFTP.KeyPath != want {
		t.Errorf("sftp.key_path not expanded: got %q, want %q", c.Factorio.SFTP.KeyPath, want)
	}
	if want := filepath.Join(dir, "known_hosts"); c.Factorio.SFTP.KnownHostsPath != want {
		t.Errorf("sftp.known_hosts_path not expanded: got %q, want %q", c.Factorio.SFTP.KnownHostsPath, want)
	}
	if want := filepath.Join(dir, "aab.log"); c.LogFile != want {
		t.Errorf("log_file not expanded: got %q, want %q", c.LogFile, want)
	}
}

func TestForcedEnvModeIgnoresConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	setEnvMode(t)
	t.Setenv("AAB_CONFIG", "none")

	cfgFile := filepath.Join(dir, "aab.yaml")
	if err := os.WriteFile(cfgFile, []byte("factorio:\n  events_file: /from/file.jsonl\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("forced env mode: %v", err)
	}
	if c.Factorio.EventsFile != "/tmp/events.jsonl" {
		t.Fatalf("config came from the file, not env: %+v", c.Factorio)
	}
}

func TestLoadFileParsesTheAgentSection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
	t.Setenv("AAB_CONTROL_TOKEN", "control-secret")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := `
factorio:
  rcon:
    address: "game:27015"
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /tmp/events.jsonl
model:
  provider: fake
  id: fake
agent:
  max_rounds: 4
  max_tokens_per_question: 1234
  max_round_tool_result_bytes: 30000
  session_idle: 90s
  clarify_idle: 15m
  session_max_bytes: 4000
  questions_per_player_per_hour: 3
history:
  path: history.sqlite
control_api:
  addr: 127.0.0.1:9999
  token_env: AAB_CONTROL_TOKEN
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Agent.MaxRounds != 4 || c.Agent.MaxTokensPerQuestion != 1234 {
		t.Errorf("agent caps = %+v", c.Agent)
	}
	if c.SessionIdle().String() != "1m30s" || c.Agent.SessionMaxBytes != 4000 {
		t.Errorf("session caps = %+v, want idle 1m30s and 4000 bytes", c.Agent)
	}
	if c.ClarifyIdle() != 15*time.Minute {
		t.Errorf("clarify_idle = %s, want 15m", c.ClarifyIdle())
	}
	if c.Agent.MaxRoundToolResultBytes != 30000 {
		t.Errorf("max_round_tool_result_bytes = %d, want 30000", c.Agent.MaxRoundToolResultBytes)
	}
	if c.NamedSessionIdle() != 30*time.Minute || c.Agent.SessionMaxExchanges != 10 {
		t.Errorf("unset session caps must default: %+v", c.Agent)
	}
	if c.Agent.QuestionsPerPlayerPerHour != 3 {
		t.Errorf("quota = %d, want 3", c.Agent.QuestionsPerPlayerPerHour)
	}
	if want := filepath.Join(dir, "history.sqlite"); c.History.Path != want {
		t.Errorf("history.path = %q, want it resolved against the config file as %q", c.History.Path, want)
	}
	if c.ControlAPI.Addr != "127.0.0.1:9999" || c.ControlAPI.Token != "control-secret" {
		t.Errorf("control api = %+v", c.ControlAPI)
	}
	if c.Model.ID != "fake" {
		t.Errorf("model = %q, want fake", c.Model.ID)
	}
}

func TestAgentDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.Agent.MaxRounds != 6 || c.Agent.MaxTokensPerQuestion != 20000 {
		t.Errorf("agent caps = %+v, want 6 rounds and 20000 tokens", c.Agent)
	}
	if c.SessionIdle() != 3*time.Minute || c.NamedSessionIdle() != 30*time.Minute {
		t.Errorf("session idle defaults = %s and %s, want 3m and 30m", c.SessionIdle(), c.NamedSessionIdle())
	}
	if c.ClarifyIdle() != 10*time.Minute {
		t.Errorf("clarify_idle default = %s, want 10m", c.ClarifyIdle())
	}
	if c.Agent.MaxRoundToolResultBytes != 24000 {
		t.Errorf("max_round_tool_result_bytes default = %d, want 24000", c.Agent.MaxRoundToolResultBytes)
	}
	if c.Agent.QuestionsPerPlayerPerHour != 20 {
		t.Errorf("quota = %d, want 20", c.Agent.QuestionsPerPlayerPerHour)
	}
	if c.Agent.QuestionsPerHour != 120 || c.Agent.MaxCostPerDay != 5.0 || c.Agent.MaxToolCalls != 30 {
		t.Errorf("safety defaults = %+v, want 120 an hour, $5 a day, 12 tool calls", c.Agent)
	}
	if c.History.Path != "history.sqlite" {
		t.Errorf("history.path = %q, want history.sqlite beside the working directory", c.History.Path)
	}
	if c.ControlAPI.Addr != "127.0.0.1:8090" || c.ControlAPI.TokenEnv != "AAB_CONTROL_TOKEN" {
		t.Errorf("control api = %+v", c.ControlAPI)
	}
}

func TestLoadFromEnvAgentOverrides(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_SESSION_IDLE", "45s")
	t.Setenv("AAB_CLARIFY_IDLE", "2m")
	t.Setenv("AAB_MAX_ROUND_TOOL_RESULT_BYTES", "12000")
	t.Setenv("AAB_QUESTIONS_PER_PLAYER_PER_HOUR", "-1")
	t.Setenv("AAB_HISTORY_PATH", "/var/lib/aab/history.sqlite")
	t.Setenv("AAB_CONTROL_ADDR", "0.0.0.0:9000")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.SessionIdle().String() != "45s" {
		t.Errorf("session_idle = %s, want 45s", c.SessionIdle())
	}
	if c.ClarifyIdle().String() != "2m0s" {
		t.Errorf("clarify_idle = %s, want 2m0s", c.ClarifyIdle())
	}
	if c.Agent.MaxRoundToolResultBytes != 12000 {
		t.Errorf("max_round_tool_result_bytes = %d, want 12000", c.Agent.MaxRoundToolResultBytes)
	}
	// A negative quota is a deliberate "no quota" and must survive the defaults.
	if c.Agent.QuestionsPerPlayerPerHour != -1 {
		t.Errorf("quota = %d, want -1", c.Agent.QuestionsPerPlayerPerHour)
	}
	if c.History.Path != "/var/lib/aab/history.sqlite" {
		t.Errorf("history.path = %q", c.History.Path)
	}
	if c.ControlAPI.Addr != "0.0.0.0:9000" {
		t.Errorf("control addr = %q", c.ControlAPI.Addr)
	}
}

func TestLoadFromEnvInvalidSessionIdle(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_SESSION_IDLE", "ten minutes")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_SESSION_IDLE")
	}
}

func TestLoadFromEnvInvalidClarifyIdle(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_CLARIFY_IDLE", "ten minutes")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_CLARIFY_IDLE")
	}
}

func TestLoadFromEnvInvalidMaxRoundToolResultBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_MAX_ROUND_TOOL_RESULT_BYTES", "not a number")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_MAX_ROUND_TOOL_RESULT_BYTES")
	}
}

// The ledger is on by default (it holds player names and question text, so
// an operator turns it off explicitly, never the other way around), and its
// dir falls back to the working directory when there is no config file to
// anchor it to.
func TestLedgerDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if !c.LedgerEnabled() {
		t.Error("LedgerEnabled() = false, want true by default")
	}
	if c.Ledger.Enabled == nil || !*c.Ledger.Enabled {
		t.Errorf("Ledger.Enabled = %v, want a resolved true so the effective-config dump shows it plainly", c.Ledger.Enabled)
	}
	if c.Ledger.Dir != "." {
		t.Errorf("Ledger.Dir = %q, want %q (no config file to anchor to)", c.Ledger.Dir, ".")
	}
}

// In file mode, ledger.dir is resolved against the config file the same way
// history.path already is, and an explicit enabled: false must survive
// (must not be reinterpreted as "unset").
func TestLoadFileParsesTheLedgerSection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := `
factorio:
  rcon:
    address: "game:27015"
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /tmp/events.jsonl
ledger:
  enabled: false
  dir: ledgers
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.LedgerEnabled() {
		t.Error("LedgerEnabled() = true, want false: the config said enabled: false")
	}
	if want := filepath.Join(dir, "ledgers"); c.Ledger.Dir != want {
		t.Errorf("Ledger.Dir = %q, want it resolved against the config file as %q", c.Ledger.Dir, want)
	}
}

// A config file that says nothing about the ledger still gets the default
// dir (the directory holding the config file), not the working directory.
func TestLoadFileDefaultsLedgerDirToConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir()) // a different cwd than the config file's own directory
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := "factorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /tmp/events.jsonl\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.LedgerEnabled() {
		t.Error("LedgerEnabled() = false, want true: enabled was left unset")
	}
	if c.Ledger.Dir != dir {
		t.Errorf("Ledger.Dir = %q, want %q (the config file's own directory)", c.Ledger.Dir, dir)
	}
}

func TestLoadFromEnvLedgerOverrides(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_LEDGER_ENABLED", "false")
	t.Setenv("AAB_LEDGER_DIR", "/var/lib/aab/ledger")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.LedgerEnabled() {
		t.Error("LedgerEnabled() = true, want false")
	}
	if c.Ledger.Dir != "/var/lib/aab/ledger" {
		t.Errorf("Ledger.Dir = %q, want /var/lib/aab/ledger", c.Ledger.Dir)
	}
}

// AAB_LEDGER_DIR is a path the same way AAB_LOG_FILE and AAB_EVENTS_FILE are, so it
// expands ${VAR} and a leading ~/ the same way those two do.
func TestLoadFromEnvLedgerDirExpandsPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir()) // a different cwd, so a relative result here couldn't pass by accident
	setEnvMode(t)
	t.Setenv("AAB_TEST_HOME", dir)
	t.Setenv("AAB_LEDGER_DIR", "${AAB_TEST_HOME}/ledger")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if want := filepath.Join(dir, "ledger"); c.Ledger.Dir != want {
		t.Errorf("Ledger.Dir = %q, want %q (AAB_LEDGER_DIR expanded)", c.Ledger.Dir, want)
	}
}

func TestLoadFromEnvInvalidLedgerEnabled(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_LEDGER_ENABLED", "not-a-bool")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_LEDGER_ENABLED")
	}
}

// The briefing is on by default, the same "unset means on" rule the ledger
// already follows (docs/design/phase4-spec.md section 19: briefing default on).
func TestBriefingDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if !c.BriefingEnabled() {
		t.Error("BriefingEnabled() = false, want true by default")
	}
	if c.Briefing.Enabled == nil || !*c.Briefing.Enabled {
		t.Errorf("Briefing.Enabled = %v, want a resolved true so the effective-config dump shows it plainly", c.Briefing.Enabled)
	}
}

// An explicit enabled: false must survive (must not be reinterpreted as
// "unset").
func TestLoadFileParsesTheBriefingSection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := `
factorio:
  rcon:
    address: "game:27015"
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /tmp/events.jsonl
briefing:
  enabled: false
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.BriefingEnabled() {
		t.Error("BriefingEnabled() = true, want false: the config said enabled: false")
	}
}

// A config file that says nothing about the briefing still resolves to on.
func TestLoadFileDefaultsBriefingToOn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgPath := filepath.Join(dir, "aab.yaml")
	yaml := "factorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /tmp/events.jsonl\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.BriefingEnabled() {
		t.Error("BriefingEnabled() = false, want true: enabled was left unset")
	}
}

func TestLoadFromEnvBriefingOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_BRIEFING_ENABLED", "false")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.BriefingEnabled() {
		t.Error("BriefingEnabled() = true, want false")
	}
}

func TestLoadFromEnvInvalidBriefingEnabled(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_BRIEFING_ENABLED", "not-a-bool")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_BRIEFING_ENABLED")
	}
}
