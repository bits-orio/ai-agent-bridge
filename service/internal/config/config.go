// Package config loads the service's configuration, in the same two-mode style as
// open-discord-bridge/bridge/internal/config: a YAML file (aab.yaml), or, when no file
// is present or AAB_CONFIG=none forces it, environment variables prefixed AAB_, with
// secrets (RCON password, SFTP password, the Anthropic API key) always resolved from a
// separately named env var, never written into YAML.
//
// FACTORIO_RCON_PASSWORD and SFTP_PASSWORD deliberately reuse open-discord-bridge's env
// var names: an operator running both services against the same Factorio server can point
// one .env file at both.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults applied whenever a field is left unset, in both config modes.
const (
	defaultModel                = "claude-opus-5"
	defaultMaxRounds            = 8
	defaultMaxTokensPerQuestion = 4096
	defaultPollInterval         = time.Second
)

// Duration is a time.Duration that unmarshals from a YAML string like "2s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

type Config struct {
	Factorio             FactorioConfig  `yaml:"factorio"`
	Transport            string          `yaml:"transport"` // "local" or "sftp"
	PollInterval         Duration        `yaml:"poll_interval"`
	Anthropic            AnthropicConfig `yaml:"anthropic"`
	MaxRounds            int             `yaml:"max_rounds"`              // per-question cap on agent-loop rounds
	MaxTokensPerQuestion int             `yaml:"max_tokens_per_question"` // per-question token budget
	LogFile              string          `yaml:"log_file"`                // also write logs here (default: aab.log next to events; "-" = stderr only)
}

// FactorioConfig groups everything needed to reach the companion mod: RCON for the
// aab-rpc-v1 protocol, and the events file (tailed for history, starting Phase 2).
type FactorioConfig struct {
	RCON       RCONConfig `yaml:"rcon"`
	EventsFile string     `yaml:"events_file"` // local path, or remote path for sftp
	SFTP       SFTPConfig `yaml:"sftp"`
}

// SFTPConfig is used when transport is "sftp" (the service on separate infra from
// Factorio, e.g. Pterodactyl's per-server SFTP).
type SFTPConfig struct {
	Host           string `yaml:"host"` // host:port
	User           string `yaml:"user"`
	KeyPath        string `yaml:"key_path"`
	PasswordEnv    string `yaml:"password_env"`
	Password       string `yaml:"-"` // resolved from env at load time
	KnownHostsPath string `yaml:"known_hosts_path"`
}

type RCONConfig struct {
	Address     string `yaml:"address"`
	PasswordEnv string `yaml:"password_env"`
	Password    string `yaml:"-"` // resolved from env at load time
}

