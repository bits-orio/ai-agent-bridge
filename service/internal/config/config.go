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
	defaultProvider             = "openrouter"
	defaultModel                = "deepseek/deepseek-v4-pro-0813"
	defaultSmallModel           = "deepseek/deepseek-v4.1-flash"
	defaultReasoning            = "low"
	defaultCacheTTL             = "1h"
	defaultDataCollection       = "deny"
	defaultOpenRouterKeyEnv     = "OPENROUTER_API_KEY"
	defaultAnthropicKeyEnv      = "ANTHROPIC_API_KEY"
	defaultMaxRounds            = 6
	defaultMaxTokensPerQuestion = 20000
	defaultMaxOutputTokens      = 4096
	defaultMaxToolResultBytes   = 4096
	defaultSessionIdle          = 3 * time.Minute
	defaultNamedSessionIdle     = 30 * time.Minute
	defaultSessionMaxExchanges  = 10
	defaultSessionMaxBytes      = 8000
	defaultQuestionsPerHour     = 20
	defaultServerQuestionsHour  = 120
	defaultMaxCostPerDay        = 5.0
	defaultMaxToolCalls         = 30
	defaultPollInterval         = time.Second
	defaultHistoryPath          = "history.sqlite"
	defaultControlAddr          = "127.0.0.1:8090"
	defaultControlTokenEnv      = "AAB_CONTROL_TOKEN"
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
	Factorio     FactorioConfig   `yaml:"factorio"`
	Transport    string           `yaml:"transport"` // "local" or "sftp"
	PollInterval Duration         `yaml:"poll_interval"`
	Model        ModelConfig      `yaml:"model"`
	OpenRouter   OpenRouterConfig `yaml:"openrouter"`
	Anthropic    AnthropicConfig  `yaml:"anthropic"`
	Agent        AgentConfig      `yaml:"agent"`
	History      HistoryConfig    `yaml:"history"`
	ControlAPI   ControlAPIConfig `yaml:"control_api"`
	Ledger       LedgerConfig     `yaml:"ledger"`
	Briefing     BriefingConfig   `yaml:"briefing"`
	LogFile      string           `yaml:"log_file"` // also write logs here (default: aab.log next to events; "-" = stderr only)
}

// AgentConfig is the per-question budget the operator sets. A question that
// hits any of these caps still answers, with a notice saying why.
type AgentConfig struct {
	MaxRounds                 int      `yaml:"max_rounds"`                    // model turns per question
	MaxTokensPerQuestion      int      `yaml:"max_tokens_per_question"`       // token budget across those turns
	MaxOutputTokens           int      `yaml:"max_output_tokens"`             // cap on one model turn's output, thinking included
	MaxToolResultBytes        int      `yaml:"max_tool_result_bytes"`         // a tool result longer than this is cut before the model sees it
	SessionIdle               Duration `yaml:"session_idle"`                  // a session ends after this long without a question
	NamedSessionIdle          Duration `yaml:"named_session_idle"`            // a #named session waits longer
	SessionMaxExchanges       int      `yaml:"session_max_exchanges"`         // oldest exchanges drop past this count
	SessionMaxBytes           int      `yaml:"session_max_bytes"`             // and past this many bytes of question and answer text
	QuestionsPerPlayerPerHour int      `yaml:"questions_per_player_per_hour"` // rolling-hour quota, -1 for no quota
	QuestionsPerHour          int      `yaml:"questions_per_hour"`            // the whole server's rolling-hour quota, -1 for none
	MaxCostPerDay             float64  `yaml:"max_cost_per_day"`              // USD the service may spend in a rolling day, -1 for no cap
	MaxToolCalls              int      `yaml:"max_tool_calls"`                // tool calls one question may make across its rounds
}

// HistoryConfig points at the SQLite file the service keeps a save's whole
// event history in. A relative path is resolved against the config file, so
// moving the config moves the history with it.
type HistoryConfig struct {
	Path string `yaml:"path"`
}

// LedgerConfig is the observability ledger: one JSONL line per question and
// one per model round (docs/design/phase4-observability-spec.md). It holds
// player names and question text by design, so it is on unless the operator
// turns it off, rather than the other way around. Enabled is a pointer
// because that default is true: an absent "enabled" key in the YAML must
// read as unset, not as an explicit false, which a plain bool cannot tell
// apart, so this section cannot reuse the "zero means take the default"
// idiom applyAgentDefaults uses for every int and float64 field below.
type LedgerConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Dir     string `yaml:"dir"` // default: the directory holding the config file
}

