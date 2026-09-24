// v1 path-prefix constant. The TypeScript SDK uses this to build URLs against
// the v1 surface of the sandbox daemon. When a new wire version lands, a
// sibling internal/api/vN/paths.ts will export its own PATH_PREFIX, and the
// client constructor's apiVersion option selects which one to use.
//
// Keep this in sync with pkg/api/v1/dto.go::PathPrefix on the server.
export const PATH_PREFIX = "/v1";

/**
 * Collection URL for a sandbox's custom-domain bindings. Used for both POST
 * (add) and GET (list). The DELETE variant lives at
 * {@link sandboxCustomDomainPath}.
 *
 * `id` is percent-escaped into a single path segment — it is caller-supplied
 * and must not be able to traverse out of its route.
 */
export function sandboxCustomDomainsPath(prefix: string, id: string): string {
  return `${prefix}/sandboxes/${encodeURIComponent(id)}/custom-domains`;
}

/**
 * URL for a single custom-domain binding. The hostname and sandbox id are
 * percent-encoded so IDN punycode (`xn--...`), wildcard labels, and hostile
 * ids round-trip safely through
 * `DELETE /v1/sandboxes/{id}/custom-domains/{hostname}`.
 */
export function sandboxCustomDomainPath(prefix: string, id: string, hostname: string): string {
  return `${prefix}/sandboxes/${encodeURIComponent(id)}/custom-domains/${encodeURIComponent(hostname)}`;
}

/**
 * URL for the cluster's published ingress address(es), used by
 * `microvm.dns.target()` to render the CNAME/A target a user must point
 * custom-domain DNS at.
 */
export function ingressDNSPath(prefix: string): string {
  return `${prefix}/ingress/dns`;
}

/**
 * URL for the ready-to-paste DNS records of one sandbox's custom-domain
 * bindings. Sibling of {@link sandboxCustomDomainsPath} — same `id` rules
 * apply (percent-escaped into one path segment).
 */
export function sandboxCustomDomainDNSPath(prefix: string, id: string): string {
  return `${prefix}/sandboxes/${encodeURIComponent(id)}/custom-domains/dns`;
}
