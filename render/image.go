package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"  // format registration for image.Decode/DecodeConfig
	_ "image/jpeg" // format registration for image.Decode/DecodeConfig
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// maxImagePixels bounds the *declared* pixel count of an image before it is fully
// decoded: a tiny file can claim an enormous canvas (the raster equivalent of a
// decompression bomb), so the header is inspected first and an oversized claim is
// rejected before any pixel buffer is allocated.
const maxImagePixels = 64_000_000 // e.g. an 8000x8000 photo

// imageRenderer renders common raster images (already decoded by the Go standard
// library — no third-party parser, no untrusted input class beyond what the rest of
// the service already trusts). It has no engine pool: a decode is cheap enough to
// run inline, bounded by the same size/dimension caps every other backend uses.
type imageRenderer struct {
	cfg Config
}

// NewImage builds the raster-image renderer.
func NewImage(cfg Config) Renderer { return &imageRenderer{cfg: cfg} }

// header reads just the declared dimensions, without allocating the full image.
func header(in Input) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(in.Bytes))
	if err != nil {
		return image.Config{}, ErrUnsupportedFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return image.Config{}, ErrUnsupportedFormat
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return image.Config{}, ErrTooLarge
	}

	return cfg, nil
}

// scaledDims returns the dimensions a page renders at: unchanged if already within
// maxWidth, else scaled down proportionally — the same clamp philosophy the PDF
// backend applies to DPI so a rendered page never exceeds the configured width.
func scaledDims(w, h, maxWidth int) (int, int) {
	if maxWidth <= 0 || w <= maxWidth {
		return w, h
	}
	scale := float64(maxWidth) / float64(w)
	sh := int(math.Round(float64(h) * scale))
	if sh < 1 {
		sh = 1
	}

	return maxWidth, sh
}

// Inspect reads the image header and reports its single rendered page.
func (r *imageRenderer) Inspect(_ context.Context, in Input) (*Document, error) {
	cfg, err := header(in)
	if err != nil {
		return nil, err
	}
	w, h := scaledDims(cfg.Width, cfg.Height, r.cfg.MaxWidth)

	return &Document{Format: "image", PageCount: 1, Pages: []PageDim{{Width: w, Height: h}}}, nil
}

// RenderPage decodes the image (page 0 only — an image has exactly one page) and
// re-encodes it to PNG: the caller never receives the original byte stream, only
// what Go's own encoder produced from the decoded pixels.
func (r *imageRenderer) RenderPage(_ context.Context, in Input, page int) (*Image, error) {
	if page != 0 {
		return nil, ErrPageOutOfRange
	}

	cfg, err := header(in)
	if err != nil {
		return nil, err
	}

	src, _, err := image.Decode(bytes.NewReader(in.Bytes))
	if err != nil {
		return nil, ErrUnsupportedFormat
	}

	out := image.Image(src)
	w, h := scaledDims(cfg.Width, cfg.Height, r.cfg.MaxWidth)
	if w != cfg.Width || h != cfg.Height {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
		out = dst
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, fmt.Errorf("encode image page: %w", err)
	}
	b := out.Bounds()

	return &Image{Bytes: buf.Bytes(), ContentType: "image/png", Width: b.Dx(), Height: b.Dy()}, nil
}

// Text reports no text layer: an image has nothing to extract. The bytes are still
// validated so a malformed input fails the same way on every endpoint.
func (r *imageRenderer) Text(_ context.Context, in Input) ([]string, error) {
	if _, err := header(in); err != nil {
		return nil, err
	}

	return nil, nil
}

// Ready always succeeds: there is no engine pool to warm up.
func (r *imageRenderer) Ready(_ context.Context) error { return nil }

// Close releases nothing — the renderer holds no resources.
func (r *imageRenderer) Close() {}
