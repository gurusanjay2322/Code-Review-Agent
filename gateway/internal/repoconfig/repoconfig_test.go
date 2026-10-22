package repoconfig

import "testing"

func TestExcludeAndInclude(t *testing.T) {
	c, err := Parse([]byte("include: [\"src/**\"]\nexclude: [\"src/legacy/**\"]\nmin_severity: medium\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Excluded("src/app/main.go") != "" {
		t.Error("src/app/main.go should be included")
	}
	if c.Excluded("src/legacy/old.go") == "" {
		t.Error("legacy should be excluded")
	}
	if c.Excluded("docs/readme.md") == "" {
		t.Error("paths outside include should be excluded")
	}
	if c.MinSeverity != "medium" {
		t.Errorf("min_severity = %q", c.MinSeverity)
	}
}

func TestNilConfigAndDisabled(t *testing.T) {
	var c *Config
	if !c.IsEnabled() || c.Excluded("a.go") != "" || c.ReviewInstructions() != "" {
		t.Error("nil config should allow everything")
	}
	off, _ := Parse([]byte("enabled: false"))
	if off.IsEnabled() {
		t.Error("enabled: false not honored")
	}
}

func TestInvalidGlob(t *testing.T) {
	if _, err := Parse([]byte("exclude: [\"[\"]")); err == nil {
		t.Fatal("expected invalid glob error")
	}
}
