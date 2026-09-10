// Adapted from open-discord-bridge/bridge/internal/config/config_test.go: same coverage
// shape (env-mode load, defaults, path expansion, validation failure), retargeted at
// AAB's field set (no Discord routes/commands/admins; adds Anthropic model/rounds/tokens).

package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if c.MaxRounds != defaultMaxRounds {
		t.Errorf("MaxRounds = %d, want default %d", c.MaxRounds, defaultMaxRounds)
	}
	if c.MaxTokensPerQuestion != defaultMaxTokensPerQuestion {
		t.Errorf("MaxTokensPerQuestion = %d, want default %d", c.MaxTokensPerQuestion, defaultMaxTokensPerQuestion)
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
	if c.MaxRounds != 12 {
		t.Errorf("MaxRounds = %d, want 12", c.MaxRounds)
	}
	if c.MaxTokensPerQuestion != 8000 {
		t.Errorf("MaxTokensPerQuestion = %d, want 8000", c.MaxTokensPerQuestion)
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
