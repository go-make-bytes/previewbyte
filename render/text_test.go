package render

import (
	"bytes"
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

func testTextRenderer(maxPages int) *textRenderer {
	return NewText(Config{MaxWidth: 900, MaxPages: maxPages}).(*textRenderer)
}

func TestTextInspectAndRenderShortFile(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()
	in := Input{Bytes: []byte("hello previewbyte\nsecond line"), Mime: "text/plain"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.Format, "text"))
	qt.Assert(t, qt.Equals(doc.PageCount, 1))
	qt.Assert(t, qt.Equals(len(doc.Pages), 1))
	qt.Assert(t, qt.IsTrue(doc.Pages[0].Width > 0 && doc.Pages[0].Height > 0))

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(len(texts), 1))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "hello previewbyte")))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "second line")))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(img.ContentType, "image/png"))
	qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0))

	_, format, err := image.DecodeConfig(bytes.NewReader(img.Bytes))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(format, "png"))

	_, err = r.RenderPage(ctx, in, 1)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrPageOutOfRange)))
}

// A Markdown upload sniffs identically to plain text (there is no distinct
// signature), so it takes the exact same path: rendered as its raw source, never
// interpreted as Markdown syntax.
func TestTextRendersMarkdownSourceVerbatim(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()
	in := Input{Bytes: []byte("# Heading\n\n**bold** and _italic_ stay literal"), Mime: "text/plain"}

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "# Heading")))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "**bold**")))
}

// Latvian diacritics must survive normalization and rasterize without error — Go's
// monospace typeface covers Latin Extended-A.
func TestTextHandlesLatvianDiacritics(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()
	in := Input{Bytes: []byte("Šī ir ārprātīgi lāba diena, paraksti šeit"), Mime: "text/plain"}

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(texts[0], "paraksti šeit")))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0))
}

// A file with more lines than fit on one page paginates, and each page's Text()
// entry matches what RenderPage draws for that page.
func TestTextPaginatesMultiplePages(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()

	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line number of content")
	}
	in := Input{Bytes: []byte(strings.Join(lines, "\n")), Mime: "text/plain"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(doc.PageCount > 1))

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(len(texts), doc.PageCount))

	_, err = r.RenderPage(ctx, in, doc.PageCount-1)
	qt.Assert(t, qt.IsNil(err))

	_, err = r.RenderPage(ctx, in, doc.PageCount)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrPageOutOfRange)))
}

// The page-count cap is shared with the PDF backend's meaning: too many pages is
// too many pages, regardless of format.
func TestTextTooManyPages(t *testing.T) {
	r := testTextRenderer(1)
	ctx := context.Background()

	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "one line of plain text content")
	}
	in := Input{Bytes: []byte(strings.Join(lines, "\n")), Mime: "text/plain"}

	_, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrTooManyPages)))
}

// A single run with no whitespace longer than the wrap width is hard-broken rather
// than producing one unbounded line.
func TestTextWrapsLongUnbrokenRun(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()
	in := Input{Bytes: []byte(strings.Repeat("a", 5000)), Mime: "text/plain"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(doc.PageCount >= 1))

	_, err = r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
}

func TestTextEmptyContent(t *testing.T) {
	r := testTextRenderer(100)
	ctx := context.Background()
	in := Input{Bytes: []byte(""), Mime: "text/plain"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.PageCount, 1))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0))
}

func TestTextReadyAndClose(t *testing.T) {
	r := testTextRenderer(100)
	qt.Assert(t, qt.IsNil(r.Ready(context.Background())))
	r.Close() // no-op; must not panic
}
