// Package config loads and validates the daemon configuration from a YAML file.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level daemon configuration.
type Config struct {
	Matrix Matrix `yaml:"matrix"`
	Vinted Vinted `yaml:"vinted"`
	Poll   Poll   `yaml:"poll"`
	DBPath string `yaml:"db_path"`
}

// Matrix holds Matrix homeserver connection details.
type Matrix struct {
	Homeserver  string `yaml:"homeserver"`
	UserID      string `yaml:"user_id"`
	AccessToken string `yaml:"access_token"`
	RoomID      string `yaml:"room_id"`
}

// Vinted selects which Vinted site to search.
type Vinted struct {
	// Domain is the Vinted site host, e.g. "www.vinted.pt" or "www.vinted.fr".
	// It sets the market searched, the language of labels (condition,
	// categories) and the links posted to the room. A scheme, trailing slash or
	// missing "www." are tolerated and normalised away.
	Domain string `yaml:"domain"`
}

// Poll holds polling behaviour settings.
type Poll struct {
	IntervalSeconds int `yaml:"interval_seconds"`
	MaxPages        int `yaml:"max_pages"`

	// JitterPercent spreads each tick randomly by ±this percentage of the
	// interval, so the daemon does not hit Vinted on a perfectly regular beat.
	// A pointer so an explicit "jitter_percent: 0" disables jitter rather than
	// reading as unset.
	JitterPercent *int `yaml:"jitter_percent"`
}

// Jitter returns the tick spread as a fraction of the interval (0.1 = ±10%).
func (p Poll) Jitter() float64 {
	if p.JitterPercent == nil {
		return 0.1
	}
	return float64(*p.JitterPercent) / 100
}

// Load reads, parses and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Poll.IntervalSeconds == 0 {
		c.Poll.IntervalSeconds = 300
	}
	if c.Poll.MaxPages == 0 {
		c.Poll.MaxPages = 2
	}
	if c.DBPath == "" {
		c.DBPath = "./vinted.db"
	}
	c.Vinted.Domain = normalizeDomain(c.Vinted.Domain)
}

// domainRe matches a Vinted site host: www.vinted.<tld>, including two-part
// TLDs such as vinted.co.uk and vinted.com.au.
var domainRe = regexp.MustCompile(`^www\.vinted\.[a-z]{2,3}(\.[a-z]{2})?$`)

// normalizeDomain turns "https://vinted.fr/" or "Vinted.FR" into "www.vinted.fr".
func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimRight(d, "/")
	if d != "" && !strings.HasPrefix(d, "www.") {
		d = "www." + d
	}
	return d
}

func (c *Config) validate() error {
	switch {
	case c.Matrix.Homeserver == "":
		return fmt.Errorf("matrix.homeserver is required")
	case c.Matrix.UserID == "":
		return fmt.Errorf("matrix.user_id is required")
	case c.Matrix.AccessToken == "":
		return fmt.Errorf("matrix.access_token is required")
	case c.Matrix.RoomID == "":
		return fmt.Errorf("matrix.room_id is required")
	case c.Vinted.Domain == "":
		return fmt.Errorf("vinted.domain is required, e.g. \"www.vinted.pt\" or \"www.vinted.fr\"")
	case !domainRe.MatchString(c.Vinted.Domain):
		return fmt.Errorf("vinted.domain %q is not a Vinted site (expected e.g. \"www.vinted.fr\")", c.Vinted.Domain)
	case c.Poll.IntervalSeconds < 30:
		return fmt.Errorf("poll.interval_seconds must be >= 30 to avoid hammering Vinted")
	case c.Poll.JitterPercent != nil && (*c.Poll.JitterPercent < 0 || *c.Poll.JitterPercent > 50):
		return fmt.Errorf("poll.jitter_percent must be between 0 and 50")
	}
	return nil
}
