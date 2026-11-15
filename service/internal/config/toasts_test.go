package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func loadToastYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("FACTORIO_RCON_PASSWORD", "pw")
	path := filepath.Join(dir, "aab.yaml")
	base := "factorio:\n  rcon:\n    address: \"game:27015\"\n    password_env: FACTORIO_RCON_PASSWORD\n  events_file: /tmp/events.jsonl\n"
	if err := os.WriteFile(path, []byte(base+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestToastsDefaults(t *testing.T) {
	c, err := loadToastYAML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Toasts.Mode != "plain" || c.Toasts.MaxPerHour != 6 || c.Toasts.WhenEmpty {
		t.Errorf("toasts = %+v, want plain, 6 an hour, silent with nobody on", c.Toasts)
	}
	if !slices.Equal(c.Toasts.Technologies, DefaultToastTechnologies) {
		t.Errorf("technologies = %v, want the default list", c.Toasts.Technologies)
	}
	// Present in the effective snapshot.
	raw, _ := os.ReadFile(EffectiveConfigName)
	if !strings.Contains(string(raw), "toasts:") || !strings.Contains(string(raw), "max_per_hour: 6") {
		t.Errorf("effective config has no toasts section:\n%s", raw)
	}
}

func TestToastsFromFile(t *testing.T) {
	c, err := loadToastYAML(t, "toasts:\n  mode: model\n  max_per_hour: 2\n  when_empty: true\n  technologies: [\"rocket-silo\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Toasts.Mode != "model" || c.Toasts.MaxPerHour != 2 || !c.Toasts.WhenEmpty || !slices.Equal(c.Toasts.Technologies, []string{"rocket-silo"}) {
		t.Errorf("toasts = %+v", c.Toasts)
	}
}

func TestToastsRefusesBadValues(t *testing.T) {
	for _, body := range []string{
		"toasts:\n  mode: loud\n",
		"toasts:\n  max_per_hour: -1\n",
		"toasts:\n  technologies: [\"[bad\"]\n",
	} {
		if _, err := loadToastYAML(t, body); err == nil || !strings.Contains(err.Error(), "toasts.") {
			t.Errorf("%q loaded, err %v; want a toasts.* error", body, err)
		}
	}
}

func TestToastsFromEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	setEnvMode(t)
	t.Setenv("AAB_TOASTS_MODE", "off")
	t.Setenv("AAB_TOASTS_MAX_PER_HOUR", "3")
	t.Setenv("AAB_TOASTS_TECHNOLOGIES", "rocket-silo, *-science-pack")
	t.Setenv("AAB_TOASTS_WHEN_EMPTY", "true")
	c, err := Load("/no/such/aab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Toasts.Mode != "off" || c.Toasts.MaxPerHour != 3 || !c.Toasts.WhenEmpty || !slices.Equal(c.Toasts.Technologies, []string{"rocket-silo", "*-science-pack"}) {
		t.Errorf("toasts = %+v", c.Toasts)
	}
}
