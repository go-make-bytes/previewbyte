package routes

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"azugo.io/azugo"
	corehttp "azugo.io/core/http"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	pkerrors "github.com/gmb-lib/go-platform-kit/errors"

	"github.com/go-make-bytes/previewbyte/clients"
	"github.com/go-make-bytes/previewbyte/render"
)

// previewBasePath is the public path the manifest's inert references point back to.
const previewBasePath = "/api/v1/previews/"

// previewHint is how long the manifest advertises a preview as fresh. P0 has no
// cache, so it is only a presentation hint; the real value follows the cache TTL.
const previewHint = 5 * time.Minute

func pageRef(id string, i int) string { return fmt.Sprintf("%s%s/pages/%d", previewBasePath, id, i) }
func textRef(id string) string        { return previewBasePath + id + "/text" }

// previewManifest renders (or inspects) a document and returns its page manifest.
// A document that cannot be previewed yields a typed not-renderable result, not an
// error, so the caller can offer "download to review".
//
// @operationId PreviewManifest
// @title Document preview manifest
// @description Inspects a document on behalf of the user and returns the page manifest, or a typed not-renderable result.
// @param documentId path string true "Document id"
// @success 200 Manifest Manifest "The preview manifest, or a not-renderable result"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "Not found"
// @failure 502 string string "Source unavailable"
// @failure 503 {empty} "Document source not configured"
// @resource Preview
// @route /api/v1/previews/{documentId} [get].
func (r *router) previewManifest(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")

	docs := r.Documents()
	if docs == nil {
		r.sourceUnavailable(ctx)

		return
	}
	obo, ok := r.onBehalf(ctx)
	if !ok {
		ctx.Error(corehttp.UnauthorizedError{})

		return
	}
	cfg := r.Config().RenderConfig()

	// Cheap pre-check: reject by declared size before transferring any bytes.
	meta, err := docs.Metadata(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return
	}
	if cfg.CheckSize(meta.Size) != nil {
		ctx.JSON(NotRenderable{DocumentID: id, Reason: "too_large", Mime: meta.Mime})

		return
	}

	content, err := docs.Content(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return
	}
	if cfg.CheckSize(int64(len(content))) != nil {
		ctx.JSON(NotRenderable{DocumentID: id, Reason: "too_large", Mime: render.Sniff(content)})

		return
	}

	sniff := render.Sniff(content)
	if !cfg.Supported(sniff) {
		ctx.JSON(NotRenderable{DocumentID: id, Reason: "unsupported_format", Mime: sniff})

		return
	}

	doc, err := r.Renderer().Inspect(ctx, render.Input{Bytes: content, Mime: sniff})
	if err != nil {
		if reason, ok := renderableReason(err); ok {
			ctx.JSON(NotRenderable{DocumentID: id, Reason: reason, Mime: sniff})

			return
		}
		r.renderFailed(ctx, err)

		return
	}

	pages := make([]PageRef, len(doc.Pages))
	for i, p := range doc.Pages {
		pages[i] = PageRef{Index: i, Width: p.Width, Height: p.Height, ImageRef: pageRef(id, i)}
	}
	ctx.JSON(Manifest{
		PreviewID:    id,
		DocumentID:   id,
		Format:       doc.Format,
		PageCount:    doc.PageCount,
		Pages:        pages,
		TextLayerRef: textRef(id),
		Renderable:   true,
		ExpiresAt:    time.Now().Add(previewHint).UTC().Format(time.RFC3339),
	})
}

// previewPage renders one page to an inert image.
//
// @operationId PreviewPage
// @title Rendered page image
// @description Renders one page of a document on behalf of the user to an inert image.
// @param documentId path string true "Document id"
// @param n path int true "Zero-based page index"
// @success 200 {empty} "The inert page image"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "Not found"
// @failure 415 string string "Unsupported document"
// @failure 502 string string "Source unavailable"
// @resource Preview
// @route /api/v1/previews/{documentId}/pages/{n} [get].
func (r *router) previewPage(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")
	n, err := ctx.Params.Int("n")
	if err != nil {
		ctx.Error(err)

		return
	}

	content, sniff, ok := r.fetchRenderable(ctx, id)
	if !ok {
		return
	}

	img, err := r.Renderer().RenderPage(ctx, render.Input{Bytes: content, Mime: sniff}, n)
	if err != nil {
		if errors.Is(err, render.ErrPageOutOfRange) {
			ctx.Error(corehttp.NotFoundError{Resource: "page"})

			return
		}
		if reason, ok := renderableReason(err); ok {
			r.unsupported(ctx, reason)

			return
		}
		r.renderFailed(ctx, err)

		return
	}

	ctx.Header.Set("Cache-Control", "no-store")
	ctx.ContentType(img.ContentType)
	ctx.Raw(img.Bytes)
}

