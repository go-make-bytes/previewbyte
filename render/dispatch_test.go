package render

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

func testDispatch(t *testing.T) Renderer {
	r, err := NewDispatch(Config{
		PoolSize: 1, MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png",
		Timeout: 20 * time.Second, MaxPages: 100, InputMaxBytes: 64 << 20,
	})
	qt.Assert(t, qt.IsNil(err))

	return r
}

// Each sniffed MIME routes to its own backend, and the backends never see a format
// they weren't built for.
func TestDispatchRoutesByMime(t *testing.T) {
	r := testDispatch(t)
	defer r.Close()
	ctx := context.Background()

	pdfDoc, err := r.Inspect(ctx, Input{Bytes: samplePDF(), Mime: "application/pdf"})
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(pdfDoc.Format, "pdf"))

	imgDoc, err := r.Inspect(ctx, Input{Bytes: samplePNG(10, 10), Mime: "image/png"})
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(imgDoc.Format, "image"))

	txtDoc, err := r.Inspect(ctx, Input{Bytes: []byte("hello"), Mime: "text/plain"})
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(txtDoc.Format, "text"))
}

// A MIME with no registered backend is unsupported, even if some other allowlisted
// type would otherwise render.
func TestDispatchUnknownMimeUnsupported(t *testing.T) {
	r := testDispatch(t)
	defer r.Close()

	_, err := r.Inspect(context.Background(), Input{Bytes: []byte{0x50, 0x4b, 0x03, 0x04}, Mime: "application/zip"})
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// Ready proves the PDF engine pool is live — the only backend with real resources.
func TestDispatchReady(t *testing.T) {
	r := testDispatch(t)
	defer r.Close()

	qt.Assert(t, qt.IsNil(r.Ready(context.Background())))
}

// Close releases every distinct backend without panicking, even though the three
// image MIME entries all share one backend instance.
func TestDispatchCloseIsSafe(t *testing.T) {
	r := testDispatch(t)
	r.Close()
}

// Without OfficeConverterURL set, Office MIME types are simply absent from the
// dispatcher — the same outcome as any other unconfigured type, not a failure.
func TestDispatchOfficeAbsentWhenNotConfigured(t *testing.T) {
	r := testDispatch(t) // built with no OfficeConverterURL
	defer r.Close()

	_, err := r.Inspect(context.Background(), Input{Bytes: []byte("x"), Mime: MimeDocx})
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

// With OfficeConverterURL set, all three Office MIME types route to the office
// backend.
func TestDispatchOfficeRoutedWhenConfigured(t *testing.T) {
	srv := stubConverter(t, http.StatusOK, "application/pdf", samplePDF())
	r, err := NewDispatch(Config{
		PoolSize: 1, MaxDPI: 150, MaxWidth: 2048, ImageFormat: "png",
		Timeout: 20 * time.Second, MaxPages: 100, InputMaxBytes: 64 << 20,
		OfficeConverterURL: srv.URL, OfficeConverterTimeout: 5 * time.Second,
	})
	qt.Assert(t, qt.IsNil(err))
	defer r.Close()
	ctx := context.Background()

	for _, mime := range []string{MimeDocx, MimeXlsx, MimePptx} {
		doc, err := r.Inspect(ctx, Input{Bytes: []byte("stub ignores this"), Mime: mime})
		qt.Assert(t, qt.IsNil(err), qt.Commentf(mime))
		qt.Assert(t, qt.Equals(doc.Format, "office"), qt.Commentf(mime))
	}
}