// BriefingConfig is the per-question game-state snapshot that rides in the
// user turn ahead of every question (docs/design/phase4-spec.md section 3):
// who is online, what each force is researching, the last few chat lines.
// Enabled is a pointer for the same reason LedgerConfig.Enabled is: the
// default is true, and an absent "enabled" key in the YAML must read as
// unset, not as an explicit false, which a plain bool cannot tell apart.
type BriefingConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// ControlAPIConfig is the service's own HTTP surface. An empty addr turns it
// off. The bearer token, when one is named, is resolved from the environment
// like every other secret.
type ControlAPIConfig struct {
	Addr     string `yaml:"addr"`
	TokenEnv string `yaml:"token_env"`
	Token    string `yaml:"-"` // resolved from env at load time
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

// ModelConfig is the operator's model choice, one section for every
// provider (docs/design/phase3-spec.md part 3, ADR 0007).
type ModelConfig struct {
	Provider       string   `yaml:"provider"`        // openrouter (default) | anthropic | fake
	ID             string   `yaml:"id"`              // the model id as the provider names it
	Small          string   `yaml:"small"`           // reserved for sub-agents; unused until they land
	Fallbacks      []string `yaml:"fallbacks"`       // OpenRouter: models tried in order when ID fails
	Reasoning      string   `yaml:"reasoning"`       // low (default) | off | model | medium | high
	CacheTTL       string   `yaml:"cache_ttl"`       // 1h (default) | 5m for the rules-and-tools cache entry
	DataCollection string   `yaml:"data_collection"` // OpenRouter: deny (default) | allow
}

// OpenRouterConfig and AnthropicConfig hold each provider's key reference.
// A key is not required to be present at load: the Phase 0 subcommands
// (status/probe/rpc/poll) never call a model, so a service that only wants
// to check the RCON transport is not blocked on having one. The run
// subcommand checks for itself.
type OpenRouterConfig struct {
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"-"` // resolved from env at load time
}

type AnthropicConfig struct {
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"-"` // resolved from env at load time
}

// check refuses a value a provider would refuse later, so a typo in
// aab.yaml fails at start and not on the first question.
func (m ModelConfig) check() error {
	switch m.Provider {
	case "openrouter", "anthropic", "fake":
	default:
		return fmt.Errorf("model.provider: %q is not openrouter, anthropic or fake", m.Provider)
	}
	switch m.Reasoning {
	case "off", "model", "low", "medium", "high":
	default:
		return fmt.Errorf("model.reasoning: %q is not off, model, low, medium or high", m.Reasoning)
	}
	switch m.CacheTTL {
	case "5m", "1h":
	default:
		return fmt.Errorf("model.cache_ttl: %q is not 5m or 1h", m.CacheTTL)
	}
	switch m.DataCollection {
	case "deny", "allow":
	default:
		return fmt.Errorf("model.data_collection: %q is not deny or allow", m.DataCollection)
	}
	return nil
}

// Load reads and validates configuration. If the config file is absent, or env-var mode
// is forced with AAB_CONFIG=none, it builds the config entirely from environment
// variables (env-var config mode). See LoadFromEnv. It also writes the effective-config
// snapshot (effective.go) as a side effect, the way every subcommand but stats wants.
func Load(path string) (*Config, error) {
	return load(path, true)
}

// LoadQuiet is Load without the effective-config snapshot: same resolution and
// validation, but finish's writeEffective side effect never runs. Use it for a caller
// that only wants a resolved *Config to read a field off of and cannot say which
// directory it will be run from (stats.go's statsLedgerDir, the one caller today) --
// Load would otherwise clobber another process's own aab.effective.yaml sitting there.
func LoadQuiet(path string) (*Config, error) {
	return load(path, false)
}

func load(path string, dump bool) (*Config, error) {
	_, statErr := os.Stat(path)
	if forced := strings.EqualFold(os.Getenv("AAB_CONFIG"), "none"); forced || errors.Is(statErr, fs.ErrNotExist) {
		return loadFromEnv(Meta{Mode: "env", ConfigPath: path, Forced: forced, FileExists: statErr == nil}, dump)
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
	c.applyModelDefaults()
	if err := c.Model.check(); err != nil {
		return nil, err
	}
	c.applyAgentDefaults()
	c.History.Path = resolveHistoryPath(c.History.Path, path)
	c.applyLedgerDefaults(path)
	c.applyBriefingDefaults()
	c.applyControlDefaults()

	// Resolve secrets from the environment; never store them in the YAML.
	if c.Factorio.RCON.PasswordEnv != "" {
		c.Factorio.RCON.Password = os.Getenv(c.Factorio.RCON.PasswordEnv)
	}
	if c.OpenRouter.APIKeyEnv != "" {
		c.OpenRouter.APIKey = os.Getenv(c.OpenRouter.APIKeyEnv)
	}
	if c.Anthropic.APIKeyEnv != "" {
		c.Anthropic.APIKey = os.Getenv(c.Anthropic.APIKeyEnv)
	}
	if c.Factorio.SFTP.PasswordEnv != "" {
		c.Factorio.SFTP.Password = os.Getenv(c.Factorio.SFTP.PasswordEnv)
	}
	if c.ControlAPI.TokenEnv != "" {
		c.ControlAPI.Token = os.Getenv(c.ControlAPI.TokenEnv)
	}

	return finish(&c, Meta{Mode: "file", ConfigPath: path, Warnings: unknownKeyWarnings(b)}, dump)
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
	return loadFromEnv(Meta{Mode: "env"}, true)
}

func loadFromEnv(m Meta, dump bool) (*Config, error) {
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
		Model: ModelConfig{
			Provider:       getenvDefault("AAB_MODEL_PROVIDER", defaultProvider),
			ID:             getenvDefault("AAB_MODEL", defaultModel),
			Small:          getenvDefault("AAB_MODEL_SMALL", defaultSmallModel),
			Fallbacks:      splitList(os.Getenv("AAB_MODEL_FALLBACKS")),
			Reasoning:      getenvDefault("AAB_REASONING", defaultReasoning),
			CacheTTL:       getenvDefault("AAB_CACHE_TTL", defaultCacheTTL),
			DataCollection: getenvDefault("AAB_DATA_COLLECTION", defaultDataCollection),
		},
		OpenRouter: OpenRouterConfig{
			APIKeyEnv: defaultOpenRouterKeyEnv,
			APIKey:    os.Getenv(defaultOpenRouterKeyEnv),
		},
		Anthropic: AnthropicConfig{
			APIKeyEnv: defaultAnthropicKeyEnv,
			APIKey:    os.Getenv(defaultAnthropicKeyEnv),
		},
		History: HistoryConfig{Path: expandPath(os.Getenv("AAB_HISTORY_PATH"))},
		ControlAPI: ControlAPIConfig{
			Addr:     getenvDefault("AAB_CONTROL_ADDR", defaultControlAddr),
			TokenEnv: getenvDefault("AAB_CONTROL_TOKEN_ENV", defaultControlTokenEnv),
		},
	}
	c.ControlAPI.Token = os.Getenv(c.ControlAPI.TokenEnv)

	if v := os.Getenv("AAB_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_POLL_INTERVAL: %w", err)
		}
		c.PollInterval = Duration(d)
	} else {
		c.PollInterval = Duration(defaultPollInterval)
	}

	if v := os.Getenv("AAB_MAX_ROUNDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_ROUNDS: invalid value %q", v)
		}
		c.Agent.MaxRounds = n
	}
	if v := os.Getenv("AAB_MAX_TOKENS_PER_QUESTION"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_TOKENS_PER_QUESTION: invalid value %q", v)
		}
		c.Agent.MaxTokensPerQuestion = n
	}
	if v := os.Getenv("AAB_MAX_OUTPUT_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_OUTPUT_TOKENS: invalid value %q", v)
		}
		c.Agent.MaxOutputTokens = n
	}
	if v := os.Getenv("AAB_MAX_TOOL_RESULT_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_TOOL_RESULT_BYTES: invalid value %q", v)
		}
		c.Agent.MaxToolResultBytes = n
	}
	if v := os.Getenv("AAB_SESSION_IDLE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_SESSION_IDLE: %w", err)
		}
		c.Agent.SessionIdle = Duration(d)
	}
	if v := os.Getenv("AAB_NAMED_SESSION_IDLE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_NAMED_SESSION_IDLE: %w", err)
		}
		c.Agent.NamedSessionIdle = Duration(d)
	}
	if v := os.Getenv("AAB_SESSION_MAX_EXCHANGES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_SESSION_MAX_EXCHANGES: invalid value %q", v)
		}
		c.Agent.SessionMaxExchanges = n
	}
	if v := os.Getenv("AAB_SESSION_MAX_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_SESSION_MAX_BYTES: invalid value %q", v)
		}
		c.Agent.SessionMaxBytes = n
	}
	if v := os.Getenv("AAB_QUESTIONS_PER_HOUR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_QUESTIONS_PER_HOUR: invalid value %q", v)
		}
		c.Agent.QuestionsPerHour = n
	}
	if v := os.Getenv("AAB_MAX_COST_PER_DAY"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("AAB_MAX_COST_PER_DAY: invalid value %q", v)
		}
		c.Agent.MaxCostPerDay = f
	}
	if v := os.Getenv("AAB_MAX_TOOL_CALLS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("AAB_MAX_TOOL_CALLS: invalid value %q", v)
		}
		c.Agent.MaxToolCalls = n
	}
	if v := os.Getenv("AAB_QUESTIONS_PER_PLAYER_PER_HOUR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_QUESTIONS_PER_PLAYER_PER_HOUR: invalid value %q", v)
		}
		c.Agent.QuestionsPerPlayerPerHour = n
	}
	if v := os.Getenv("AAB_LEDGER_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_LEDGER_ENABLED: invalid value %q", v)
		}
		c.Ledger.Enabled = &b
	}
	if v := os.Getenv("AAB_LEDGER_DIR"); v != "" {
		c.Ledger.Dir = expandPath(v)
	}
	if v := os.Getenv("AAB_BRIEFING_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("AAB_BRIEFING_ENABLED: invalid value %q", v)
		}
		c.Briefing.Enabled = &b
	}

	c.applyAgentDefaults()
	// Env-var mode has no config file to anchor a relative history path, or
	// a relative ledger dir, to.
	c.History.Path = resolveHistoryPath(c.History.Path, "")
	c.applyLedgerDefaults("")
	c.applyBriefingDefaults()
	c.applyControlDefaults()

	if err := c.Model.check(); err != nil {
		return nil, err
	}
	return finish(c, m, dump)
}

