import assert from "node:assert/strict";
import test from "node:test";

import { useSubprotocolAuth } from "./internal/client.js";
import { MicroVM } from "./MicroVM.js";

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readonly protocols: string[];
  readonly init: { headers?: Record<string, string> } | undefined;
  binaryType = "blob";
  sent: Array<string | Uint8Array> = [];
  private readonly listeners = new Map<string, Array<(event?: unknown) => void>>();

  constructor(url: string, protocolsOrInit?: string | string[] | { protocols?: string[]; headers?: Record<string, string> }) {
    this.url = url;
    if (Array.isArray(protocolsOrInit)) {
      this.protocols = protocolsOrInit;
      this.init = undefined;
    } else if (typeof protocolsOrInit === "string") {
      this.protocols = [protocolsOrInit];
      this.init = undefined;
    } else {
      this.protocols = protocolsOrInit?.protocols ?? [];
      this.init = protocolsOrInit;
    }
    FakeWebSocket.instances.push(this);
  }

  addEventListener(name: string, listener: (event?: unknown) => void): void {
    const listeners = this.listeners.get(name) ?? [];
    listeners.push(listener);
    this.listeners.set(name, listeners);
  }

  send(data: string | Uint8Array): void {
    this.sent.push(data);
  }

  close(): void {
    // no-op for tests
  }

  emit(name: string, event?: unknown): void {
    for (const listener of this.listeners.get(name) ?? []) {
      listener(event);
    }
  }
}

function sandboxFetch(): typeof fetch {
  return async () =>
    new Response(
      JSON.stringify({
        id: "sb-ws",
        image: "ubuntu:22.04",
        status: "started",
        public_url: "https://sb-ws.example.com",
        cpu: 2,
        memory_mb: 2048,
        disk_gb: 20,
        os_user: "root",
        network_block_all: false,
        toolbox_enabled: true,
        exposed_ports: [],
        created_at: "2026-05-07T10:00:00Z",
        updated_at: "2026-05-07T10:00:00Z",
        last_active_at: "2026-05-07T10:00:00Z",
      }),
      { status: 200, headers: { "content-type": "application/json" } },
    ) as unknown as Response;
}

test("execStream authenticates with an Authorization header, not a subprotocol token", async () => {
  const originalWebSocket = globalThis.WebSocket;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    const sdk = new MicroVM({ patToken: "pat-token-secret", apiUrl: "https://api.example.com" });
    sdk.execStream("sb-ws", { command: "bash" });

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    assert.ok(
      !ws.protocols.includes("pat-token-secret"),
      `PAT exposed via Sec-WebSocket-Protocol: ${JSON.stringify(ws.protocols)}`,
    );
    assert.equal(ws.init?.headers?.Authorization, "Bearer pat-token-secret");
  } finally {
    globalThis.WebSocket = originalWebSocket;
  }
});

test("attachSession authenticates with an Authorization header, not a subprotocol token", async () => {
  const originalWebSocket = globalThis.WebSocket;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    const sdk = new MicroVM({
      patToken: "pat-token-secret",
      apiUrl: "https://api.example.com",
      fetch: sandboxFetch(),
    });
    const sandbox = await sdk.get("sb-ws");
    sandbox.attachSession("ses-1");

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    assert.ok(
      !ws.protocols.includes("pat-token-secret"),
      `PAT exposed via Sec-WebSocket-Protocol: ${JSON.stringify(ws.protocols)}`,
    );
    assert.equal(ws.init?.headers?.Authorization, "Bearer pat-token-secret");
  } finally {
    globalThis.WebSocket = originalWebSocket;
  }
});

test("authViaSubprotocol is the explicit browser fallback", async () => {
  const originalWebSocket = globalThis.WebSocket;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    const sdk = new MicroVM({ patToken: "pat-token-secret", apiUrl: "https://api.example.com" });
    sdk.execStream("sb-ws", { command: "bash", authViaSubprotocol: true } as never);

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    assert.deepEqual(ws.protocols, ["sandbox.bearer", "pat-token-secret"]);
  } finally {
    globalThis.WebSocket = originalWebSocket;
  }
});

test("useSubprotocolAuth auto-selects subprotocol outside Node and honours the explicit flag", () => {
  // Outside Node the header init object is stringified to "[object Object]"
  // and the WHATWG constructor throws, so browser must imply subprotocol mode.
  assert.equal(useSubprotocolAuth(undefined, true), true, "browser default must use the subprotocol");
  assert.equal(useSubprotocolAuth(undefined, false), false, "Node default must use the Authorization header");
  assert.equal(useSubprotocolAuth(true, false), true, "explicit opt-in wins in Node");
  assert.equal(useSubprotocolAuth(false, true), false, "explicit opt-out wins in a browser");
});

test("execStream auto-selects subprotocol auth when running outside Node", () => {
  const originalWebSocket = globalThis.WebSocket;
  const originalProcess = (globalThis as { process?: unknown }).process;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    (globalThis as { process?: unknown }).process = undefined;
    const sdk = new MicroVM({ patToken: "pat-token-secret", apiUrl: "https://api.example.com" });
    sdk.execStream("sb-ws", { command: "bash" });

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    assert.deepEqual(ws.protocols, ["sandbox.bearer", "pat-token-secret"]);
    assert.equal(ws.init, undefined, "browsers cannot accept a headers init object");
  } finally {
    (globalThis as { process?: unknown }).process = originalProcess;
    globalThis.WebSocket = originalWebSocket;
  }
});

test("execStream rejects an oversized websocket message instead of delivering it", async () => {
  const originalWebSocket = globalThis.WebSocket;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    const sdk = new MicroVM({ patToken: "pat-token-secret", apiUrl: "https://api.example.com" });
    const stdout: Uint8Array[] = [];
    const handle = sdk.execStream("sb-ws", { command: "bash", onStdout: (chunk) => stdout.push(chunk) });

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    const oversized = new Uint8Array(32 * 1024 * 1024 + 1);
    oversized[0] = 0x01;
    ws.emit("message", { data: oversized.buffer });
    ws.emit("message", { data: JSON.stringify({ type: "exit", code: 0 }) });

    await assert.rejects(handle.done, /exceeds the 33554432-byte limit/);
    assert.equal(stdout.length, 0, "oversized message must not reach onStdout");
  } finally {
    globalThis.WebSocket = originalWebSocket;
  }
});

test("attachSession rejects an oversized websocket message instead of delivering it", async () => {
  const originalWebSocket = globalThis.WebSocket;
  try {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    FakeWebSocket.instances = [];
    const sdk = new MicroVM({
      patToken: "pat-token-secret",
      apiUrl: "https://api.example.com",
      fetch: sandboxFetch(),
    });
    const sandbox = await sdk.get("sb-ws");
    const stdout: Uint8Array[] = [];
    const handle = sandbox.attachSession("ses-1", { onStdout: (chunk) => stdout.push(chunk) });

    const ws = FakeWebSocket.instances[0];
    assert.ok(ws);
    const oversized = new Uint8Array(32 * 1024 * 1024 + 1);
    oversized[0] = 0x01;
    ws.emit("message", { data: oversized.buffer });
    ws.emit("message", { data: JSON.stringify({ type: "exit", code: 0 }) });

    await assert.rejects(handle.done, /exceeds the 33554432-byte limit/);
    assert.equal(stdout.length, 0, "oversized message must not reach onStdout");
  } finally {
    globalThis.WebSocket = originalWebSocket;
  }
});
