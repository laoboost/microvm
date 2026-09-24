package types

import (
	"fmt"
	"strings"
)

// RedactToken renders a credential for fmt without exposing the secret. PAT
// tokens carry an "avm_" prefix; that prefix is kept so a log line still
// identifies the credential, and the remainder is elided.
func RedactToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if i := strings.IndexByte(token, '_'); i >= 0 && i < len(token)-1 {
		return token[:i+1] + "***"
	}
	return "***"
}

// redactSecret renders an opaque secret (e.g. a registry password) as "***"
// when set, leaving it empty otherwise. Unlike RedactToken no fragment is kept
// — a password has no safe-to-log prefix.
func redactSecret(secret string) string {
	if secret == "" {
		return ""
	}
	return "***"
}

// String redacts PATToken so MicroVMConfig is safe to log with %v / %+v.
func (c MicroVMConfig) String() string {
	httpClient := "nil"
	if c.HTTPClient != nil {
		httpClient = "set"
	}
	return fmt.Sprintf("MicroVMConfig{PATToken: %s, APIUrl: %q, APIVersion: %q, HTTPClient: %s}",
		RedactToken(c.PATToken), c.APIUrl, c.APIVersion, httpClient)
}

// String redacts Password so BuildImagePushOptions is safe to log.
func (o BuildImagePushOptions) String() string {
	return fmt.Sprintf("BuildImagePushOptions{Registry: %q, Tag: %q, Server: %q, Username: %q, Password: %s}",
		o.Registry, o.Tag, o.Server, o.Username, redactSecret(o.Password))
}
