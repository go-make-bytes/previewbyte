# previewbyte

A reusable, security-hardened **document preview / render** service. Given a
document — by reference to a document source, or by raw bytes — it returns a
**safe, review-only rendering**: inert per-page images plus an optional plain-text
layer, with **no active content** and **no signature placement**, so a person can
read a document before they sign, send, or share it.

It is the one place that **opens untrusted document bytes**, so its whole reason to
exist is to do that dangerous, complex job **once, behind hard isolation**, instead
of every product rendering inline.

## What it does

- **Renders to an inert preview** — per-page raster images (PNG) and an optional
  extracted text layer for accessibility and search. The output is images, text,
  and JSON only; no interpretable document and no active content ever reach the
  caller.
- **Reads the source on behalf of the user.** A user-owned document is fetched with
  a delegated token, so the source's owner-filtering holds end-to-end and the
  service can never see a document the caller could not.
- **Runs the parser in a sandbox.** The PDF engine is PDFium compiled to
  WebAssembly and run inside a pure-Go runtime — an isolated, memory-bounded module
  with no ambient access to the network or filesystem. A parser fault is contained
  to one job. The container around it adds no egress, a read-only filesystem, and
  resource caps.
- **Bounds every render** — input size cap, content-type sniff + allowlist (the
  declared type is never trusted), page-count and output-dimension caps, and a
  render timeout.

It holds **no durable data** (the document source owns the bytes) and **no signing
crypto**. Audit of a user-facing preview view is emitted by the caller that knows
the actor is a person (the portal backend), not by this background renderer.

## API

Base path `/api/v1`. Inbound calls are authenticated and require the `preview:read`
scope; user-owned renders are performed on behalf of the user.

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/previews/{documentId}` | the preview manifest (page list + dimensions + inert references), or a typed `{renderable:false, reason}` result for a non-previewable type |
| `GET /api/v1/previews/{documentId}/pages/{n}` | one rendered, inert page image |
| `GET /api/v1/previews/{documentId}/text` | the optional plain-text layer (404 when none) |
| `GET /healthz` · `GET /readyz` | liveness · readiness (the render engine is live) |

A non-previewable type returns `{renderable:false, reason:"unsupported_format", mime}`
so the caller can offer "download to review" — never an error to guess at.

## Configuration

| Env | Purpose |
|---|---|
| `AUTH_ISSUER_URL` / `SERVICE_AUDIENCE` (`svc:preview`) | inbound token validation + this service's audience |
| `SERVICE_CLIENT_ID` (`svc:preview`) / `SERVICE_CLIENT_SECRET[_FILE]` | the service identity used for the on-behalf token exchange |
| `DOCUMENT_BASE_URL` / `DOCUMENT_AUDIENCE` (`svc:document`) | the first document source |
| `RENDER_MODE` (`raster`) · `RENDER_MAX_DPI` · `RENDER_MAX_WIDTH` · `RENDER_IMAGE_FORMAT` (`png`) | output strategy + dimension caps |
| `RENDER_TIMEOUT` · `RENDER_MAX_PAGES` · `RENDER_POOL_SIZE` · `INPUT_MAX_BYTES` | render bounds + engine pool size |
| `SUPPORTED_MIME` | the content-type allowlist (sniffed, not declared) |

No database, no object storage, and no signing keys: the service talks to the
document source over HTTP on behalf of the user and renders in memory.

## Scope (this build)

Renders **PDF** to PNG page images plus a text layer, reading from the document
source on behalf of the user. WebP output, an ephemeral encrypted cache, Office
formats (via a converter), a sanitized-PDF mode, and a direct-bytes mode are
later additions; this build returns a typed not-renderable result for anything
outside the allowlist.
