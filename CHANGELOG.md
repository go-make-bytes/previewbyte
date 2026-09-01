# Changelog

Notable changes to this service, newest first, per release. This file is written for whoever
runs the service or integrates against it.

## v0.1.0 — 2026-09-01

Initial code.

The previewbyte document preview service as first released: untrusted document bytes rendered
into inert raster pages with an optional text layer, behind a WASM-sandboxed parser. Renders
PDF, common images (PNG/JPEG/GIF), plain text and Markdown, and — when an external converter
is configured — Office documents (.docx/.xlsx/.pptx). Holds no durable data and no signing
keys. MIT.
