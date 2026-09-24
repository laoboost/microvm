package adapters

import (
	"fmt"
	"strings"

	"github.com/aerol-ai/microvm/pkg/models"
)

// checkSource re-validates a tenant-supplied mount source at adapter build
// time as defense-in-depth: spec validation (pkg/models) enforces the same
// rules, but a spec could reach an adapter without passing through it.
//
// A source starting with '-' would be parsed as a flag by the mount tool
// (argument injection, e.g. sshfs -oProxyCommand=... executed by host root).
// Whitespace and control bytes are refused too: the source is one argv token,
// so a space/tab/newline/NUL would split it into extra tokens or corrupt it.
// The length is capped to bound the token the mount tool has to process.
func checkSource(kind, source string) error {
	if strings.HasPrefix(source, "-") {
		return fmt.Errorf("%s source must not start with '-': %q", kind, source)
	}
	for i := 0; i < len(source); i++ {
		if c := source[i]; c <= ' ' || c == 0x7f {
			return fmt.Errorf("%s source must not contain whitespace or control bytes: %q", kind, source)
		}
	}
	if len(source) > maxSourceLength {
		return fmt.Errorf("%s source exceeds %d bytes (%d)", kind, maxSourceLength, len(source))
	}
	return nil
}

// maxSourceLength bounds a tenant-supplied mount source token. It matches
// MaxCredentialBytes (4 KiB) and comfortably exceeds any real host path or
// remote:path, so only pathological input is refused.
const maxSourceLength = 4096

// CheckNFSSource enforces the host:/path shape mount(2) expects, plus the '-'
// guard. mount(8) has no universal '--' separator, so the shape check is the
// only argv-injection defense for nfs. Exported so stored volume sources can
// be re-validated before every use (pkg/volumes reclaim).
func CheckNFSSource(source string) error {
	if err := checkSource("nfs", source); err != nil {
		return err
	}
	// host:/path — no whitespace (which would become extra argv tokens), a
	// non-empty host without '/', and a path starting with '/'.
	host, path, ok := strings.Cut(source, ":")
	if !ok || host == "" || strings.ContainsAny(host, "/ \t\n") || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\n") {
		return fmt.Errorf("nfs source must look like host:/path: %q", source)
	}
	return nil
}

// checkSSHFSSource enforces the '-' guard plus the shared user@host:/path
// grammar. The grammar itself lives in pkg/models (ValidateSSHFSSource) so the
// adapter and spec validation cannot drift; this re-checks it as
// defense-in-depth because the source reaches ssh as root via the mount tool.
func checkSSHFSSource(source string) error {
	if err := checkSource("sshfs", source); err != nil {
		return err
	}
	return models.ValidateSSHFSSource(source)
}
