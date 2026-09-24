import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";

import { APIClient, OpaqueRedirectError } from "./client.js";
import { Image } from "../Image.js";

interface Recorded {
  url: string;
  headers: Record<string, string>;
  body: string;
}

type Handler = (req: IncomingMessage, res: ServerResponse, body: string) => void;

async function startServer(handler: Handler): Promise<{ url: string; close: () => Promise<void> }> {
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => handler(req, res, body));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

function recordInto(sink: Recorded[]): Handler {
  return (req, res, body) => {
    sink.push({
      url: req.url ?? "",
      headers: Object.fromEntries(
        Object.entries(req.headers).map(([k, v]) => [k, Array.isArray(v) ? v.join(",") : (v ?? "")]),
      ),
      body,
    });
    res.setHeader("content-type", "application/json");
    res.end(`{"image":"built:latest"}`);
  };
}

test("cross-origin redirect strips Authorization and X-Registry-* headers", async () => {
  const seen: Recorded[] = [];
  const target = await startServer(recordInto(seen));
  const redirector = await startServer((_req, res) => {
    res.statusCode = 302;
    res.setHeader("Location", `${target.url}/v1/wasm-modules/push`);
    res.end();
  });

  try {
    const client = new APIClient({ baseURL: redirector.url, patToken: "pat-token" });
    await client.pushWasmModule({
      name: "mod",
      tag: "latest",
      module: new Uint8Array([1, 2, 3]),
      registryToken: "registry-token-secret",
      registryUsername: "registry-user",
    });

    assert.equal(seen.length, 1, "redirect should be followed");
    const headers = seen[0].headers;
    assert.equal(headers["authorization"], undefined, `Authorization leaked: ${headers["authorization"]}`);
    assert.equal(headers["x-registry-token"], undefined, `X-Registry-Token leaked: ${headers["x-registry-token"]}`);
    assert.equal(headers["x-registry-username"], undefined, `X-Registry-Username leaked: ${headers["x-registry-username"]}`);
  } finally {
    await redirector.close();
    await target.close();
  }
});

test("cross-origin 307 refuses to replay credential-bearing request bodies", async () => {
  const seen: Recorded[] = [];
  const target = await startServer(recordInto(seen));
  const redirector = await startServer((_req, res) => {
    res.statusCode = 307;
    res.setHeader("Location", `${target.url}/v1/images/build`);
    res.end();
  });

  try {
    const client = new APIClient({ baseURL: redirector.url, patToken: "pat-token" });
    let threw = false;
    try {
      await client.buildImage(
        Image.base("alpine"),
        {
          push: {
            registry: "ghcr.io/acme/app",
            username: "acme",
            password: "super-secret-push-password",
          },
        },
      );
    } catch {
      threw = true;
    }
    for (const [i, rec] of seen.entries()) {
      assert.ok(
        !rec.body.includes("super-secret-push-password"),
        `cross-origin redirect replayed request body ${i} containing credentials`,
      );
    }
    assert.ok(
      threw || seen.length === 0,
      `cross-origin 307 with a credential-bearing body was followed (hits=${seen.length}); want refusal`,
    );
  } finally {
    await redirector.close();
    await target.close();
  }
});

test("an opaque-redirect response raises a named error instead of a body-less TypeError", async () => {
  // Browsers report a manual cross-origin redirect as an opaque-redirect
  // response: status 0, no readable Location, empty body. Returning it as-is
  // makes every legitimate 3xx look like a body-less failure.
  const opaqueFetch = (async () => {
    const response = new Response(null, { status: 200 });
    Object.defineProperty(response, "type", { value: "opaqueredirect" });
    Object.defineProperty(response, "status", { value: 0 });
    Object.defineProperty(response, "ok", { value: false });
    return response;
  }) as unknown as typeof fetch;

  const client = new APIClient({ baseURL: "https://api.example.com", patToken: "pat-token", fetch: opaqueFetch });
  await assert.rejects(client.health(), (error: unknown) => {
    assert.ok(error instanceof OpaqueRedirectError, `want OpaqueRedirectError, got ${String(error)}`);
    assert.equal((error as Error).name, "OpaqueRedirectError");
    assert.match((error as Error).message, /opaque redirect/);
    return true;
  });
});

test("same-origin redirect keeps Authorization and is followed", async () => {
  const seen: Recorded[] = [];
  const server = await startServer((req, res, body) => {
    if (req.url === "/health") {
      res.statusCode = 302;
      res.setHeader("Location", "/health2");
      res.end();
      return;
    }
    recordInto(seen)(req, res, body);
  });

  try {
    const client = new APIClient({ baseURL: server.url, patToken: "pat-token" });
    await client.health();
    assert.equal(seen.length, 1);
    assert.equal(seen[0].headers["authorization"], "Bearer pat-token");
  } finally {
    await server.close();
  }
});
