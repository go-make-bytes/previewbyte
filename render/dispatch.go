package render

import (
	"context"
	"fmt"
)

// dispatchRenderer routes each call to the backend registered for the input's
// sniffed MIME type, so the API surface and the manifest contract stay identical
// regardless of which backend serves a given format: adding a format is a new
// backend plus one allowlist entry, never a change to the routes that call it.
type dispatchRenderer struct {
	byMime map[string]Renderer
	pdf    Renderer // the one backend with a real resource pool; readyz proves it live
}

// NewDispatch builds the PDF, image, text, and (when configured) Office backends
// and wires them behind one Renderer, keyed by sniffed MIME. The Office backend is
// built and registered only when cfg.OfficeEnabled(): an unconfigured converter
// means those MIME types are simply absent from the map, not present-but-failing.
func NewDispatch(cfg Config) (Renderer, error) {
	pdf, err := NewPDFium(cfg)
	if err != nil {
		return nil, fmt.Errorf("build pdf renderer: %w", err)
	}
	img := NewImage(cfg)
	txt := NewText(cfg)

	byMime := map[string]Renderer{
		"application/pdf": pdf,
		"image/png":       img,
		"image/jpeg":      img,
		"image/gif":       img,
		"text/plain":      txt,
	}
	if cfg.OfficeEnabled() {
		office := NewOffice(cfg, pdf)
		for _, m := range OfficeMimeTypes() {
			byMime[m] = office
		}
	}

	return &dispatchRenderer{pdf: pdf, byMime: byMime}, nil
}

func (d *dispatchRenderer) backend(mime string) (Renderer, error) {
	r, ok := d.byMime[mime]
	if !ok {
		return nil, ErrUnsupportedFormat
	}

	return r, nil
}

// Inspect delegates to the backend for the input's sniffed MIME type.
func (d *dispatchRenderer) Inspect(ctx context.Context, in Input) (*Document, error) {
	r, err := d.backend(in.Mime)
	if err != nil {
		return nil, err
	}

	return r.Inspect(ctx, in)
}

// RenderPage delegates to the backend for the input's sniffed MIME type.
func (d *dispatchRenderer) RenderPage(ctx context.Context, in Input, page int) (*Image, error) {
	r, err := d.backend(in.Mime)
	if err != nil {
		return nil, err
	}

	return r.RenderPage(ctx, in, page)
}

// Text delegates to the backend for the input's sniffed MIME type.
func (d *dispatchRenderer) Text(ctx context.Context, in Input) ([]string, error) {
	r, err := d.backend(in.Mime)
	if err != nil {
		return nil, err
	}

	return r.Text(ctx, in)
}

// Ready checks the PDF backend's engine pool — the only backend with real resources
// to warm up; the image/text backends have none and are always ready.
func (d *dispatchRenderer) Ready(ctx context.Context) error {
	return d.pdf.Ready(ctx)
}

// Close releases every distinct backend exactly once (image/*'s three entries all
// share one backend instance).
func (d *dispatchRenderer) Close() {
	seen := make(map[Renderer]bool, len(d.byMime))
	for _, r := range d.byMime {
		if seen[r] {
			continue
		}
		seen[r] = true
		r.Close()
	}
}
