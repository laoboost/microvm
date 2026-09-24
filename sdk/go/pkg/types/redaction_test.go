package types

import (
	"fmt"
	"strings"
	"testing"
)

// Every secret-bearing type in this package must render without the secret
// under fmt's default verb (Rust and TypeScript were hardened on this axis;
// Go structs printed the raw PAT/password).
func TestSecretBearingTypesRedactUnderFmt(t *testing.T) {
	const pat = "avm_deadbeefdeadbeefdeadbeefdeadbeef"
	const password = "hunter2"

	cfg := MicroVMConfig{PATToken: pat, APIUrl: "https://api.example.com", APIVersion: "v1"}
	if got := fmt.Sprintf("%+v", cfg); strings.Contains(got, pat) {
		t.Fatalf("MicroVMConfig %%+v leaked PAT: %s", got)
	} else if !strings.Contains(got, "avm_") {
		t.Fatalf("MicroVMConfig %%+v dropped the token prefix: %s", got)
	}

	push := BuildImagePushOptions{Registry: "ghcr.io/x/y", Tag: "v1", Server: "ghcr.io", Username: "u", Password: password}
	if got := fmt.Sprintf("%+v", push); strings.Contains(got, password) {
		t.Fatalf("BuildImagePushOptions %%+v leaked password: %s", got)
	}
	if got := fmt.Sprintf("%v", push); strings.Contains(got, password) {
		t.Fatalf("BuildImagePushOptions %%v leaked password: %s", got)
	}
}

func TestRedactToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"avm_abc123", "avm_***"},
		{"avm_", "***"},
		{"opaque-secret", "***"},
	}
	for _, tc := range cases {
		if got := RedactToken(tc.in); got != tc.want {
			t.Errorf("RedactToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
