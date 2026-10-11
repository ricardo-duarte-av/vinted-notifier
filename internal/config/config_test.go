package config

import (
	"os"
	"path/filepath"
	"testing"
)

const baseConfig = `matrix:
  homeserver: "https://matrix.org"
  user_id: "@bot:matrix.org"
  access_token: "tok"
  room_id: "!room:matrix.org"
`

func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func writeConfig(t *testing.T, pollExtra string) *Config {
	t.Helper()
	c, err := load(t, baseConfig+`vinted:
  domain: "www.vinted.pt"
poll:
  interval_seconds: 300
`+pollExtra)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestJitterDefaultsToTenPercent(t *testing.T) {
	if got := writeConfig(t, "").Poll.Jitter(); got != 0.1 {
		t.Fatalf("Jitter() = %v, want 0.1", got)
	}
}

// An explicit zero must disable jitter, not read as "unset".
func TestJitterExplicitZeroDisables(t *testing.T) {
	if got := writeConfig(t, "  jitter_percent: 0\n").Poll.Jitter(); got != 0 {
		t.Fatalf("Jitter() = %v, want 0", got)
	}
}

func TestJitterExplicitValue(t *testing.T) {
	if got := writeConfig(t, "  jitter_percent: 25\n").Poll.Jitter(); got != 0.25 {
		t.Fatalf("Jitter() = %v, want 0.25", got)
	}
}

func TestJitterOutOfRangeRejected(t *testing.T) {
	if _, err := load(t, baseConfig+"vinted:\n  domain: www.vinted.pt\npoll:\n  jitter_percent: 80\n"); err == nil {
		t.Fatal("expected jitter_percent: 80 to be rejected")
	}
}

func TestDomainNormalised(t *testing.T) {
	cases := map[string]string{
		"www.vinted.fr":          "www.vinted.fr",
		"vinted.pt":              "www.vinted.pt",
		"https://www.vinted.de/": "www.vinted.de",
		"  Vinted.CO.UK ":        "www.vinted.co.uk",
		"http://vinted.com.au":   "www.vinted.com.au",
	}
	for in, want := range cases {
		c, err := load(t, baseConfig+"vinted:\n  domain: \""+in+"\"\n")
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if c.Vinted.Domain != want {
			t.Errorf("%q: got %q want %q", in, c.Vinted.Domain, want)
		}
	}
}

func TestDomainRequiredAndValidated(t *testing.T) {
	if _, err := load(t, baseConfig); err == nil {
		t.Error("missing vinted.domain should be rejected")
	}
	for _, bad := range []string{"www.olx.pt", "vinted", "www.vinted.fr/catalog", "evil.com/vinted.fr"} {
		if _, err := load(t, baseConfig+"vinted:\n  domain: \""+bad+"\"\n"); err == nil {
			t.Errorf("domain %q should be rejected", bad)
		}
	}
}
