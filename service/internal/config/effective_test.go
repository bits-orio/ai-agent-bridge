// Adapted from open-discord-bridge/bridge/internal/config/effective_test.go: same
// effective-config-dump coverage (no secret leakage, validation-failure recording, unknown
// key warnings, forced env mode), retargeted at aab.effective.yaml and AAB's secrets.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readEffective(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(EffectiveConfigName)
	if err != nil {
		t.Fatalf("read %s: %v", EffectiveConfigName, err)
	}
	return string(b)
}

func TestEffectiveDumpWrittenWithoutSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secretvalue")

	if _, err := Load("/no/such/aab.yaml"); err != nil {
		t.Fatalf("env load: %v", err)
	}
	out := readEffective(t)

	for _, secret := range []string{"pw", "sk-ant-secretvalue"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret value %q leaked into effective config:\n%s", secret, out)
		}
	}
	for _, want := range []string{
		"env-var mode",
		"Validation:   OK",
		"FACTORIO_RCON_PASSWORD: SET (2 chars)",
		"ANTHROPIC_API_KEY: SET (18 chars)",
		"poll_interval: 1s", // Duration must marshal as a string, not nanoseconds
		"address: game:27015",
		"model: claude-sonnet-5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("effective config missing %q:\n%s", want, out)
		}
	}
}

func TestEffectiveDumpWrittenOnValidationFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("FACTORIO_RCON_PASSWORD", "") // the empty-secret case

	if _, err := Load("/no/such/aab.yaml"); err == nil {
		t.Fatal("expected validation error for empty RCON password")
	}
	out := readEffective(t)

	if !strings.Contains(out, "Validation:   FAILED") || !strings.Contains(out, "RCON password is empty") {
		t.Fatalf("effective config should record the validation failure:\n%s", out)
	}
	if !strings.Contains(out, "FACTORIO_RCON_PASSWORD: MISSING") {
		t.Fatalf("effective config should mark the missing secret:\n%s", out)
	}
}

func TestUnknownKeysWarnButLoad(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")

	cfgFile := filepath.Join(t.TempDir(), "aab.yaml")
	yaml := `
factorio:
  rcon:
    address: 127.0.0.1:27015
    password_env: FACTORIO_RCON_PASSWORD
  events_file: /tmp/events.jsonl
rcon:
  address: 127.0.0.1:33641
  password: testing123
`
	if err := os.WriteFile(cfgFile, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("unknown keys must warn, not fail: %v", err)
	}
	if c.Factorio.RCON.Address != "127.0.0.1:27015" {
		t.Fatalf("config not loaded: %+v", c.Factorio)
	}
	out := readEffective(t)
	if !strings.Contains(out, `unknown key "rcon"`) {
		t.Fatalf("effective config should warn about the unknown top-level rcon key:\n%s", out)
	}
	// The injected literal password must not leak into the dump either.
	if strings.Contains(out, "testing123") {
		t.Fatalf("ignored unknown-key value leaked into effective config:\n%s", out)
	}
	if !strings.Contains(out, "file mode") {
		t.Fatalf("effective config should state file mode:\n%s", out)
	}
}

func TestForcedEnvModeWithoutFileStatesTheGuard(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_CONFIG", "none")

	// No config file exists; the header must still show the guard is active, so
	// "forced, no file" and "unguarded, no file" are distinguishable.
	if _, err := Load("/no/such/aab.yaml"); err != nil {
		t.Fatalf("forced env mode: %v", err)
	}
	if out := readEffective(t); !strings.Contains(out, "AAB_CONFIG=none; no file") {
		t.Fatalf("effective config should state the AAB_CONFIG=none guard even without a file:\n%s", out)
	}
}