// applyAgentDefaults fills the agent caps left unset. A quota below zero is a
// deliberate "no quota", so only an unset (zero) value takes the default.
func (c *Config) applyAgentDefaults() {
	if c.Agent.MaxRounds == 0 {
		c.Agent.MaxRounds = defaultMaxRounds
	}
	if c.Agent.MaxTokensPerQuestion == 0 {
		c.Agent.MaxTokensPerQuestion = defaultMaxTokensPerQuestion
	}
	if c.Agent.MaxOutputTokens == 0 {
		c.Agent.MaxOutputTokens = defaultMaxOutputTokens
	}
	if c.Agent.MaxToolResultBytes == 0 {
		c.Agent.MaxToolResultBytes = defaultMaxToolResultBytes
	}
	if c.Agent.SessionIdle == 0 {
		c.Agent.SessionIdle = Duration(defaultSessionIdle)
	}
	if c.Agent.NamedSessionIdle == 0 {
		c.Agent.NamedSessionIdle = Duration(defaultNamedSessionIdle)
	}
	if c.Agent.SessionMaxExchanges == 0 {
		c.Agent.SessionMaxExchanges = defaultSessionMaxExchanges
	}
	if c.Agent.SessionMaxBytes == 0 {
		c.Agent.SessionMaxBytes = defaultSessionMaxBytes
	}
	if c.Agent.QuestionsPerPlayerPerHour == 0 {
		c.Agent.QuestionsPerPlayerPerHour = defaultQuestionsPerHour
	}
	if c.Agent.QuestionsPerHour == 0 {
		c.Agent.QuestionsPerHour = defaultServerQuestionsHour
	}
	if c.Agent.MaxCostPerDay == 0 {
		c.Agent.MaxCostPerDay = defaultMaxCostPerDay
	}
	if c.Agent.MaxToolCalls == 0 {
		c.Agent.MaxToolCalls = defaultMaxToolCalls
	}
}

