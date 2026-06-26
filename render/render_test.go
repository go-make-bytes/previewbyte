package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

// samplePDF builds a valid single-page PDF (200x200 pt) containing the text
// "Hello previewbyte", computing the cross-reference offsets so the document
// parses without relying on the engine's repair path.
func samplePDF() []byte {
	var buf bytes.Buffer
	offsets := make([]int, 6)

	buf.WriteString("%PDF-1.4\n")
	obj := func(n int, body string) {
		offsets[n] = buf.Len()
		buf.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", n, body))
	}

	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	stream := "BT /F1 24 Tf 20 100 Td (Hello previewbyte) Tj ET"
	obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xref := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref))

	return buf.Bytes()
}

func testRenderer(t *testing.T) Renderer {
	r, err := NewPDFium(Config{
		PoolSize:      1,
		MaxDPI:        150,
		MaxWidth:      2048,
		ImageFormat:   "png",
		Timeout:       20 * time.Second,
		MaxPages:      100,
		InputMaxBytes: 64 << 20,
		SupportedMime: map[string]bool{"application/pdf": true},
	})
	qt.Assert(t, qt.IsNil(err))

	return r
}

// TestPDFiumRendersSamplePDF exercises the real WebAssembly PDFium engine
// end-to-end: a real PDF inspects to one page, renders to a decodable PNG, and
// yields its text layer.
func TestPDFiumRendersSamplePDF(t *testing.T) {
	r := testRenderer(t)
	defer r.Close()

	ctx := context.Background()
	in := Input{Bytes: samplePDF(), Mime: "application/pdf"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.Format, "pdf"))
	qt.Assert(t, qt.Equals(doc.PageCount, 1))
	qt.Assert(t, qt.Equals(len(doc.Pages), 1))
	qt.Assert(t, qt.IsTrue(doc.Pages[0].Width > 0 && doc.Pages[0].Height > 0))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(img.ContentType, "image/png"))
	qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0))
	qt.Assert(t, qt.IsTrue(img.Width > 0 && img.Height > 0))

	// The bytes are a real, decodable PNG.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img.Bytes))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(format, "png"))
	qt.Assert(t, qt.Equals(cfg.Width, img.Width))

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(len(texts), 1))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "previewbyte")))
}

// TestPDFiumPageOutOfRange rejects a page index past the end.
func TestPDFiumPageOutOfRange(t *testing.T) {
	r := testRenderer(t)
	defer r.Close()

	_, err := r.RenderPage(context.Background(), Input{Bytes: samplePDF(), Mime: "application/pdf"}, 7)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrPageOutOfRange)))
}

// TestPDFiumRejectsNonPDF returns the unsupported-format outcome for input the
// engine cannot open as a document.
func TestPDFiumRejectsNonPDF(t *testing.T) {
	r := testRenderer(t)
	defer r.Close()

	_, err := r.Inspect(context.Background(), Input{Bytes: []byte("this is not a pdf at all"), Mime: "text/plain"})
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// TestPDFiumTooManyPages enforces the page-count cap.
func TestPDFiumTooManyPages(t *testing.T) {
	r, err := NewPDFium(Config{
		PoolSize: 1, MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png",
		Timeout: 20 * time.Second, MaxPages: 0, InputMaxBytes: 64 << 20,
		SupportedMime: map[string]bool{"application/pdf": true},
	})
	qt.Assert(t, qt.IsNil(err))
	defer r.Close()

	_, err = r.Inspect(context.Background(), Input{Bytes: samplePDF(), Mime: "application/pdf"})
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrTooManyPages)))
}
