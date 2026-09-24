package models

import "testing"

// Docker binds are colon-joined "src:dst[:opts]" with options split on commas,
// so a colon or comma in Target injects bind options (e.g. "/data:rshared"
// smuggles a shared-propagation mount). Validate must reject both separators.
func TestMountSpecValidate_RejectsBindOptionInjectionInTarget(t *testing.T) {
	for _, target := range []string{"/data:rshared", "/data,suid", "/data:ro", "/data:rw,z"} {
		m := MountSpec{Type: MountTypeS3, Source: "bucket", Target: target}
		if err := m.Validate(""); err == nil {
			t.Errorf("Validate(target %q) = nil, want error", target)
		}
	}

	m := MountSpec{Type: MountTypeS3, Source: "bucket", Target: "/data"}
	if err := m.Validate(""); err != nil {
		t.Errorf("Validate(target /data) = %v, want nil", err)
	}
}
