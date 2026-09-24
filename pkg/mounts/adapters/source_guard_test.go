package adapters

import (
	"strings"
	"testing"
)

// checkSource runs at adapter build time, before the source reaches a mount
// tool's argv. It historically only rejected a leading '-'; whitespace/control
// bytes (which split into extra argv tokens or corrupt the token) and an
// unbounded length must be rejected too.
func TestCheckSourceHardening(t *testing.T) {
	rejected := []struct {
		name   string
		source string
	}{
		{"leading_space", " leading"},
		{"trailing_space", "trailing "},
		{"embedded_space", "a b"},
		{"tab", "a\tb"},
		{"newline", "a\nb"},
		{"carriage_return", "a\rb"},
		{"nul", "a\x00b"},
		{"del", "a\x7fb"},
		{"empty_before_remote", " :/path"},
		{"leading_dash", "-oProxyCommand=x"},
		{"too_long", strings.Repeat("a", 4097)},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource("rclone", tc.source); err == nil {
				t.Fatalf("checkSource(%q) = nil, want rejection", tc.source)
			}
		})
	}

	accepted := []string{"remote:path", "x:bucket/prefix", "bucket/prefix", "user@host:/path"}
	for _, source := range accepted {
		if err := checkSource("rclone", source); err != nil {
			t.Errorf("checkSource(%q) = %v, want nil", source, err)
		}
	}
	// A source at exactly the cap is still allowed; one byte over is rejected.
	if err := checkSource("rclone", strings.Repeat("a", 4096)); err != nil {
		t.Errorf("source at the length cap rejected: %v", err)
	}
}
