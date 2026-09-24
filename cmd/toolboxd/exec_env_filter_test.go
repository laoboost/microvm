package main

import (
	"strings"
	"testing"
)

// Requirement (review H1): toolboxd runs execs as ROOT and merges the request
// env straight into that root /bin/sh. Caller-supplied privilege-escalation
// keys (LD_PRELOAD, BASH_ENV, PATH, ...) hijack the root wrapper before any
// setpriv drop the control plane embeds in the command string. This filter is
// the enforcement boundary: even a compromised or older control plane must not
// be able to hand the root shell a hijack vector. Only the CALLER-SUPPLIED map
// is filtered — the container's own os.Environ() is trusted base state.

func TestMergeEnvForExec_DropsPrivilegeEscalationKeys(t *testing.T) {
	merged := mergeEnvForExec(map[string]string{
		"SAFE_VAR":         "1",
		"LD_PRELOAD":       "/workspace/evil.so",
		"LD_LIBRARY_PATH":  "/workspace/lib",
		"LD_AUDIT":         "/workspace/audit.so",
		"BASH_ENV":         "/workspace/evil.sh",
		"ENV":              "/workspace/evil.sh",
		"SHELLOPTS":        "xtrace",
		"BASHOPTS":         "xtrace",
		"PS4":              "$(id)",
		"GCONV_PATH":       "/workspace/gconv",
		"LOCPATH":          "/workspace/locale",
		"HOSTALIASES":      "/workspace/hosts",
		"IFS":              "-",
		"BASH_FUNC_evil%%": "() { id; }",
		"A=B":              "injected",
		"PATH":             "/workspace/bin:/usr/bin",
	})

	// These exact caller-supplied assignments must never appear in the root
	// wrapper env. (The base os.Environ() may legitimately carry its own PATH.)
	for _, banned := range []string{
		"LD_PRELOAD=/workspace/evil.so",
		"LD_LIBRARY_PATH=/workspace/lib",
		"LD_AUDIT=/workspace/audit.so",
		"BASH_ENV=/workspace/evil.sh",
		"ENV=/workspace/evil.sh",
		"SHELLOPTS=xtrace",
		"BASHOPTS=xtrace",
		"PS4=$(id)",
		"GCONV_PATH=/workspace/gconv",
		"LOCPATH=/workspace/locale",
		"HOSTALIASES=/workspace/hosts",
		"IFS=-",
		"BASH_FUNC_evil%%=() { id; }",
		"A=B=injected",
		"PATH=/workspace/bin:/usr/bin",
	} {
		for _, item := range merged {
			if item == banned {
				t.Fatalf("mergeEnvForExec leaked privileged assignment %q into the root wrapper env", banned)
			}
		}
	}

	// Any caller key containing '=' is an injection vector (KEY=A=B becomes a
	// different variable). Reject them.
	for _, item := range merged {
		key, _, _ := strings.Cut(item, "=")
		if key == "A=B" {
			t.Fatalf("mergeEnvForExec leaked a key containing '=' (item %q)", item)
		}
	}

	// Ordinary caller env still reaches the command.
	found := false
	for _, item := range merged {
		if item == "SAFE_VAR=1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mergeEnvForExec dropped the ordinary SAFE_VAR key: %v", merged)
	}
}

func TestMergeEnvForExec_AllowsOrdinaryUserEnv(t *testing.T) {
	merged := mergeEnvForExec(map[string]string{
		"MY_APP_MODE": "debug",
		"MY_TOKEN":    "abc",
	})

	found := map[string]bool{}
	for _, item := range merged {
		if item == "MY_APP_MODE=debug" || item == "MY_TOKEN=abc" {
			found[item] = true
		}
	}
	if !found["MY_APP_MODE=debug"] || !found["MY_TOKEN=abc"] {
		t.Fatalf("mergeEnvForExec dropped ordinary user env keys: %v", merged)
	}
}
