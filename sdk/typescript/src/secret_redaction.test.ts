import assert from "node:assert/strict";
import test from "node:test";

import { MicroVM } from "./MicroVM.js";
import type { Sandbox } from "./types.js";

const sandboxPayload = {
  id: "sb-secret",
  image: "ubuntu:22.04",
  status: "started",
  public_url: "https://sb-secret.example.com",
  cpu: 2,
  memory_mb: 2048,
  disk_gb: 20,
  os_user: "root",
  network_block_all: false,
  toolbox_enabled: true,
  ssh_public_key: "ssh-ed25519 AAAA sandbox",
  ssh_private_key: "PRIVATE-KEY-MATERIAL",
  exposed_ports: [],
  created_at: "2026-05-07T10:00:00Z",
  updated_at: "2026-05-07T10:00:00Z",
  last_active_at: "2026-05-07T10:00:00Z",
};

function sdkWithSandboxFetch(): MicroVM {
  return new MicroVM({
    patToken: "pat-token-secret",
    apiUrl: "https://api.example.com",
    fetch: async () =>
      new Response(JSON.stringify(sandboxPayload), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
  });
}

test("MicroVM PAT is not an enumerable property and never serializes", () => {
  const sdk = sdkWithSandboxFetch();
  assert.equal(sdk.patToken, "pat-token-secret", "patToken must stay readable");

  const json = JSON.stringify(sdk);
  assert.ok(!json.includes("pat-token-secret"), `JSON.stringify leaked PAT: ${json}`);
  assert.ok(!Object.keys(sdk).includes("patToken"), "patToken must not be an enumerable own property");
});

test("Sandbox toJSON and JSON.stringify drop sshPrivateKey and the client", async () => {
  const sdk = sdkWithSandboxFetch();
  const sandbox = await sdk.create({ image: "ubuntu:22.04" });

  assert.equal(sandbox.sshPrivateKey, "PRIVATE-KEY-MATERIAL", "key stays readable as a property");

  const plain = sandbox.toJSON();
  assert.equal(plain.sshPrivateKey, undefined, "toJSON() must drop sshPrivateKey by default");

  const json = JSON.stringify(sandbox);
  assert.ok(!json.includes("PRIVATE-KEY-MATERIAL"), `JSON.stringify leaked sshPrivateKey: ${json}`);
  assert.ok(!json.includes("pat-token-secret"), `JSON.stringify leaked the PAT via client: ${json}`);

  const withSecrets = (
    sandbox.toJSON.bind(sandbox) as unknown as (options?: { includeSecrets?: boolean }) => Sandbox
  )({ includeSecrets: true });
  assert.equal(withSecrets.sshPrivateKey, "PRIVATE-KEY-MATERIAL");
});
