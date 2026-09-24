package microvm

import (
	"fmt"
	"strings"
	"testing"

	sdktypes "github.com/aerol-ai/microvm/sdk/go/pkg/types"
)

// The Client holds the PAT and Sandbox holds the per-sandbox SSH private key;
// neither may appear under fmt's default verb.
func TestClientAndSandboxRedactSecretsUnderFmt(t *testing.T) {
	const pat = "avm_deadbeefdeadbeefdeadbeefdeadbeef"
	const sshKey = "PRIVATE-KEY-MATERIAL"

	client, err := NewClientWithConfig(&sdktypes.MicroVMConfig{PATToken: pat, APIUrl: "http://127.0.0.1:21212"})
	if err != nil {
		t.Fatalf("NewClientWithConfig: %v", err)
	}
	if got := fmt.Sprintf("%+v", client); strings.Contains(got, pat) {
		t.Fatalf("Client %%+v leaked PAT: %s", got)
	}

	sandbox := &Sandbox{
		Sandbox:       sdktypes.Sandbox{ID: "sb", Image: "alpine", Status: "started"},
		SSHPrivateKey: sshKey,
		client:        client,
	}
	got := fmt.Sprintf("%+v", sandbox)
	if strings.Contains(got, sshKey) {
		t.Fatalf("Sandbox %%+v leaked ssh private key: %s", got)
	}
	if strings.Contains(got, pat) {
		t.Fatalf("Sandbox %%+v leaked the client PAT: %s", got)
	}
}