func (c *Config) applyControlDefaults() {
	if c.ControlAPI.TokenEnv == "" {
		c.ControlAPI.TokenEnv = defaultControlTokenEnv
	}
}

// applyLedgerDefaults resolves the ledger section: enabled defaults to true
// (LedgerConfig's own doc comment says why a plain bool cannot do this), and
// dir defaults to the directory holding the config file, resolved the same
// way resolveHistoryPath already resolves history.path.
func (c *Config) applyLedgerDefaults(configPath string) {
	if c.Ledger.Enabled == nil {
		enabled := true
		c.Ledger.Enabled = &enabled
	}
	c.Ledger.Dir = resolveLedgerDir(c.Ledger.Dir, configPath)
}

// resolveLedgerDir applies the ledger.dir default (the directory holding the
// config file, an empty dir joined to nothing rather than a filename joined
// to it, since ledger.dir names a directory) and resolves a relative dir
// against the config file the same way resolveHistoryPath resolves
// history.path: moving the config moves the ledger with it. Env-var mode has
// no config file to anchor to, so an unset dir falls back to the working
// directory rather than an empty string, which ledger.Open cannot create a
// file under.
func resolveLedgerDir(dir, configPath string) string {
	if dir == "" {
		if configPath == "" {
			return "."
		}
		if d := filepath.Dir(configPath); d != "" {
			return d
		}
		return "."
	}
	return anchorTo(dir, configPath)
}

