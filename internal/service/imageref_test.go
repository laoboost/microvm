package service

import (
	"strings"
	"testing"
)

// TestValidateImageRef covers the tenant image-ref validator (task 004).
// The production ref must keep passing (COMPAT), and transport-prefixed
// refs must be rejected before any reference-grammar parsing happens.
func TestValidateImageRef(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{
			name:  "it accepts a full registry image ref with tag",
			image: "registry.example.com:5000/team/app:v2",
		},
		{
			name:  "it accepts the production agent sandbox image ref",
			image: "cr.selcloud.ru/speshu/agent-sandbox:v1",
		},
		{
			name:  "it accepts an image ref with a sha256 digest",
			image: "cr.selcloud.ru/speshu/agent-sandbox@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		{
			name:  "it accepts and strips a leading docker transport prefix",
			image: "docker://cr.selcloud.ru/speshu/agent-sandbox:v1",
		},
		{
			name:    "it rejects an oci archive transport prefix",
			image:   "oci-archive:/tmp/image.tar",
			wantErr: true,
		},
		{
			name:    "it rejects a dir transport prefix",
			image:   "dir:/var/lib/containers/storage",
			wantErr: true,
		},
		{
			name:    "it rejects an empty image after transport stripping",
			image:   "docker://",
			wantErr: true,
		},
		{
			name:    "it rejects an image ref with whitespace",
			image:   "cr.selcloud.ru/speshu/agent-sandbox :v1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ValidateImageRef(tt.image)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ValidateImageRef(%q) = %v, want error", tt.image, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateImageRef(%q) returned error: %v", tt.image, err)
			}
			if ref == nil {
				t.Fatalf("ValidateImageRef(%q) returned nil ref, want parsed reference", tt.image)
			}
			// docker:// stripping must not leak into the parsed ref.
			if strings.HasPrefix(ref.String(), "docker://") {
				t.Errorf("parsed ref %q still carries the docker transport prefix", ref.String())
			}
		})
	}
}
