// Package render turns untrusted document bytes into a safe, review-only preview:
// inert per-page raster images plus an optional plain-text layer. It is the part
// of the service that opens untrusted input, so the parse runs inside a
// memory-isolated WebAssembly runtime and every render is bounded by hard caps
// (input size, page count, output dimensions, time).
//
// The output is always inert — images, text, and structured metadata — never an
// interpretable document and never any active content.
package render

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Typed render outcomes the API maps onto safe results — never a raw engine or
// stack message reaches the caller.
var (
	// ErrUnsupportedFormat means the sniffed content type is not on the allowlist,
	// or the engine cannot open the bytes as a supported document.
	ErrUnsupportedFormat = errors.New("unsupported_format")
	// ErrTooLarge means the input exceeds the configured byte cap.
	ErrTooLarge = errors.New("too_large")
	// ErrTooManyPages means the document has more pages than the configured cap.
	ErrTooManyPages = errors.New("too_many_pages")
	// ErrPageOutOfRange means a requested page index does not exist.
	ErrPageOutOfRange = errors.New("page_out_of_range")
)

// Config bounds every render. Zero values are rejected by the service config.
type Config struct {
	PoolSize      int
	MaxDPI        int
	MaxWidth      int
	ImageFormat   string // "png" (P0); "webp" is a later addition.
	Timeout       time.Duration
	MaxPages      int
	InputMaxBytes int64
	SupportedMime map[string]bool // allowlist, matched against the SNIFFED type
}

// Input is the document to render: the raw bytes and the source-declared media
// type. The declared type is advisory only — the renderer sniffs the bytes and
// trusts the sniff, never the declaration.
type Input struct {
	Bytes []byte
	Mime  string
}

// Document is the inspected shape of a document: its normalized format and the
// per-page pixel dimensions at the dimensions the pages will render to.
type Document struct {
	Format    string
	PageCount int
	Pages     []PageDim
}

// PageDim is a page's rendered pixel size.
type PageDim struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Image is one rendered, inert page image.
type Image struct {
	Bytes       []byte
	ContentType string
	Width       int
	Height      int
}

// Renderer renders document bytes into an inert preview. Implementations run the
// parse in an isolated runtime and are safe for concurrent use.
type Renderer interface {
	// Inspect opens the document and returns its page count and per-page rendered
	// dimensions, without rasterizing. Returns ErrUnsupportedFormat for input it
	// cannot open and ErrTooManyPages above the cap.
	Inspect(ctx context.Context, in Input) (*Document, error)
	// RenderPage rasterizes one zero-based page to an inert image.
	RenderPage(ctx context.Context, in Input, page int) (*Image, error)
	// Text extracts the plain-text layer, one entry per page.
	Text(ctx context.Context, in Input) ([]string, error)
	// Ready reports the engine is live and can render (readiness probe).
	Ready(ctx context.Context) error
	// Close releases engine resources.
	Close()
}

// CheckSize rejects input above the byte cap before any parse.
func (c Config) CheckSize(n int64) error {
	if n > c.InputMaxBytes {
		return ErrTooLarge
	}

	return nil
}

// Sniff detects the actual content type of the bytes (the declared MIME is never
// trusted). It returns a bare media type without parameters.
func Sniff(b []byte) string {
	ct := http.DetectContentType(b)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}

	return strings.ToLower(strings.TrimSpace(ct))
}

// Supported reports whether a sniffed media type is on the allowlist.
func (c Config) Supported(sniffed string) bool {
	return c.SupportedMime[sniffed]
}