// anchorTo is the rule both resolveLedgerDir and resolveHistoryPath apply once
// they have a path: expand it, leave it alone when it is absolute or when
// there is no config file to anchor to, and otherwise read it relative to the
// directory holding the config. The two callers differ only in the default
// they apply before this, so the rule lives here and a change to it cannot
// reach one path and miss the other.
func anchorTo(path, configPath string) string {
	path = expandPath(path)
	if filepath.IsAbs(path) || configPath == "" {
		return path
	}
	dir := filepath.Dir(configPath)
	if dir == "" || dir == "." {
		return path
	}
	return filepath.Join(dir, path)
}

// LedgerEnabled is the ledger.enabled setting after defaults are applied:
// true unless the operator explicitly turned it off.
func (c *Config) LedgerEnabled() bool {
	return c.Ledger.Enabled == nil || *c.Ledger.Enabled
}

// applyBriefingDefaults resolves the briefing section: enabled defaults to
// true, the same "unset means on" rule applyLedgerDefaults already applies
// to the ledger.
func (c *Config) applyBriefingDefaults() {
	if c.Briefing.Enabled == nil {
		enabled := true
		c.Briefing.Enabled = &enabled
	}
}

// BriefingEnabled is the briefing.enabled setting after defaults are
// applied: true unless the operator explicitly turned it off.
func (c *Config) BriefingEnabled() bool {
	return c.Briefing.Enabled == nil || *c.Briefing.Enabled
}

// resolveHistoryPath keeps the history file beside the config that named it,
// so a service started from another directory still finds the same database.
// In env-var mode there is no config file to anchor to and a relative path
// stays relative to the working directory.
func resolveHistoryPath(path, configPath string) string {
	if path == "" {
		path = defaultHistoryPath
	}
	return anchorTo(path, configPath)
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *Config) Interval() time.Duration { return time.Duration(c.PollInterval) }

// MemoryTTL is how long a player's follow-up context lives.
// SessionIdle and NamedSessionIdle as durations.
func (c *Config) SessionIdle() time.Duration      { return time.Duration(c.Agent.SessionIdle) }
func (c *Config) NamedSessionIdle() time.Duration { return time.Duration(c.Agent.NamedSessionIdle) }

func expandPath(p string) string {
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}

func (c *Config) applyModelDefaults() {
	if c.Model.Provider == "" {
		c.Model.Provider = defaultProvider
	}
	if c.Model.ID == "" {
		c.Model.ID = defaultModel
	}
	if c.Model.Small == "" {
		c.Model.Small = defaultSmallModel
	}
	if c.Model.Reasoning == "" {
		c.Model.Reasoning = defaultReasoning
	}
	if c.Model.CacheTTL == "" {
		c.Model.CacheTTL = defaultCacheTTL
	}
	if c.Model.DataCollection == "" {
		c.Model.DataCollection = defaultDataCollection
	}
	if c.OpenRouter.APIKeyEnv == "" {
		c.OpenRouter.APIKeyEnv = defaultOpenRouterKeyEnv
	}
	if c.Anthropic.APIKeyEnv == "" {
		c.Anthropic.APIKeyEnv = defaultAnthropicKeyEnv
	}
}

// splitList reads a comma-separated env value into a list, blanks dropped.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
