package repoconfig

import (
	"fmt"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Path is read from the PR base commit so a PR cannot weaken its own review.
const Path = ".github/ai-review.yml"

type Config struct {
	Enabled       *bool    `yaml:"enabled"`
	Include       []string `yaml:"include"`
	Exclude       []string `yaml:"exclude"`
	MinSeverity   string   `yaml:"min_severity"`
	MinConfidence float64  `yaml:"min_confidence"`
	MaxComments   int      `yaml:"max_comments"`
	Instructions  string   `yaml:"instructions"`
}

func Parse(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path, err)
	}
	for _, p := range append(append([]string{}, c.Include...), c.Exclude...) {
		if !doublestar.ValidatePattern(p) {
			return nil, fmt.Errorf("parse %s: invalid glob %q", Path, p)
		}
	}
	return &c, nil
}

func (c *Config) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

func (c *Config) ReviewInstructions() string {
	if c == nil {
		return ""
	}
	return c.Instructions
}

// Excluded returns the matching rule, or "" if the path should be reviewed.
func (c *Config) Excluded(path string) string {
	if c == nil {
		return ""
	}
	for _, p := range c.Exclude {
		if ok, _ := doublestar.Match(p, path); ok {
			return "excluded by " + Path + " (" + p + ")"
		}
	}
	if len(c.Include) == 0 {
		return ""
	}
	for _, p := range c.Include {
		if ok, _ := doublestar.Match(p, path); ok {
			return ""
		}
	}
	return "not matched by include patterns in " + Path
}
