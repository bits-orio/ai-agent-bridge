package config

import (
	"fmt"
	"os"
	"path"
	"strconv"
)

// ToastsConfig is the unprompted milestone toasts: a line for the whole
// server when a force launches its first rocket, or is the first to finish
// a headline technology (cmd/aab, toasts.go).
type ToastsConfig struct {
	Mode         string   `yaml:"mode"`         // plain (default): a templated line; model: one line written by the model, plain when it fails; off
	MaxPerHour   int      `yaml:"max_per_hour"` // at most this many in a rolling hour, the rest dropped with a log line: 6 (default)
	Technologies []string `yaml:"technologies"` // glob patterns of technology names a "first to research" toast is for; leave out for the default list
	WhenEmpty    bool     `yaml:"when_empty"`   // toast with nobody online too; false (default), there only so a test server can be checked
}

// DefaultToastTechnologies is every science pack unlock, the rocket silo,
// the first space platform and each planet's discovery: the steps a whole
// server would want to hear about, and none of the hundreds of others.
var DefaultToastTechnologies = []string{"*-science-pack", "rocket-silo", "space-platform", "planet-discovery-*"}

const (
	defaultToastsMode       = "plain"
	defaultToastsMaxPerHour = 6
)

// applyToastsDefaults fills what the operator left unset. An empty list is
// kept as written: it turns the technology toasts off.
func (c *Config) applyToastsDefaults() {
	if c.Toasts.Mode == "" {
		c.Toasts.Mode = defaultToastsMode
	}
	if c.Toasts.MaxPerHour == 0 {
		c.Toasts.MaxPerHour = defaultToastsMaxPerHour
	}
	if c.Toasts.Technologies == nil {
		c.Toasts.Technologies = append([]string(nil), DefaultToastTechnologies...)
	}
}

// check refuses a mode it does not know, a negative cap and a pattern that
// cannot match anything.
func (t ToastsConfig) check() error {
	switch t.Mode {
	case "plain", "model", "off":
	default:
		return fmt.Errorf("toasts.mode %q not supported (use plain, model or off)", t.Mode)
	}
	if t.MaxPerHour < 0 {
		return fmt.Errorf("toasts.max_per_hour: negative values are not allowed (leave it out for the default, or set mode off)")
	}
	for _, p := range t.Technologies {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("toasts.technologies: %q is not a valid pattern: %v", p, err)
		}
	}
	return nil
}

// toastsFromEnv reads the toasts section in env-var mode.
func toastsFromEnv(c *Config) error {
	c.Toasts.Mode = os.Getenv("AAB_TOASTS_MODE")
	if v := os.Getenv("AAB_TOASTS_MAX_PER_HOUR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("AAB_TOASTS_MAX_PER_HOUR: invalid value %q", v)
		}
		c.Toasts.MaxPerHour = n
	}
	if v, ok := os.LookupEnv("AAB_TOASTS_TECHNOLOGIES"); ok {
		c.Toasts.Technologies = append([]string{}, splitList(v)...)
	}
	if v := os.Getenv("AAB_TOASTS_WHEN_EMPTY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("AAB_TOASTS_WHEN_EMPTY: invalid value %q", v)
		}
		c.Toasts.WhenEmpty = b
	}
	return nil
}
