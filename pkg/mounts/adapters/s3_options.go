package adapters

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// s3StructuredFlags renders the daemon-pinned structured option keys as
// mount-s3 flags, in a fixed order placed BEFORE extra_args. Empty/absent
// keys are omitted; booleans accept "true"/"1".
func s3StructuredFlags(opts map[string]string) []string {
	var argv []string
	if v := opts["uid"]; v != "" {
		argv = append(argv, "--uid", v)
	}
	if v := opts["gid"]; v != "" {
		argv = append(argv, "--gid", v)
	}
	if isS3True(opts["allow_other"]) {
		argv = append(argv, "--allow-other")
	}
	if isS3True(opts["allow_overwrite"]) {
		argv = append(argv, "--allow-overwrite")
	}
	return argv
}

// validateS3StructuredOptions enforces positive-integer uid/gid. mount-s3
// itself parses --uid/--gid with value_parser!(u32).range(1..) — 0 is rejected
// by the tool, so the daemon rejects it too rather than emitting a mount that
// can never succeed.
func validateS3StructuredOptions(opts map[string]string) error {
	for _, key := range []string{"uid", "gid"} {
		v := opts[key]
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("s3 option %q must be a positive integer (>= 1), got %q", key, v)
		}
	}
	return nil
}

func isS3True(v string) bool {
	return v == "true" || v == "1"
}

// validateS3Endpoint enforces that a non-empty endpoint is an absolute
// http/https URL. Private-network http (MinIO-style endpoints) is explicitly
// allowed. Scheme-less values like "host:9000" are rejected: url.Parse would
// interpret them as scheme="host", and mount-s3 would fail at runtime — better
// to fail at validate time with a clear message.
func validateS3Endpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("s3 endpoint %q is not a valid URL: %w", endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("s3 endpoint %q must be an absolute http:// or https:// URL", endpoint)
	}
	return nil
}

// s3ExtraArgsTokens splits extra_args into argv tokens and enforces the
// denylist of flags that conflict with daemon-pinned options. mountpoint-s3
// uses clap WITHOUT args_override_self, so a duplicated flag is a hard parse
// error, not last-wins — the denylist is the only conflict protection.
//
// mixedSpec must be true when any structured key (uid/gid/allow_*) is set:
// then --uid/--gid/--allow-* are also denied so extra_args can never
// contradict the structured block.
func s3ExtraArgsTokens(extra string, mixedSpec bool) ([]string, error) {
	deny := map[string]bool{
		"--prefix":       true,
		"--endpoint-url": true,
		"--profile":      true,
		"--region":       true,
		"--read-only":    true,
	}
	if mixedSpec {
		deny["--uid"] = true
		deny["--gid"] = true
		deny["--allow-other"] = true
		deny["--allow-overwrite"] = true
	}
	var deprecation string
	if mixedSpec {
		deprecation = "; uid/gid/allow-* in extra_args are deprecated — use the structured uid/gid/allow_other/allow_overwrite option keys"
	}

	seen := map[string]bool{}
	var tokens []string
	for _, tok := range strings.Fields(extra) {
		if tok == "--" {
			return nil, fmt.Errorf("s3 extra_args must not contain a bare '--' token")
		}
		flag := tok
		if i := strings.Index(tok, "="); i > 0 && strings.HasPrefix(tok, "--") {
			flag = tok[:i]
		}
		if deny[flag] {
			return nil, fmt.Errorf("s3 extra_args must not contain %s (it is pinned by the daemon; mount-s3 rejects duplicate flags)%s", flag, deprecation)
		}
		if strings.HasPrefix(flag, "--") && seen[flag] {
			return nil, fmt.Errorf("s3 extra_args contains duplicate flag %s (mount-s3 rejects repeated flags)", flag)
		}
		seen[flag] = true
		tokens = append(tokens, tok)
	}
	return tokens, nil
}
