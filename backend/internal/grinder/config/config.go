package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that supports YAML string parsing ("45m", "2500ms").
type Duration time.Duration

// UnmarshalYAML parses either a duration string or raw nanoseconds.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err == nil && s != "" {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(parsed)
		return nil
	}
	var n int64
	if err := value.Decode(&n); err != nil {
		return fmt.Errorf("duration must be a string like \"45m\" or an integer nanoseconds")
	}
	*d = Duration(n)
	return nil
}

// MarshalYAML emits durations as human-readable strings.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the top-level application configuration.
type Config struct {
	Clusters []Cluster     `yaml:"clusters"`
	Humanize Humanize      `yaml:"humanize"`
	Captcha  CaptchaConfig `yaml:"captcha"`
	Features Features      `yaml:"features"`
	Log      LogConfig     `yaml:"log"`
}

// Cluster groups a master account with optional secondaries in a shared channel.
type Cluster struct {
	Name      string     `yaml:"name"`
	GuildID   string     `yaml:"guild_id"`
	ChannelID string     `yaml:"channel_id"`
	Master    Instance   `yaml:"master"`
	Secondary []Instance `yaml:"secondary"`
	Shift     Shift      `yaml:"shift"`
}

// Instance is a single Discord account.
type Instance struct {
	Name  string `yaml:"name"`
	Token string `yaml:"token"`
	Proxy string `yaml:"proxy,omitempty"`
}

// Shift schedules alternating on/off periods to look less botlike.
type Shift struct {
	Enabled     bool     `yaml:"enabled"`
	OnDuration  Duration `yaml:"on_duration"`
	OffDuration Duration `yaml:"off_duration"`
	Variation   Duration `yaml:"variation"`
}

// Features toggles behavior groups.
type Features struct {
	Commands       map[string]bool `yaml:"commands"`
	AutoBuy        AutoBuy         `yaml:"auto_buy"`
	AutoShare      AutoShare       `yaml:"auto_share"`
	AutoTidepod    AutoTidepod     `yaml:"auto_tidepod"`
	AutoDeposit    AutoDeposit     `yaml:"auto_deposit"`
	CustomCommands []CustomCommand `yaml:"custom_commands"`
	Fishing        FishingConfig   `yaml:"fishing"`
	Breaks         BreaksConfig    `yaml:"breaks"`

	// Proxies is a pool of proxy URLs. If non-empty, grinder picks one at
	// random per session for both REST + Gateway traffic. Reduces captcha
	// risk by spreading IP footprint. Formats accepted:
	//   http://host:port
	//   http://user:pass@host:port
	//   socks5://host:port
	Proxies []string `yaml:"proxies,omitempty"`

	// RandomEvents enables auto-response to DM's random "first to say X" events
	// (Anti-Rizz, Dank Memer Corp, etc). Works regardless of grinder mode.
	RandomEvents bool `yaml:"random_events,omitempty"`

	// RandomEventRoleID is the Discord role id that DM pings when a random
	// channel event spawns. Per-server; blank disables role-based detection
	// (falls back to pure phrase matching). Right-click your event role
	// in Discord → Copy Role ID (Developer Mode required) to grab it.
	RandomEventRoleID string `yaml:"random_event_role_id,omitempty"`

	// RandomEventHandlers is a per-event-type auto-respond toggle. Keys
	// match the Key field of entries in internal/instance/randomevents.go
	// (currently "first_to_say" and "reverse_reverse"). Missing keys
	// default to true — the master RandomEvents toggle is the kill switch
	// and these are opt-OUT. Lets the user disable e.g. Reverse Reverse
	// if DM changes its wording and the detector starts false-matching,
	// without turning off the whole system.
	RandomEventHandlers map[string]bool `yaml:"random_event_handlers,omitempty"`
}

// FishingConfig controls /fish catch grinder behavior.
type FishingConfig struct {
	SellMode     string `yaml:"sell_mode"`     // "coins" or "fish_tokens"
	SellStrategy string `yaml:"sell_strategy"` // "bulk" (sell when bucket full) or "per_fish" (sell each catch)
	AutoBuyRod   bool   `yaml:"auto_buy_rod"`  // attempt to buy same rod when broken
	StopOnBreak  bool   `yaml:"stop_on_break"` // pause grinder when rod breaks (if auto-buy fails)
}

// BreaksConfig toggles the fatigue-based humanized break scheduler. There
// are no knobs by design: the scheduler picks short-break timing/length
// (5-15m, randomized to the second), chunk duration (scaled off the last
// big break), and big-break size (tiered by total session runtime —
// longer runs get sleep-sized pauses). See internal/humanize/breaks.go.
type BreaksConfig struct {
	Enabled bool `yaml:"enabled"`
}

type AutoBuy struct {
	FishingPole  bool `yaml:"fishing_pole"`
	HuntingRifle bool `yaml:"hunting_rifle"`
	Shovel       bool `yaml:"shovel"`
	Laptop       bool `yaml:"laptop"`
}

type AutoShare struct {
	Enabled     bool `yaml:"enabled"`
	MinBalance  int  `yaml:"min_balance"`
	LeaveBehind int  `yaml:"leave_behind"`
}

type AutoTidepod struct {
	Enabled   bool `yaml:"enabled"`
	Lifesaver bool `yaml:"lifesaver"`
}

type AutoDeposit struct {
	Enabled   bool `yaml:"enabled"`
	Threshold int  `yaml:"threshold"`
}

type CustomCommand struct {
	Name     string   `yaml:"name"`
	Interval Duration `yaml:"interval"`
	MaxRuns  int      `yaml:"max_runs"`
	Args     []string `yaml:"args"`
}

type Humanize struct {
	TypingIndicator bool     `yaml:"typing_indicator"`
	MinDelay        Duration `yaml:"min_delay"`
	MaxDelay        Duration `yaml:"max_delay"`
	TypingVariance  Duration `yaml:"typing_variance"`
}

type CaptchaConfig struct {
	Webhook       string `yaml:"webhook"`
	PauseOnDetect bool   `yaml:"pause_on_detect"`
	MentionUserID string `yaml:"mention_user_id"`
	DesktopNotify bool   `yaml:"desktop_notify"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// Load reads, parses, defaults and validates a YAML config file at path.
// Thin wrapper around Parse for callers that want file I/O too.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse unmarshals + validates a YAML config from a byte slice. Used by
// the web backend to validate configs stored in the database without
// needing a file. Applies defaults + runs the same validation as Load.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.setDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) setDefaults() {
	if c.Humanize.MinDelay == 0 {
		c.Humanize.MinDelay = Duration(2 * time.Second)
	}
	if c.Humanize.MaxDelay == 0 {
		c.Humanize.MaxDelay = Duration(5 * time.Second)
	}
	if c.Humanize.TypingVariance == 0 {
		c.Humanize.TypingVariance = Duration(1500 * time.Millisecond)
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
}

func (c *Config) validate() error {
	if len(c.Clusters) == 0 {
		return fmt.Errorf("no clusters configured")
	}
	for i, cl := range c.Clusters {
		if cl.ChannelID == "" {
			return fmt.Errorf("cluster %d: channel_id required", i)
		}
		if cl.GuildID == "" {
			return fmt.Errorf("cluster %d: guild_id required", i)
		}
		if cl.Master.Token == "" {
			return fmt.Errorf("cluster %d: master token required", i)
		}
	}
	return nil
}
