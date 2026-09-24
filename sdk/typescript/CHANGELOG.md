# Changelog

## 0.6.0

Security release. Review these migration notes before upgrading:

- `Sandbox.toJSON()` — and therefore `JSON.stringify(sandbox)` and
  `console.log(sandbox)` — now omits `sshPrivateKey`. The private key is
  returned only by `create()` and cannot be recovered afterwards, so read it
  from the create response or pass `sandbox.toJSON({ includeSecrets: true })`
  when you deliberately need it.
- Streaming WebSockets (`execStream`, `attachSession`) now authenticate with an
  `Authorization` header instead of a `Sec-WebSocket-Protocol` token. Daemons
  that only accept the subprotocol form must be upgraded, or pass
  `authViaSubprotocol: true` to opt back in. Outside Node.js (browsers) the SDK
  selects the subprotocol form automatically, because a browser cannot attach
  handshake headers.
- Cross-origin redirects are stripped of `Authorization` and `X-Registry-*`,
  and a 307/308 that would replay a request body cross-origin is refused. In a
  browser, `fetch` cannot expose a manual cross-origin redirect (it returns an
  opaque-redirect response), so redirects there are followed by the runtime,
  which drops credentials per the Fetch spec; an opaque redirect that still
  surfaces raises a named `OpaqueRedirectError` instead of a body-less
  `TypeError`.
- A single WebSocket message larger than 32 MiB (32 * 1024 * 1024 bytes) now
  fails the stream instead of being delivered to `onStdout`/`onStderr`.
