import assert from "node:assert/strict";
import test from "node:test";

import { APIClient } from "./client.js";

// A hostile id concatenated raw into a URL path traverses out of its route
// (x/../admin becomes /v1/admin). Escaped, it stays one literal segment.
test("resource IDs are percent-escaped in URL paths", async () => {
  const seen: string[] = [];
  const client = new APIClient({
    baseURL: "https://api.example.com",
    patToken: "pat-token",
    fetch: async (input) => {
      seen.push(String(input));
      return new Response(`{"custom_domains":[]}`, {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    },
  });

  const hostile = "x/../admin";
  await client.get(hostile);
  await client.destroy(hostile);
  await client.getTemplate(hostile);
  await client.deleteTemplate(hostile);
  await client.getWasmModule(hostile);
  await client.getSession(hostile, hostile);
  await client.deleteSession(hostile, hostile);
  await client.exposePort(hostile, 8080);
  await client.addCustomDomain(hostile, "api.example.com");
  await client.removeCustomDomain(hostile, "api.example.com");
  await client.customDomainDNS(hostile);

  const escaped = "x%2F..%2Fadmin";
  const want = [
    `https://api.example.com/v1/sandboxes/${escaped}`,
    `https://api.example.com/v1/sandboxes/${escaped}`,
    `https://api.example.com/v1/templates/${escaped}`,
    `https://api.example.com/v1/templates/${escaped}`,
    `https://api.example.com/v1/wasm-modules/${escaped}`,
    `https://api.example.com/v1/sandboxes/${escaped}/sessions/${escaped}`,
    `https://api.example.com/v1/sandboxes/${escaped}/sessions/${escaped}`,
    `https://api.example.com/v1/sandboxes/${escaped}/ports/8080`,
    `https://api.example.com/v1/sandboxes/${escaped}/custom-domains`,
    `https://api.example.com/v1/sandboxes/${escaped}/custom-domains/api.example.com`,
    `https://api.example.com/v1/sandboxes/${escaped}/custom-domains/dns`,
  ];
  assert.deepEqual(seen, want);
});
