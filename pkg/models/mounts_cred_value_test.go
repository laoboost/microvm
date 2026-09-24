package models

import "testing"

// Credential values are spliced into an AWS shared-credentials INI file
// (buildAWSCredentialsFile). A newline in a value injects a new profile line.
// Keys were already checked; values were not.
func TestMountSpecValidate_RejectsControlCharsInCredentialValues(t *testing.T) {
	for _, v := range []string{
		"secret\n[evil]",
		"secret\r\nx",
		"secret\x00x",
	} {
		m := MountSpec{
			Type:   MountTypeS3,
			Source: "s3://bucket/data",
			Target: "/data",
			Credentials: map[string]string{
				"access_key_id":     "AKIAEXAMPLE",
				"secret_access_key": v,
			},
		}
		if err := m.Validate("/toolbox"); err == nil {
			t.Errorf("credential value %q must be rejected", v)
		}
	}
}

func TestMountSpecValidate_AllowsPlainCredentialValues(t *testing.T) {
	m := MountSpec{
		Type:   MountTypeS3,
		Source: "s3://bucket/data",
		Target: "/data",
		Credentials: map[string]string{
			"access_key_id":     "AKIAEXAMPLE",
			"secret_access_key": "a/very+long=secret-value.1",
		},
	}
	if err := m.Validate("/toolbox"); err != nil {
		t.Fatalf("plain credential values must validate: %v", err)
	}
}

// rclone_conf is an entire rclone.conf and private_key_pem is a PEM — both are
// inherently multi-line and are NOT spliced into the line-oriented AWS
// credentials INI. Only the AWS INI keys (access_key_id, secret_access_key,
// session_token) must be single-line; blanket newline rejection breaks these
// two mount types at the API.
func TestMountSpecValidate_AllowsMultiLineCredentialValues(t *testing.T) {
	cases := []MountSpec{
		{
			Type:   MountTypeRclone,
			Source: "x:bucket/path",
			Target: "/data",
			Credentials: map[string]string{
				"rclone_conf": "[x]\ntype = s3\nprovider = AWS\n",
			},
		},
		{
			Type:   MountTypeSSHFS,
			Source: "user@host:/path",
			Target: "/data",
			Credentials: map[string]string{
				"private_key_pem": "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n",
			},
		},
	}
	for i := range cases {
		if err := cases[i].Validate("/toolbox"); err != nil {
			t.Errorf("multi-line credential value must validate (type %q): %v", cases[i].Type, err)
		}
	}
}
