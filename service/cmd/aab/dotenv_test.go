// Adapted from open-discord-bridge/bridge/cmd/bridge/dotenv_test.go: same test logic
// against loadDotEnv, with ODB's env var names swapped for AAB's.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	content := "# a comment\n" +
		"\n" +
		"ANTHROPIC_API_KEY=from-file\n" +
		"export FACTORIO_RCON_PASSWORD=\"qu=oted#with-specials\"\n" +
		"AAB_PREEXISTING=from-file\n" +
		"malformed-line-no-equals\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// Unset (not t.Setenv(..., "")) the two we expect from the file: loadDotEnv skips keys
	// that are already SET, even to empty, so they must be truly absent for the file value
	// to apply. The third is pre-set below to prove an existing env var is NOT overridden.
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("FACTORIO_RCON_PASSWORD")
	t.Cleanup(func() {
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("FACTORIO_RCON_PASSWORD")
	})
	t.Setenv("AAB_PREEXISTING", "from-env")

	loadDotEnv(dir)

	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "from-file" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want %q", got, "from-file")
	}
	// Quotes stripped; '=' and '#' inside the value preserved.
	if got := os.Getenv("FACTORIO_RCON_PASSWORD"); got != "qu=oted#with-specials" {
		t.Errorf("FACTORIO_RCON_PASSWORD = %q, want %q", got, "qu=oted#with-specials")
	}
	// Pre-existing environment must win over the file.
	if got := os.Getenv("AAB_PREEXISTING"); got != "from-env" {
		t.Errorf("AAB_PREEXISTING = %q, want %q (env must win)", got, "from-env")
	}
}

func TestLoadDotEnvMissing(t *testing.T) {
	loadDotEnv(t.TempDir()) // no .env present: must be a silent no-op, not a panic/error
}
