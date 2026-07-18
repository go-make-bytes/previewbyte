package render

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"math"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// pdfiumRenderer renders PDF documents using PDFium compiled to WebAssembly and
// run inside the wazero runtime. The PDFium binary is embedded in the go-pdfium
// module (pinned via go.sum), so there is no external blob or runtime download.
//
// The WebAssembly runtime is the inner sandbox: the parser executes in an isolated
// linear-memory module with no ambient access to the network or filesystem, so a
// parser fault is contained to one job and cannot reach the host. The container
// around the service adds the outer sandbox (no egress, read-only filesystem,
// resource caps).
type pdfiumRenderer struct {
	pool pdfium.Pool
	cfg  Config
}

// pointsPerInch is the PDF user-space unit: 72 points = 1 inch.
const pointsPerInch = 72.0

// NewPDFium builds the renderer and its pool of WebAssembly instances. The pool is
// created once at startup; instances are claimed per render and returned after.
func NewPDFium(cfg Config) (Renderer, error) {
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:  1,
		MaxIdle:  cfg.PoolSize,
		MaxTotal: cfg.PoolSize,
	})
	if err != nil {
		return nil, fmt.Errorf("init wasm pool: %w", err)
	}

	return &pdfiumRenderer{pool: pool, cfg: cfg}, nil
}

// instance claims a WebAssembly worker from the pool, bounded by the configured
// timeout. The caller must Close it to return it to the pool.
func (r *pdfiumRenderer) instance() (pdfium.Pdfium, error) {
	inst, err := r.pool.GetInstance(r.cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("acquire render instance: %w", err)
	}

	return inst, nil
}

// withDocument claims an instance, opens the bytes as a document, runs fn, then
// closes the document and returns the instance to the pool.
func (r *pdfiumRenderer) withDocument(in Input, fn func(inst pdfium.Pdfium, doc *requests.FPDF_CloseDocument, count int) error) error {
	inst, err := r.instance()
	if err != nil {
		return err
	}
	defer func() { _ = inst.Close() }()

	data := in.Bytes
	opened, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return ErrUnsupportedFormat
	}
	closeReq := &requests.FPDF_CloseDocument{Document: opened.Document}
	defer func() { _, _ = inst.FPDF_CloseDocument(closeReq) }()

	pc, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
	if err != nil {
		return ErrUnsupportedFormat
	}
	if pc.PageCount > r.cfg.MaxPages {
		return ErrTooManyPages
	}

	return fn(inst, closeReq, pc.PageCount)
}

// effectiveDPI returns the DPI to render a page at: the configured maximum, capped
// down so the rendered width never exceeds the configured maximum.
func (r *pdfiumRenderer) effectiveDPI(widthPts float64) int {
	dpi := r.cfg.MaxDPI
	if widthPts > 0 {
		widthInches := widthPts / pointsPerInch
		if maxDPI := float64(r.cfg.MaxWidth) / widthInches; maxDPI < float64(dpi) {
			dpi = int(math.Floor(maxDPI))
		}
	}
	if dpi < 1 {
		dpi = 1
	}

	return dpi
}

// pageDim returns the rendered pixel size of a page at its effective DPI.
func (r *pdfiumRenderer) pageDim(inst pdfium.Pdfium, doc *requests.FPDF_CloseDocument, index int) (PageDim, error) {
	sz, err := inst.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{
		Document: doc.Document,
		Index:    index,
	})
	if err != nil {
		return PageDim{}, ErrUnsupportedFormat
	}
	dpi := r.effectiveDPI(sz.Width)
	scale := float64(dpi) / pointsPerInch

	return PageDim{
		Width:  int(math.Round(sz.Width * scale)),
		Height: int(math.Round(sz.Height * scale)),
	}, nil
}

// Inspect opens the document and returns its page count and per-page dimensions.
func (r *pdfiumRenderer) Inspect(_ context.Context, in Input) (*Document, error) {
	out := &Document{Format: "pdf"}
	err := r.withDocument(in, func(inst pdfium.Pdfium, doc *requests.FPDF_CloseDocument, count int) error {
		out.PageCount = count
		out.Pages = make([]PageDim, 0, count)
		for i := 0; i < count; i++ {
			dim, err := r.pageDim(inst, doc, i)
			if err != nil {
				return err
			}
			out.Pages = append(out.Pages, dim)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// RenderPage rasterizes one zero-based page to an inert PNG image.
func (r *pdfiumRenderer) RenderPage(_ context.Context, in Input, page int) (*Image, error) {
	var out *Image
	err := r.withDocument(in, func(inst pdfium.Pdfium, doc *requests.FPDF_CloseDocument, count int) error {
		if page < 0 || page >= count {
			return ErrPageOutOfRange
		}

		sz, err := inst.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{Document: doc.Document, Index: page})
		if err != nil {
			return ErrUnsupportedFormat
		}
		dpi := r.effectiveDPI(sz.Width)

		rendered, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
			DPI: dpi,
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{Document: doc.Document, Index: page},
			},
		})
		if err != nil {
			return ErrUnsupportedFormat
		}
		defer rendered.Cleanup()

		img := rendered.Result.Image
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return fmt.Errorf("encode page image: %w", err)
		}

		b := img.Bounds()
		out = &Image{
			Bytes:       buf.Bytes(),
			ContentType: "image/png",
			Width:       b.Dx(),
			Height:      b.Dy(),
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Text extracts the plain-text layer, one entry per page.
func (r *pdfiumRenderer) Text(_ context.Context, in Input) ([]string, error) {
	var out []string
	err := r.withDocument(in, func(inst pdfium.Pdfium, doc *requests.FPDF_CloseDocument, count int) error {
		out = make([]string, 0, count)
		for i := 0; i < count; i++ {
			txt, err := inst.GetPageText(&requests.GetPageText{
				Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: i}},
			})
			if err != nil {
				return ErrUnsupportedFormat
			}
			out = append(out, txt.Text)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Ready reports the engine pool can hand out a live instance.
func (r *pdfiumRenderer) Ready(_ context.Context) error {
	inst, err := r.instance()
	if err != nil {
		return err
	}
	_ = inst.Close()

	return nil
}

// Close releases the engine pool.
func (r *pdfiumRenderer) Close() {
	if r.pool != nil {
		_ = r.pool.Close()
	}
}
