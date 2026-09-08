package adapters

import (
	"fmt"
	"strings"
)

// checkSource re-validates a tenant-supplied mount source at adapter build
// time as defense-in-depth: spec validation (pkg/models) enforces the same
// rules, but a spec could reach an adapter without passing through it.
//
// A source starting with '-' would be parsed as a flag by the mount tool
// (argument injection, e.g. sshfs -oProxyCommand=... executed by host root).
func checkSource(kind, source string) error {
	if strings.HasPrefix(source, "-") {
		return fmt.Errorf("%s source must not start with '-': %q", kind, source)
	}
	return nil
}

// checkNFSSource enforces the host:/path shape mount(2) expects, plus the '-'
// guard. mount(8) has no universal '--' separator, so the shape check is the
// only argv-injection defense for nfs.
func checkNFSSource(source string) error {
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
