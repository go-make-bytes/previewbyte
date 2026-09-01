package render

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/go-quicktest/qt"
)

func testImageRenderer(maxWidth int) Renderer {
	return NewImage(Config{MaxDPI: 150, MaxWidth: maxWidth, ImageFormat: "png"})
}

func samplePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 10, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)

	return buf.Bytes()
}

func sampleJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)

	return buf.Bytes()
}

func sampleGIF(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	_ = gif.Encode(&buf, img, nil)

	return buf.Bytes()
}

// fakePNGHeader builds a PNG signature + IHDR chunk only (no pixel data) declaring
// the given dimensions. image.DecodeConfig needs nothing past IHDR, so this proves
// the oversized-claim guard rejects before any pixel buffer is allocated — a real
// 9000x9000 image would be slow and memory-heavy to encode just to test the guard.
func fakePNGHeader(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], w)
	binary.BigEndian.PutUint32(data[4:8], h)
	data[8] = 8 // bit depth
	data[9] = 6 // color type: truecolor + alpha

	var lenB, crcB [4]byte
	binary.BigEndian.PutUint32(lenB[:], uint32(len(data)))
	buf.Write(lenB[:])
	buf.WriteString("IHDR")
	buf.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte("IHDR"))
	_, _ = crc.Write(data)
	binary.BigEndian.PutUint32(crcB[:], crc.Sum32())
	buf.Write(crcB[:])

	return buf.Bytes()
}

func TestImageInspectAndRenderPNG(t *testing.T) {
	r := testImageRenderer(2048)
	ctx := context.Background()
	in := Input{Bytes: samplePNG(20, 10), Mime: "image/png"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.Format, "image"))
	qt.Assert(t, qt.Equals(doc.PageCount, 1))
	qt.Assert(t, qt.Equals(len(doc.Pages), 1))
	qt.Assert(t, qt.Equals(doc.Pages[0].Width, 20))
	qt.Assert(t, qt.Equals(doc.Pages[0].Height, 10))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(img.ContentType, "image/png"))
	qt.Assert(t, qt.Equals(img.Width, 20))
	qt.Assert(t, qt.Equals(img.Height, 10))

	cfg, format, err := image.DecodeConfig(bytes.NewReader(img.Bytes))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(format, "png"))
	qt.Assert(t, qt.Equals(cfg.Width, 20))

	_, err = r.RenderPage(ctx, in, 1)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrPageOutOfRange)))

	texts, err := r.Text(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(len(texts), 0))
}

func TestImageRendersJPEGAndGIF(t *testing.T) {
	r := testImageRenderer(2048)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		mime string
		data []byte
	}{
		{"jpeg", "image/jpeg", sampleJPEG(16, 12)},
		{"gif", "image/gif", sampleGIF(16, 12)},
	} {
		doc, err := r.Inspect(ctx, Input{Bytes: tc.data, Mime: tc.mime})
		qt.Assert(t, qt.IsNil(err), qt.Commentf(tc.name))
		qt.Assert(t, qt.Equals(doc.PageCount, 1), qt.Commentf(tc.name))

		img, err := r.RenderPage(ctx, Input{Bytes: tc.data, Mime: tc.mime}, 0)
		qt.Assert(t, qt.IsNil(err), qt.Commentf(tc.name))
		qt.Assert(t, qt.Equals(img.ContentType, "image/png"), qt.Commentf(tc.name))
		qt.Assert(t, qt.IsTrue(len(img.Bytes) > 0), qt.Commentf(tc.name))
	}
}

// A page wider than the configured max is scaled down proportionally, the same
// clamp philosophy the PDF backend applies via DPI.
func TestImageScalesDownToMaxWidth(t *testing.T) {
	r := testImageRenderer(50)
	ctx := context.Background()
	in := Input{Bytes: samplePNG(200, 100), Mime: "image/png"}

	doc, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(doc.Pages[0].Width, 50))
	qt.Assert(t, qt.Equals(doc.Pages[0].Height, 25))

	img, err := r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(img.Width, 50))
	qt.Assert(t, qt.Equals(img.Height, 25))
}

// An oversized claimed dimension is rejected before any pixel buffer is allocated.
func TestImageTooLargeRejectedBeforeDecode(t *testing.T) {
	r := testImageRenderer(2048)
	ctx := context.Background()
	in := Input{Bytes: fakePNGHeader(9000, 9000), Mime: "image/png"}

	_, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrTooLarge)))

	_, err = r.RenderPage(ctx, in, 0)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrTooLarge)))
}

func TestImageRejectsGarbage(t *testing.T) {
	r := testImageRenderer(2048)
	ctx := context.Background()
	in := Input{Bytes: []byte("not an image at all"), Mime: "image/png"}

	_, err := r.Inspect(ctx, in)
	qt.Assert(t, qt.IsTrue(errors.Is(err, ErrUnsupportedFormat)))
}

func TestImageReadyAndClose(t *testing.T) {
	r := testImageRenderer(2048)
	qt.Assert(t, qt.IsNil(r.Ready(context.Background())))
	r.Close() // no-op; must not panic
}
