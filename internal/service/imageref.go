package service

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// dockerTransportPrefix is the only transport prefix a tenant-supplied image
// ref may carry. It is stripped before validation so plain Docker-style refs
// and refs routed through containerd's docker transport both validate.
const dockerTransportPrefix = "docker://"

// transportPrefixes is the denylist of containerd/containers-image transport
// prefixes that must never reach the image pull path from a tenant request.
// The scan runs BEFORE reference parsing: reference.ParseDockerRef happily
// accepts some prefixed strings (e.g. `dir:foo` parses as a bare name),
// so prefix rejection is the only reliable guard here.
var transportPrefixes = []string{
	"docker-archive:",
	"oci-archive:",
	"oci:",
	"dir:",
}

// ValidateImageRef validates a tenant-supplied image reference.
//
// It strips an optional leading `docker://`, rejects every other container
// transport prefix (`oci-archive:`, `docker-archive:`, `oci:`, `dir:`), and
// requires the remainder to parse as a full Docker reference
// (host[:port]/path[:tag][@sha256:digest]) via the already-vendored
// github.com/distribution/reference package — no hand-rolled grammar.
//
// On success it returns the parsed reference so callers (task 012 wiring)
// can normalize via the reference.Named value. On failure the image MUST be
// rejected at the API boundary.
func ValidateImageRef(image string) (reference.Named, error) {
	for _, p := range transportPrefixes {
		if strings.HasPrefix(image, p) {
			return nil, fmt.Errorf("image reference transport prefix %q is not allowed: %q", p, image)
		}
	}

	if strings.HasPrefix(image, dockerTransportPrefix) {
		image = strings.TrimPrefix(image, dockerTransportPrefix)
	}

	if strings.TrimSpace(image) == "" {
		return nil, fmt.Errorf("image reference is empty")
	}

	ref, err := reference.ParseDockerRef(image)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference %q: %w", image, err)
	}
	return ref, nil
}
