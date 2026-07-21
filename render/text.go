package render

import (
	"bytes"
	"context"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Plain text (and Markdown — the sniffer has no signature of its own for Markdown,
// so a .md upload sniffs identically to a .txt one) is paginated and rasterized into
// the same inert-page-image shape every other backend produces, rather than
// inventing a text-native display: it keeps the manifest contract, the BFF, and the
// SPA completely unchanged, and it keeps Markdown source un-interpreted — there is
// no Markdown-to-HTML pass. This service's safety model rests on never turning
// untrusted bytes into interpretable markup, so Markdown gets exactly the same
// treatment as any other document: shown verbatim, never rendered.
//
// The font is Go's own monospace typeface, vendored as Go source inside
// golang.org/x/image and pinned by go.sum — not a parse of untrusted input, the same
// trust class as the PDFium engine binary. It covers Latin Extended-A, so Latvian
// diacritics render correctly.
const (
	textFontSizePx  = 13.0
	textFontDPI     = 72.0
	textDefaultPage = 900 // px; clamped to Config.MaxWidth when that is smaller
	textMargin      = 32  // px
	textLinesPerPg  = 56
	textTabWidth    = 4 // spaces per tab stop
)

// textFace is parsed once at package init — a fixed, trusted asset, not per-request
// work.
var textFace = mustGoMonoFace()

func mustGoMonoFace() font.Face {
	f, err := opentype.Parse(gomono.TTF)
	if err != nil {
		panic("render: parse embedded gomono font: " + err.Error())
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size: textFontSizePx,
		DPI:  textFontDPI,
	})
	if err != nil {
		panic("render: build gomono face: " + err.Error())
	}

	return face
}

// textRenderer rasterizes plain-text/Markdown source into paginated inert page
// images plus a matching text layer. It holds no engine pool — layout is bounded,
// deterministic work, not a parse of a complex untrusted format.
type textRenderer struct {
	cfg        Config
	pageWidth  int
	charWidth  int
	lineHeight int
	ascent     int
}

// NewText builds the plain-text/Markdown renderer.
func NewText(cfg Config) Renderer {
	pageWidth := textDefaultPage
	if cfg.MaxWidth > 0 && cfg.MaxWidth < pageWidth {
		pageWidth = cfg.MaxWidth
	}

	adv, ok := textFace.GlyphAdvance('M')
	charWidth := textFontSizePx
	if ok {
		charWidth = float64(adv.Round())
	}
	m := textFace.Metrics()

	return &textRenderer{
		cfg:        cfg,
		pageWidth:  pageWidth,
		charWidth:  maxInt(int(charWidth), 1),
		lineHeight: maxInt(m.Height.Round(), 1),
		ascent:     maxInt(m.Ascent.Round(), 1),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

// charsPerLine is the wrap width in runes: the usable page width (page minus both
// margins) divided by the monospace advance.
func (r *textRenderer) charsPerLine() int {
	return maxInt((r.pageWidth-2*textMargin)/r.charWidth, 1)
}

// paginate normalizes, word-wraps, and pages the source text. It bails out as soon
// as the page cap is exceeded rather than wrapping an entire oversized file, so a
// pathological many-line input inside the byte cap still fails fast.
func (r *textRenderer) paginate(content string) ([]string, error) {
	lines := wrapAll(normalizeText(content), r.charsPerLine())

	perPage := textLinesPerPg
	maxLines := r.cfg.MaxPages * perPage
	if len(lines) > maxLines {
		return nil, ErrTooManyPages
	}
	if len(lines) == 0 {
		lines = []string{""}
	}

	pages := make([]string, 0, (len(lines)+perPage-1)/perPage)
	for i := 0; i < len(lines); i += perPage {
		end := i + perPage
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, strings.Join(lines[i:end], "\n"))
	}

	return pages, nil
}

// normalizeText canonicalizes line endings, expands tabs to a fixed stop width, and
// drops non-printable control bytes other than newline — display hygiene, not a
// security boundary (nothing here is ever interpreted).
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", strings.Repeat(" ", textTabWidth))

	return strings.Map(func(r rune) rune {
		if r == '\n' || r >= 0x20 {
			return r
		}

		return -1
	}, s)
}

// wrapAll splits normalized text into source lines and word-wraps each to width
// runes, preserving blank lines.
func wrapAll(s string, width int) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}

	return out
}

// wrapLine greedily word-wraps one line to width runes; a single word longer than
// width is hard-broken so one pathological run can't produce an unbounded line.
func wrapLine(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for _, w := range words {
		wr := []rune(w)
		for len(wr) > width {
			flush()
			out = append(out, string(wr[:width]))
			wr = wr[width:]
		}
		switch {
		case len(cur) == 0:
			cur = wr
		case len(cur)+1+len(wr) > width:
			flush()
			cur = wr
		default:
			cur = append(cur, ' ')
			cur = append(cur, wr...)
		}
	}
	flush()

	return out
}

// pageHeight sizes the canvas to the lines actually on that page, so a short final
// page isn't padded with blank space.
func (r *textRenderer) pageHeight(lines int) int {
	return 2*textMargin + maxInt(lines, 1)*r.lineHeight
}

// draw rasterizes one page of already-wrapped text (joined by "\n") onto a white
// canvas.
func (r *textRenderer) draw(pageText string) *Image {
	lines := strings.Split(pageText, "\n")
	h := r.pageHeight(len(lines))
	img := image.NewRGBA(image.Rect(0, 0, r.pageWidth, h))
	stddraw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)

	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: textFace}
	y := textMargin + r.ascent
	for _, line := range lines {
		d.Dot = fixed.Point26_6{X: fixed.I(textMargin), Y: fixed.I(y)}
		d.DrawString(line)
		y += r.lineHeight
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img) // RGBA -> PNG never fails

	return &Image{Bytes: buf.Bytes(), ContentType: "image/png", Width: r.pageWidth, Height: h}
}

// Inspect paginates the text and reports one rendered page per chunk.
func (r *textRenderer) Inspect(_ context.Context, in Input) (*Document, error) {
	pages, err := r.paginate(string(in.Bytes))
	if err != nil {
		return nil, err
	}

	dims := make([]PageDim, len(pages))
	for i, p := range pages {
		dims[i] = PageDim{Width: r.pageWidth, Height: r.pageHeight(len(strings.Split(p, "\n")))}
	}

	return &Document{Format: "text", PageCount: len(pages), Pages: dims}, nil
}

// RenderPage rasterizes one page of wrapped text to an inert image.
func (r *textRenderer) RenderPage(_ context.Context, in Input, page int) (*Image, error) {
	pages, err := r.paginate(string(in.Bytes))
	if err != nil {
		return nil, err
	}
	if page < 0 || page >= len(pages) {
		return nil, ErrPageOutOfRange
	}

	return r.draw(pages[page]), nil
}

// Text returns the same wrapped per-page lines used to rasterize each page, so the
// screen-reader layer always matches what's visually shown.
func (r *textRenderer) Text(_ context.Context, in Input) ([]string, error) {
	return r.paginate(string(in.Bytes))
}

// Ready always succeeds: the font is parsed once at init and layout has no engine
// pool to warm up.
func (r *textRenderer) Ready(_ context.Context) error { return nil }

// Close releases nothing — the renderer holds no resources.
func (r *textRenderer) Close() {}
