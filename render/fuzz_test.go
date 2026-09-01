package render

import (
	"context"
	"testing"
	"time"
)

// This file is the P3 fuzz/abuse corpus (task:previewbyte-p0-remainders): every
// backend that opens untrusted bytes gets a fuzz target asserting the only
// invariant that matters for an untrusted-input parser — never panic, never hang
// past the caps already enforced in code. Seeds favor the shapes each backend
// treats specially (malicious/malformed PDF, decompression-/dimension-bomb
// images, OOXML zip markers) so the fuzzer's mutations start from a shape the
// parser actually recognizes rather than wandering pure noise.
//
// fuzzMaxBytes bounds what reaches a real parser/engine during fuzzing (PDFium,
// the stdlib image decoders) — independent of the service's own InputMaxBytes —
// so a single pathological corpus entry can't stall a fuzz run past its
// -fuzztime budget. It has no bearing on the caps enforced in production.
const fuzzMaxBytes = 1 << 18 // 256 KiB

// FuzzSniff exercises content-type detection — including the OOXML zip-marker
// disambiguation (render.go) — against arbitrary bytes. It must never panic
// regardless of how malformed or adversarial the input zip/PDF/anything is.
func FuzzSniff(f *testing.F) {
	f.Add([]byte("%PDF-1.4\n"))
	f.Add([]byte("PK\x03\x04 garbage"))
	f.Add([]byte(""))
	f.Add(buildZip(map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<w:document/>"}))
	f.Add(buildZip(map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<workbook/>"}))
	f.Add(buildZip(map[string]string{"[Content_Types].xml": "<Types/>", "ppt/presentation.xml": "<presentation/>"}))
	f.Add(buildZip(map[string]string{"hello.txt": "just a zip, not an office document"}))

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = Sniff(data)
	})
}

// FuzzPDFiumInspect is the malicious-PDF / bomb corpus target: arbitrary bytes
// through the real WASM PDFium engine, the one backend with the largest attack
// surface (a mature but complex third-party parser). One instance pool is built
// once and reused across every fuzz execution — recreating the WASM pool per
// input would make fuzzing too slow to be useful.
func FuzzPDFiumInspect(f *testing.F) {
	r, err := NewPDFium(Config{
		PoolSize: 2, MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png",
		Timeout: 5 * time.Second, MaxPages: 100, InputMaxBytes: 64 << 20,
		SupportedMime: map[string]bool{"application/pdf": true},
	})
	if err != nil {
		f.Fatalf("build pdfium renderer: %v", err)
	}
	f.Cleanup(r.Close)

	f.Add(samplePDF())
	f.Add([]byte("%PDF-1.4\n"))
	f.Add([]byte("%PDF-1.4\ntrailer\n<< /Size 0 /Root 1 0 R >>\nstartxref\n0\n%%EOF\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxBytes {
			t.Skip()
		}
		ctx := context.Background()
		in := Input{Bytes: data, Mime: "application/pdf"}
		doc, err := r.Inspect(ctx, in)
		if err != nil || doc == nil || doc.PageCount == 0 {
			return
		}
		// A document PDFium considers openable must also survive a render + text
		// pull without panicking — the two calls a real request always makes next.
		_, _ = r.RenderPage(ctx, in, 0)
		_, _ = r.Text(ctx, in)
	})
}

// FuzzImageRenderPage is the raster-image bomb corpus: arbitrary bytes through
// the stdlib decode-and-re-encode backend, including the declared-dimension guard
// (fakePNGHeader-shaped inputs) that must reject before any pixel buffer is
// allocated.
func FuzzImageRenderPage(f *testing.F) {
	r := NewImage(Config{MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png"})
	f.Cleanup(r.Close)

	f.Add(samplePNG(10, 10))
	f.Add(sampleJPEG(10, 10))
	f.Add(sampleGIF(10, 10))
	f.Add(fakePNGHeader(60000, 60000)) // declared-dimension bomb
	f.Add([]byte(""))
	f.Add([]byte("not an image at all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxBytes {
			t.Skip()
		}
		ctx := context.Background()
		in := Input{Bytes: data, Mime: "image/png"}
		_, _ = r.Inspect(ctx, in)
		_, _ = r.RenderPage(ctx, in, 0)
	})
}

// FuzzTextPaginate covers the plain-text/Markdown backend: arbitrary — including
// non-UTF-8 — bytes through normalization, word-wrap, and pagination. MaxPages is
// capped small so a pathological huge input fails fast on ErrTooManyPages rather
// than spending fuzz time rasterizing hundreds of pages.
func FuzzTextPaginate(f *testing.F) {
	r := NewText(Config{MaxWidth: 900, MaxPages: 20}).(*textRenderer)

	f.Add([]byte("hello previewbyte\nsecond line"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02\xff\xfe binary-ish"))
	f.Add([]byte("# Heading\n\n**bold** _italic_ ` code `"))
	f.Add([]byte("Šī ir ārprātīgi lāba diena"))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxBytes {
			t.Skip()
		}
		ctx := context.Background()
		in := Input{Bytes: data, Mime: "text/plain"}
		doc, err := r.Inspect(ctx, in)
		if err != nil || doc == nil {
			return
		}
		_, _ = r.RenderPage(ctx, in, 0)
		_, _ = r.Text(ctx, in)
	})
}
