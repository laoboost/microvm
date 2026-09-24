# Changelog

The Go SDK has no version constant of its own; the module version is set by the
git tag on the repository root (this release: 0.6.0).

## 0.6.0

Security release. Migration notes:

- A redirect target that differs only by an explicit default port
  (`https://host/x` → `https://host:443/x`) is now treated as same-origin, so
  `Authorization` and `X-Registry-*` are no longer stripped from it.
- Cross-origin redirects are stripped of `Authorization` and `X-Registry-*`,
  and a 307/308 that would replay a request body cross-origin is refused.