// AnthropicConfig holds the operator's model choice and API key reference. The key is
// not validated as present here: none of the Phase 0 subcommands (status/probe/rpc/poll)
// call the Anthropic API, so a service that only wants to check the RCON transport should
// not be blocked on having a key configured yet. The agent loop (Phase 1) checks for
// itself before running.
type AnthropicConfig struct {
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"-"` // resolved from env at load time
	Model     string `yaml:"model"`
}

// Load reads and validates configuration. If the config file is absent, or env-var mode
// is forced with AAB_CONFIG=none, it builds the config entirely from environment
// variables (env-var config mode). See LoadFromEnv.
func Load(path string) (*Config, error) {
	_, statErr := os.Stat(path)
	if forced := strings.EqualFold(os.Getenv("AAB_CONFIG"), "none"); forced || errors.Is(statErr, fs.ErrNotExist) {
		return loadFromEnv(Meta{Mode: "env", ConfigPath: path, Forced: forced, FileExists: statErr == nil})
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Allow ${ENV} and a leading ~/ in paths and the RCON address so configs stay portable
	// and don't need to hardcode machine-specific values.
	c.Factorio.EventsFile = expandPath(c.Factorio.EventsFile)
	c.Factorio.RCON.Address = os.ExpandEnv(c.Factorio.RCON.Address)
	c.LogFile = expandPath(c.LogFile)
	c.Factorio.SFTP.KeyPath = expandPath(c.Factorio.SFTP.KeyPath)
	c.Factorio.SFTP.KnownHostsPath = expandPath(c.Factorio.SFTP.KnownHostsPath)

	if c.Transport == "" {
		c.Transport = "local"
	}
	if c.PollInterval == 0 {
		c.PollInterval = Duration(defaultPollInterval)
	}
	if c.Anthropic.Model == "" {
		c.Anthropic.Model = defaultModel
	}
	if c.MaxRounds == 0 {
		c.MaxRounds = defaultMaxRounds
	}
	if c.MaxTokensPerQuestion == 0 {
		c.MaxTokensPerQuestion = defaultMaxTokensPerQuestion
	}

	// Resolve secrets from the environment; never store them in the YAML.
	if c.Factorio.RCON.PasswordEnv != "" {
		c.Factorio.RCON.Password = os.Getenv(c.Factorio.RCON.PasswordEnv)
	}
	if c.Anthropic.APIKeyEnv != "" {
		c.Anthropic.APIKey = os.Getenv(c.Anthropic.APIKeyEnv)
	}
	if c.Factorio.SFTP.PasswordEnv != "" {
		c.Factorio.SFTP.Password = os.Getenv(c.Factorio.SFTP.PasswordEnv)
	}

	return finish(&c, Meta{Mode: "file", ConfigPath: path, Warnings: unknownKeyWarnings(b)})
}

func (c *Config) validate() error {
	if c.Transport != "local" && c.Transport != "sftp" {
		return fmt.Errorf("transport %q not supported (use \"local\" or \"sftp\")", c.Transport)
	}
	if c.Factorio.EventsFile == "" {
		return fmt.Errorf("factorio.events_file is required")
	}
	if c.Transport == "sftp" {
		s := c.Factorio.SFTP
		if s.Host == "" || s.User == "" {
			return fmt.Errorf("sftp transport requires factorio.sftp.host and factorio.sftp.user")
		}
		if s.KeyPath == "" && s.Password == "" {
			return fmt.Errorf("sftp transport requires factorio.sftp.key_path or a password")
		}
	}
	if c.Factorio.RCON.Address == "" {
		return fmt.Errorf("factorio.rcon.address is required")
	}
	if c.Factorio.RCON.Password == "" {
		return fmt.Errorf("RCON password is empty; set env var %q", c.Factorio.RCON.PasswordEnv)
	}
	return nil
}

// LoadFromEnv builds config entirely from environment variables. Used when no config
// file is present (containers, panels like Pterodactyl) or when AAB_CONFIG=none forces
// it. Non-secret settings use AAB_* vars; secrets use FACTORIO_RCON_PASSWORD,
// SFTP_PASSWORD and ANTHROPIC_API_KEY.
func LoadFromEnv() (*Config, error) {
	return loadFromEnv(Meta{Mode: "env"})
}

func loadFromEnv(m Meta) (*Config, error) {
	c := &Config{
		Transport: getenvDefault("AAB_TRANSPORT", "local"),
		LogFile:   expandPath(os.Getenv("AAB_LOG_FILE")),
		Factorio: FactorioConfig{
			RCON: RCONConfig{
				Address:     os.Getenv("AAB_RCON_ADDRESS"),
				PasswordEnv: "FACTORIO_RCON_PASSWORD",
				Password:    os.Getenv("FACTORIO_RCON_PASSWORD"),
			},
			EventsFile: expandPath(os.Getenv("AAB_EVENTS_FILE")),
			SFTP: SFTPConfig{
				Host:           os.Getenv("AAB_SFTP_HOST"),
				User:           os.Getenv("AAB_SFTP_USER"),
				KeyPath:        os.Getenv("AAB_SFTP_KEY_PATH"),
				PasswordEnv:    "SFTP_PASSWORD",
				Password:       os.Getenv("SFTP_PASSWORD"),
				KnownHostsPath: os.Getenv("AAB_SFTP_KNOWN_HOSTS"),
			},
		},
		Anthropic: AnthropicConfig{
			APIKeyEnv: "ANTHROPIC_API_KEY",
			APIKey:    os.Getenv("ANTHROPIC_API_KEY"),
			Model:     getenvDefault("AAB_MODEL", defaultModel),
		},
	}

	if v := os.Getenv("AAB_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_POLL_INTERVAL: %w", err)
		}
		c.PollInterval = Duration(d)
	} else {
		c.PollInterval = Duration(defaultPollInterval)
	}

	c.MaxRounds = defaultMaxRounds
	if v := os.Getenv("AAB_MAX_ROUNDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_ROUNDS: invalid value %q", v)
		}
		c.MaxRounds = n
	}

	c.MaxTokensPerQuestion = defaultMaxTokensPerQuestion
	if v := os.Getenv("AAB_MAX_TOKENS_PER_QUESTION"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_TOKENS_PER_QUESTION: invalid value %q", v)
		}
		c.MaxTokensPerQuestion = n
	}

	return finish(c, m)
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *Config) Interval() time.Duration { return time.Duration(c.PollInterval) }

func expandPath(p string) string {
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}