// previewText returns the extracted plain-text layer, one entry per page. A
// document with no extractable text yields 404 (no text layer).
//
// @operationId PreviewText
// @title Document text layer
// @description Extracts the plain-text layer of a document on behalf of the user.
// @param documentId path string true "Document id"
// @success 200 TextLayer TextLayer "The per-page text layer"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "No text layer"
// @failure 415 string string "Unsupported document"
// @failure 502 string string "Source unavailable"
// @resource Preview
// @route /api/v1/previews/{documentId}/text [get].
func (r *router) previewText(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")

	content, sniff, ok := r.fetchRenderable(ctx, id)
	if !ok {
		return
	}

	texts, err := r.Renderer().Text(ctx, render.Input{Bytes: content, Mime: sniff})
	if err != nil {
		if reason, ok := renderableReason(err); ok {
			r.unsupported(ctx, reason)

			return
		}
		r.renderFailed(ctx, err)

		return
	}

	hasText := false
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			hasText = true

			break
		}
	}
	if !hasText {
		ctx.Error(corehttp.NotFoundError{Resource: "text layer"})

		return
	}

	ctx.JSON(TextLayer{DocumentID: id, Pages: texts})
}

// fetchRenderable resolves the shared prelude of the page/text endpoints: the
// document source must be configured, the caller authorized on behalf of the user,
// the bytes within the size cap, and the sniffed type on the allowlist. It writes
// the appropriate response and returns ok=false when any check fails.
func (r *router) fetchRenderable(ctx *azugo.Context, id string) ([]byte, string, bool) {
	docs := r.Documents()
	if docs == nil {
		r.sourceUnavailable(ctx)

		return nil, "", false
	}
	obo, ok := r.onBehalf(ctx)
	if !ok {
		ctx.Error(corehttp.UnauthorizedError{})

		return nil, "", false
	}
	cfg := r.Config().RenderConfig()

	content, err := docs.Content(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return nil, "", false
	}
	if cfg.CheckSize(int64(len(content))) != nil {
		r.unsupported(ctx, "too_large")

		return nil, "", false
	}
	sniff := render.Sniff(content)
	if !cfg.Supported(sniff) {
		r.unsupported(ctx, "unsupported_format")

		return nil, "", false
	}

	return content, sniff, true
}

// onBehalf builds the on-behalf identity from the authenticated request: the user
// subject and the raw inbound token to exchange. It fails closed without a token.
func (r *router) onBehalf(ctx *azugo.Context) (clients.OnBehalf, bool) {
	tok := subjectToken(ctx)
	if tok == "" {
		return clients.OnBehalf{}, false
	}

	return clients.OnBehalf{Sub: ctx.User().ID(), Token: tok}, true
}

// subjectToken returns the raw access token from the Authorization header, without
// its scheme (Bearer / DPoP).
func subjectToken(ctx *azugo.Context) string {
	h := ctx.Header.Get("Authorization")
	if i := strings.IndexByte(h, ' '); i >= 0 {
		return strings.TrimSpace(h[i+1:])
	}

	return strings.TrimSpace(h)
}

// renderableReason maps a renderer error onto a typed not-renderable reason, or
// reports ok=false for an error that is not a renderability outcome.
func renderableReason(err error) (string, bool) {
	switch {
	case errors.Is(err, render.ErrUnsupportedFormat):
		return "unsupported_format", true
	case errors.Is(err, render.ErrTooManyPages):
		return "too_many_pages", true
	case errors.Is(err, render.ErrTooLarge):
		return "too_large", true
	default:
		return "", false
	}
}

// mapSourceError maps a document-source error onto a safe response: a not-found
// from the source (which is also how it reports a document the user does not own)
// becomes 404; any other upstream failure becomes a generic 502.
func (r *router) mapSourceError(ctx *azugo.Context, err error) {
	var he *clients.HTTPError
	if errors.As(err, &he) && he.StatusCode == fasthttp.StatusNotFound {
		ctx.Error(corehttp.NotFoundError{Resource: "document"})

		return
	}
	ctx.Log().Warn("preview source read failed", zap.Error(err))
	ctx.Error(pkerrors.NewProblem("err:upstream:unavailable",
		pkerrors.WithStatus(fasthttp.StatusBadGateway)))
}

// renderFailed reports a render failure without leaking any engine detail to the
// caller; the error is logged (it carries no document content).
func (r *router) renderFailed(ctx *azugo.Context, err error) {
	ctx.Log().Warn("preview render failed", zap.Error(err))
	ctx.Error(pkerrors.NewProblem("err:preview:renderFailed",
		pkerrors.WithStatus(fasthttp.StatusInternalServerError)))
}

// unsupported reports a typed unsupported-input result on the image/text endpoints;
// the specific reason rides the detail.
func (r *router) unsupported(ctx *azugo.Context, reason string) {
	ctx.Error(pkerrors.NewProblem("err:preview:unsupported",
		pkerrors.WithStatus(fasthttp.StatusUnsupportedMediaType),
		pkerrors.WithTitle("Unsupported media type"),
		pkerrors.WithDetail(reason)))
}

// sourceUnavailable reports that no document source is configured yet.
func (r *router) sourceUnavailable(ctx *azugo.Context) {
	ctx.Error(pkerrors.NewProblem("err:preview:notConfigured",
		pkerrors.WithStatus(fasthttp.StatusServiceUnavailable),
		pkerrors.WithDetail("document source not configured")))
}
