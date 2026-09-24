package adapters

import (
	"strings"
	"testing"

	"github.com/aerol-ai/microvm/pkg/models"
)

// Ambient (instance-role) credentials are signed by the host's credential
// chain. Handing mount-s3 a caller-supplied --endpoint-url in that mode turns
// the node's IAM role into a signing oracle for whatever host the caller
// names. Endpoints are therefore only allowed alongside static keys.
func TestS3Build_RejectsEndpointWithAmbientCredentials(t *testing.T) {
	opts := map[string]string{"endpoint": "http://attacker.tld"}
	_, err := (S3{}).Build("sb-1", 0, models.MountSpec{
		Source:  "s3://bucket/data",
		Options: opts,
	}, "/mnt/target", "/creds")
	if err == nil {
		t.Fatal("expected error: endpoint override with ambient (no static) credentials must be rejected")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("error should mention endpoint, got: %v", err)
	}
}

// A partial credential set is not a usable static profile: mount-s3 would sign
// with an incomplete profile, fall through the SDK chain to IMDS, and still
// send the node role's signature to the caller-named endpoint. Only a complete
// access_key_id + secret_access_key pair authorizes an endpoint override — a
// session token alone does not count.
func TestS3Build_RejectsEndpointWithIncompleteStaticCredentials(t *testing.T) {
	for _, creds := range []map[string]string{
		{"access_key_id": "AKIAEXAMPLE"},
		{"secret_access_key": "secret"},
		{"access_key_id": "AKIAEXAMPLE", "session_token": "tok"},
	} {
		_, err := (S3{}).Build("sb-1", 0, models.MountSpec{
			Source:      "s3://bucket/data",
			Credentials: creds,
			Options:     map[string]string{"endpoint": "http://attacker.tld"},
		}, "/mnt/target", "/creds")
		if err == nil {
			t.Fatalf("Build(creds=%v) accepted an endpoint override with incomplete static creds", creds)
		}
		if !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("error should mention endpoint, got: %v", err)
		}
	}
}

// A partial key set is not enough to write the sandbox profile; it must fall
// back to the ambient chain rather than pin an incomplete profile.
func TestS3Build_IncompleteStaticCredsFallBackToAmbient(t *testing.T) {
	plan, err := (S3{}).Build("sb-1", 0, models.MountSpec{
		Source:      "s3://bucket/data",
		Credentials: map[string]string{"access_key_id": "AKIAEXAMPLE"},
	}, "/mnt/target", "/creds")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if contains(plan.Argv, "--profile") {
		t.Fatalf("incomplete creds must not pin a profile: %v", plan.Argv)
	}
	if plan.CredBody != nil {
		t.Fatalf("incomplete creds must not write a credentials file: %q", plan.CredBody)
	}
}

func TestS3Build_AllowsEndpointWithStaticCredentials(t *testing.T) {
	plan, err := (S3{}).Build("sb-1", 0, models.MountSpec{
		Source: "s3://bucket/data",
		Credentials: map[string]string{
			"access_key_id":     "AKIAEXAMPLE",
			"secret_access_key": "secret",
		},
		Options: map[string]string{"endpoint": "http://minio.internal:9000"},
	}, "/mnt/target", "/creds")
	if err != nil {
		t.Fatalf("Build with static creds + endpoint: %v", err)
	}
	if !contains(plan.Argv, "--endpoint-url") {
		t.Fatalf("argv missing endpoint: %v", plan.Argv)
	}
}
