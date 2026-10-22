// Package config reads and writes .keywatch.toml, the per-repo file teams
// commit so ignore decisions and labels are shared.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const File = ".keywatch.toml"

type Ignore struct {
	Finding string `toml:"finding"` // e.g. "F2"
	KeyID   string `toml:"key"`     // last 4, e.g. "a91f"
	Reason  string `toml:"reason"`
}

type Config struct {
	Labels   map[string]string `toml:"labels,omitempty"` // key id -> label
	Ignores  []Ignore          `toml:"ignore,omitempty"`
	ScaryDay float64           `toml:"scary_day_usd,omitempty"`
}

func Load(dir string) (*Config, error) {
	c := &Config{Labels: map[string]string{}}
	_, err := toml.DecodeFile(filepath.Join(dir, File), c)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	return c, err
}

func (c *Config) Save(dir string) error {
	f, err := os.Create(filepath.Join(dir, File))
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

func (c *Config) Ignored(finding, keyID string) bool {
	for _, i := range c.Ignores {
		if i.Finding == finding && i.KeyID == keyID {
			return true
		}
	}
	return false
}
