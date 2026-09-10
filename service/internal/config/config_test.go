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
	if c.Anthropic.Model != "claude-opus-5" {
		t.Errorf("Anthropic.Model = %q, want default claude-opus-5", c.Anthropic.Model)
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
	t.Setenv("AAB_MODEL", "claude-sonnet-5")
	t.Setenv("AAB_MAX_ROUNDS", "12")
	t.Setenv("AAB_MAX_TOKENS_PER_QUESTION", "8000")
	t.Setenv("AAB_POLL_INTERVAL", "500ms")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.Anthropic.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want override", c.Anthropic.Model)
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
anthropic:
  api_key_env: ANTHROPIC_API_KEY
  model: fake
agent:
  max_rounds: 4
  max_tokens_per_question: 1234
  memory_ttl: 90s
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
	if c.MemoryTTL().String() != "1m30s" {
		t.Errorf("memory_ttl = %s, want 1m30s", c.MemoryTTL())
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
	if c.Anthropic.Model != "fake" {
		t.Errorf("model = %q, want fake", c.Anthropic.Model)
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
	if c.MemoryTTL() != 10*time.Minute {
		t.Errorf("memory_ttl = %s, want 10m", c.MemoryTTL())
	}
	if c.Agent.QuestionsPerPlayerPerHour != 20 {
		t.Errorf("quota = %d, want 20", c.Agent.QuestionsPerPlayerPerHour)
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
	t.Setenv("AAB_MEMORY_TTL", "45s")
	t.Setenv("AAB_QUESTIONS_PER_PLAYER_PER_HOUR", "-1")
	t.Setenv("AAB_HISTORY_PATH", "/var/lib/aab/history.sqlite")
	t.Setenv("AAB_CONTROL_ADDR", "0.0.0.0:9000")

	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatalf("env load: %v", err)
	}
	if c.MemoryTTL().String() != "45s" {
		t.Errorf("memory_ttl = %s, want 45s", c.MemoryTTL())
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

func TestLoadFromEnvInvalidMemoryTTL(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_MEMORY_TTL", "ten minutes")

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected an error for an unparsable AAB_MEMORY_TTL")
	}
}
