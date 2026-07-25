# Discovery Source Peek And Fetcher DNS Design

## Status

- Approved: 2026-07-25
- Implementation: pending
- Scope: left-panel Discovery layout and Source Fetcher DNS resolution only

## Problem

Expanded Source Discovery currently consumes the complete left panel, so users cannot see their existing Sources while reviewing candidates.

In local environments that use Mihomo/Clash Fake IP DNS, public domains resolve to reserved addresses such as `198.18.0.0/15` or private IPv6 addresses. The restricted Source Fetcher correctly rejects these addresses as `unsafe_destination`, causing every selected Candidate import to fail even though Brave returned valid public URLs.

## Decisions

### Left-panel layout

Discovery remains in the left Source panel and retains the current approximately 560 px desktop width. While Discovery is open:

- the search form, summary, candidate list, and Import Selected action occupy the upper flexible region;
- a distinct existing-Sources region remains visible at the bottom;
- the existing-Sources region is approximately 180 px high on desktop and 140 px high in compact layouts;
- both candidate results and existing Sources scroll independently;
- existing Source selection, state, edit, and delete controls remain available;
- when the Notebook has no existing Sources, the lower region shows the existing empty state rather than disappearing.

Closing Discovery restores the existing full-height Source list.

### Fetcher DNS

The restricted Source Fetcher gains an optional RFC 8484 DNS-over-HTTPS resolver. It is configured only through Fetcher-specific environment variables:

- `NANO_FETCHER_DOH_URL`
- `NANO_FETCHER_DOH_BOOTSTRAP_ADDR`

If neither value is configured, the Fetcher continues to use `net.DefaultResolver`, preserving production behavior. If one is configured without the other, startup fails configuration validation.

The local `scripts/start` command supplies development defaults for Cloudflare's RFC 8484 endpoint and a fixed public bootstrap address. This avoids system Fake IP DNS without making DoH a global application dependency.

The DoH client:

- uses RFC 8484 `application/dns-message` payloads;
- connects to the configured bootstrap address while validating TLS against the DoH URL hostname;
- disables environment proxies and redirects;
- applies bounded request time and response size;
- resolves A and AAAA records and returns only syntactically valid addresses;
- fails closed when the resolver is unavailable or returns an invalid response.

Every resolved destination still passes the existing `IsPublicAddress` policy before any connection is made. Reserved Fake IP ranges and real private, loopback, link-local, multicast, and documentation ranges remain blocked. The implementation must not add an exception for `198.18.0.0/15` or `fdfe::/16`.

## Data Flow

```text
Candidate URL
  -> URL admission
  -> restricted Source Fetcher
  -> configured DoH resolver (local) or system resolver (default)
  -> public-address validation
  -> dial the validated resolved IP
  -> immutable snapshot
  -> existing Source processing pipeline
```

Brave search, Chat routing, Research delegation, object storage, Source processing, RAG, and imported-Source persistence are unchanged.

## Error Handling

- Invalid DoH configuration prevents Fetcher startup.
- Resolver timeout, invalid DNS response, or no usable records fails the Candidate import; it never falls back to accepting the original Fake IP.
- A DNS answer containing any blocked address remains `unsafe_destination` under the current mixed-answer policy.
- Candidate imports remain independently retryable after DNS configuration or transient resolver recovery.
- The UI continues to show bounded import failure text without exposing internal network details.

## Testing

### Layout

- A component test first proves that existing Sources remain rendered while Discovery results are open.
- CSS/browser acceptance proves a visible lower Source region at desktop and compact widths, independent scrolling, and no horizontal overflow.
- Existing Source selection and Candidate selection remain independent.

### DNS and import

- Resolver unit tests cover valid A/AAAA responses, TLS bootstrap routing, timeout, malformed payloads, oversized responses, and non-success responses.
- Fetcher tests prove that a public domain resolved through DoH imports successfully while Fake IP, private IP, and mixed public/private answers remain rejected.
- Configuration tests cover disabled/default behavior, complete DoH configuration, and partial invalid configuration.
- Existing Fetcher SSRF, redirect, size, and content-type suites remain green.
- A live local acceptance run retries a previously failed public Candidate and confirms Source creation without changing Clash configuration.

## Acceptance Criteria

1. Expanded Discovery always leaves a visible, usable portion of existing Sources in the left panel.
2. Desktop and compact layouts do not overflow horizontally.
3. Local Candidate imports work under the observed Clash Fake IP environment.
4. `198.18.0.0/15`, private IPv4, and private/reserved IPv6 remain blocked by the Fetcher.
5. Other application network behavior is unchanged because the resolver is scoped to the Source Fetcher.
6. Production behavior remains unchanged unless both Fetcher DoH variables are configured.
