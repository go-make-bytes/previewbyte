package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// officeRenderer previews Office documents (Word/Excel/PowerPoint) by converting
// them to PDF through a Gotenberg instance, then delegating every render call to
// the existing PDFium renderer — no page/text logic is duplicated here. Gotenberg
// is a real office suite plus a browser engine, a materially larger attack surface
// than PDFium-in-WASM or the stdlib decoders, so it is never embedded in this
// process: it runs in its own container, reached over the network, and the
// converted bytes are re-sniffed (never trusted by declared type) before they are
// handed to the sandboxed PDF path.
type officeRenderer struct {
	client   *http.Client
	url      string
	maxBytes int64
	pdf      Renderer
}

// NewOffice builds the Office-document backend. pdf is the renderer every
// converted document is delegated to (the PDFium backend in production).
func NewOffice(cfg Config, pdf Renderer) Renderer {
	return &officeRenderer{
		client:   &http.Client{Timeout: cfg.OfficeConverterTimeout},
		url:      cfg.OfficeConverterURL,
		maxBytes: cfg.InputMaxBytes,
		pdf:      pdf,
	}
}

// officeExt returns the filename extension that selects Gotenberg's LibreOffice
// import filter for a sniffed Office MIME type — previewbyte has bytes and a
// sniffed kind, never an original filename, and doesn't need one: the extension
// only has to match the kind already established by the zip-marker sniff (render.go).
func officeExt(mime string) (string, bool) {
	switch mime {
	case MimeDocx:
		return ".docx", true
	case MimeXlsx:
		return ".xlsx", true
	case MimePptx:
		return ".pptx", true
	default:
		return "", false
	}
}

// convert posts the document to Gotenberg and returns the converted PDF bytes. A
// transport-level failure (the converter is unreachable or the round trip times
// out) is ErrUpstreamUnavailable — distinct from the input being unrenderable. A
// response that isn't a usable PDF (non-2xx, oversized, or not actually a PDF once
// re-sniffed) falls back to ErrUnsupportedFormat, the same safe bucket every other
// backend uses for an open/parse failure: Gotenberg does not cleanly separate "bad
// input" from "engine trouble" once it has answered at all.
func (r *officeRenderer) convert(ctx context.Context, in Input) ([]byte, error) {
	ext, ok := officeExt(in.Mime)
	if !ok {
		return nil, ErrUnsupportedFormat
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("files", "document"+ext)
	if err != nil {
		return nil, fmt.Errorf("build conversion request: %w", err)
	}
	if _, err := part.Write(in.Bytes); err != nil {
		return nil, fmt.Errorf("build conversion request: %w", err)
	}
	// Without this, a spreadsheet converts onto its default full paper size
	// regardless of how little of it the data actually uses — a small used range
	// renders as a speck in the corner of an otherwise blank page. This sizes each
	// sheet's page to its own content instead. It's a no-op for Word/PowerPoint
	// input (verified against a real conversion before relying on it).
	if err := w.WriteField("singlePageSheets", "true"); err != nil {
		return nil, fmt.Errorf("build conversion request: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("build conversion request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url+"/forms/libreoffice/convert", &body)
	if err != nil {
		return nil, fmt.Errorf("build conversion request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, errors.Join(ErrUpstreamUnavailable, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnsupportedFormat
	}

	limited := io.LimitReader(resp.Body, r.maxBytes+1)
	out, err := io.ReadAll(limited)
	if err != nil {
		return nil, ErrUpstreamUnavailable
	}
	if int64(len(out)) > r.maxBytes {
		return nil, ErrTooLarge
	}
	if Sniff(out) != "application/pdf" {
		return nil, ErrUnsupportedFormat
	}

	return out, nil
}

// Inspect converts, then reports the converted document's page manifest — the
// Format is overridden to "office" so the manifest reflects the source kind, not
// the intermediate PDF the engine happens to use.
func (r *officeRenderer) Inspect(ctx context.Context, in Input) (*Document, error) {
	pdfBytes, err := r.convert(ctx, in)
	if err != nil {
		return nil, err
	}
	doc, err := r.pdf.Inspect(ctx, Input{Bytes: pdfBytes, Mime: "application/pdf"})
	if err != nil {
		return nil, err
	}
	doc.Format = "office"

	return doc, nil
}

// RenderPage converts, then delegates the rasterization to the PDF backend.
func (r *officeRenderer) RenderPage(ctx context.Context, in Input, page int) (*Image, error) {
	pdfBytes, err := r.convert(ctx, in)
	if err != nil {
		return nil, err
	}

	return r.pdf.RenderPage(ctx, Input{Bytes: pdfBytes, Mime: "application/pdf"}, page)
}

// Text converts, then delegates text extraction to the PDF backend.
func (r *officeRenderer) Text(ctx context.Context, in Input) ([]string, error) {
	pdfBytes, err := r.convert(ctx, in)
	if err != nil {
		return nil, err
	}

	return r.pdf.Text(ctx, Input{Bytes: pdfBytes, Mime: "application/pdf"})
}

// Ready always succeeds: there is no local pool to warm up. A down converter
// degrades individual Office-preview requests (see convert), not this service's
// overall readiness — the same posture already used for an unconfigured document
// source, which PDF/image/text preview have nothing to do with.
func (r *officeRenderer) Ready(_ context.Context) error { return nil }

// Close releases nothing — the HTTP client needs no explicit shutdown.
func (r *officeRenderer) Close() {}
