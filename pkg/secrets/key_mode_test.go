package secrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadOrGenerateKey must apply the same mode policy as
// wrap_key_loader.go: a key file with group/world bits or a mode looser
// than 0600/0400 is refused before its bytes are trusted — otherwise a
// world-readable `cp -p` leftover silently leaks the credential
// encryption key.

func writeKeyFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	body := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %#o: %v", mode, err)
	}
}

// it refuses a group/world-readable key file
func TestLoadOrGenerateKeyRejectsInsecureMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o666, 0o664} {
		path := filepath.Join(t.TempDir(), "key")
		writeKeyFile(t, path, mode)
		_, err := loadOrGenerateKey("", path)
		if err == nil {
			t.Fatalf("mode %#o: expected refusal", mode)
		}
		if !strings.Contains(err.Error(), "insecure mode") {
			t.Fatalf("mode %#o: error = %v, want mention of insecure mode", mode, err)
		}
	}
}

// it loads a key file at 0600 and at 0400
func TestLoadOrGenerateKeyAcceptsOwnerOnlyModes(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o400} {
		path := filepath.Join(t.TempDir(), "key")
		writeKeyFile(t, path, mode)
		key, err := loadOrGenerateKey("", path)
		if err != nil {
			t.Fatalf("mode %#o: load: %v", mode, err)
		}
		if len(key) != 32 {
			t.Fatalf("mode %#o: key length = %d, want 32", mode, len(key))
		}
	}
}
