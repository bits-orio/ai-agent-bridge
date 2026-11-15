package config

// The review-fix keys: agent.question_timeout, agent.daily_spend_path,
// factorio.sftp.insecure_ignore_host_key, and negative caps refused in YAML.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// loadYAML writes body as aab.yaml under a fresh directory, with the RCON
// password set, and loads it.
func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
	path := filepath.Join(dir, "aab.yaml")
	head := "factorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /tmp/events.jsonl\nmodel:\n  provider: fake\n  id: fake\n"
	if err := os.WriteFile(path, []byte(head+body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestQuestionTimeout(t *testing.T) {
	cases := []struct {
		name, yaml string
		want       time.Duration
		err        string
	}{
		{"default", "", 90 * time.Second, ""},
		{"set", "agent:\n  question_timeout: 45s\n", 45 * time.Second, ""},
		{"under the floor", "agent:\n  question_timeout: 5s\n", 0, "question_timeout"},
		{"negative", "agent:\n  question_timeout: -1s\n", 0, "question_timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadYAML(t, c.yaml)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want one naming %s", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.QuestionTimeout(); got != c.want {
				t.Errorf("QuestionTimeout() = %s, want %s", got, c.want)
			}
		})
	}
}

// A local model is given longer by default: one Ollama request may take
// five minutes on its own.
func TestQuestionTimeoutDefaultsLongerForOllama(t *testing.T) {
	cfg, err := loadYAML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Provider = "ollama"
	cfg.Agent.QuestionTimeout = 0
	cfg.applyQuestionDefaults("")
	if got := cfg.QuestionTimeout(); got != defaultOllamaQuestionTimeout {
		t.Errorf("ollama QuestionTimeout() = %s, want %s", got, defaultOllamaQuestionTimeout)
	}
}

func TestQuestionTimeoutFromEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_QUESTION_TIMEOUT", "30s")
	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.QuestionTimeout() != 30*time.Second {
		t.Errorf("QuestionTimeout() = %s", c.QuestionTimeout())
	}
	t.Setenv("AAB_QUESTION_TIMEOUT", "1s")
	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Error("a question_timeout under the floor loaded")
	}
}

// The daily spend file sits beside the history database unless named.
func TestDailySpendPath(t *testing.T) {
	cfg, err := loadYAML(t, "history:\n  path: data/history.sqlite\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(cfg.History.Path), "daily-spend.json"); cfg.Agent.DailySpendPath != want {
		t.Errorf("daily_spend_path = %q, want %q", cfg.Agent.DailySpendPath, want)
	}
	cfg, err = loadYAML(t, "agent:\n  daily_spend_path: spend.json\n")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.Agent.DailySpendPath) || filepath.Base(cfg.Agent.DailySpendPath) != "spend.json" {
		t.Errorf("a relative daily_spend_path = %q, want it resolved against the config", cfg.Agent.DailySpendPath)
	}
}

func TestInsecureIgnoreHostKey(t *testing.T) {
	cases := []struct {
		name, extra string
		ignore      bool
		err         bool
	}{
		{"unset keeps accepting any key", "", true, false},
		{"true accepts any key", "    insecure_ignore_host_key: true\n", true, false},
		{"false without known_hosts is refused", "    insecure_ignore_host_key: false\n", false, true},
		{"false with known_hosts verifies", "    insecure_ignore_host_key: false\n    known_hosts_path: /kh\n", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
			yaml := "transport: sftp\nfactorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /e\n  sftp:\n    host: h\n    user: u\n    key_path: /k\n" + c.extra + "model:\n  provider: fake\n  id: fake\n"
			path := filepath.Join(dir, "aab.yaml")
			if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if c.err {
				if err == nil || !strings.Contains(err.Error(), "known_hosts_path") {
					t.Fatalf("err = %v, want a refusal naming known_hosts_path", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Factorio.SFTP.IgnoreHostKey(); got != c.ignore {
				t.Errorf("IgnoreHostKey() = %v, want %v", got, c.ignore)
			}
		})
	}
}

// S14: a negative cap in YAML is a typo, not a setting, except on the three
// keys documented with -1 for none.
func TestNegativeCapsAreRefusedExceptWhereMinusOneMeansNone(t *testing.T) {
	refused := []string{
		"max_rounds", "max_tokens_per_question", "max_output_tokens", "max_tool_result_bytes",
		"max_round_tool_result_bytes", "session_max_exchanges", "session_max_bytes", "max_tool_calls",
	}
	for _, key := range refused {
		t.Run(key, func(t *testing.T) {
			_, err := loadYAML(t, "agent:\n  "+key+": -5\n")
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("agent.%s: -5 loaded (err %v)", key, err)
			}
		})
	}
	for _, key := range []string{"session_idle", "named_session_idle", "clarify_idle"} {
		t.Run(key, func(t *testing.T) {
			_, err := loadYAML(t, "agent:\n  "+key+": -1m\n")
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("agent.%s: -1m loaded (err %v)", key, err)
			}
		})
	}
	t.Run("poll_interval", func(t *testing.T) {
		if _, err := loadYAML(t, "poll_interval: -1s\n"); err == nil || !strings.Contains(err.Error(), "poll_interval") {
			t.Fatalf("poll_interval: -1s loaded (err %v)", err)
		}
	})
	for _, key := range []string{"questions_per_player_per_hour", "questions_per_hour", "max_cost_per_day"} {
		t.Run(key+" takes -1", func(t *testing.T) {
			if _, err := loadYAML(t, "agent:\n  "+key+": -1\n"); err != nil {
				t.Fatalf("agent.%s: -1 refused: %v", key, err)
			}
		})
	}
}
