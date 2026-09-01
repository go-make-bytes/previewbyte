# Security policy

This service exists to do one dangerous thing once, behind hard isolation: it opens untrusted
document bytes and returns an inert rendering — page images, plain text and JSON, never an
interpretable document and never active content. Its security is the isolation and the inertness;
a problem with either matters more than anything else about it.

Please report security problems privately. Do not open a public issue, pull request or discussion
for anything that could be exploited before a fix exists.

## How to report

Use **[private vulnerability reporting](https://github.com/go-make-bytes/previewbyte/security/advisories/new)**
on this repository. The report stays visible only to you and the maintainers until an advisory is
published, and it gives us one place to discuss and co-ordinate a fix with you.

Please include, as far as you can establish it:

- what the problem is, and what an attacker gains from it;
- the smallest set of steps that reproduces it, and against which version or commit — for a
  parser problem, the input that triggers it (or a way to generate one) is the report;
- the configuration it needs, if it only appears under particular settings (for example with
  the optional Office converter configured);
- whether you have told anyone else, and whether a disclosure date already binds you.

## What happens next

- We acknowledge a report within **five working days**.
- We tell you whether we can reproduce it, and what we think its severity is, as soon as we know.
- We keep you updated while a fix is prepared, and we agree a disclosure date with you. Our default
  is to publish an advisory once a fix is available, and in any case within **90 days** of the
  report — earlier if the problem is already public or being exploited.
- We credit you in the advisory unless you would rather stay anonymous.

There is no bug-bounty programme. We are grateful anyway, and we say so publicly.

## What we consider most serious

- **Escaping the sandbox:** a crafted document that makes the PDF engine (PDFium in WebAssembly)
  or any other backend reach the host process, the filesystem, the network, or another job.
- **Output that is not inert:** a rendering that carries active content, an interpretable document,
  or the original input bytes — anything a caller could execute or that reaches them un-rendered.
- **Rendering what the caller may not read:** a way to obtain a preview of a document the calling
  user could not fetch from the document source directly — the delegated-authority boundary.
- **Defeating the render caps:** input that bypasses the size, page-count, pixel-count, dimension or
  timeout limits so that one request can exhaust the service (for this service, a resource
  exhaustion through a crafted document is a real finding, not a low-priority one).
- **Leaking document content or internal error detail** in responses, logs or metrics.

Findings that need an already-compromised host or an already-authenticated administrator are in
scope but lower priority. Reports about outdated dependencies are welcome only where you can show
the vulnerable path is actually reachable — with one exception: a vulnerability in the embedded PDF
engine or the image decoders is reachable by definition, so tell us.

## Scope

This policy covers the code in this repository, including the pinned WebAssembly PDF engine it
embeds. It does not cover the optional external Office converter (report to that project), the
document source this service reads from, or deployments operated by someone other than us; ask
their operator.

## Releases

The project has not yet published a release. Security fixes land on the default branch, and once
releases exist this section will name the versions that receive them.
