package routes

import (
	"errors"
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"azugo.io/azugo"
	corehttp "azugo.io/core/http"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	pkerrors "github.com/gmb-lib/go-platform-kit/errors"
	pkweb "github.com/gmb-lib/go-platform-kit/web"

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

// innerPageRef / innerTextRef are the manifest references for one inner file of an
// ASiC-E container: they carry the container id and the inner file name, mirroring
// the document source's `/data-objects/{name}` extraction path.
func innerPageRef(id, name string, i int) string {
	return fmt.Sprintf("%s%s/data-objects/%s/pages/%d", previewBasePath, id, neturl.PathEscape(name), i)
}
func innerTextRef(id, name string) string {
	return fmt.Sprintf("%s%s/data-objects/%s/text", previewBasePath, id, neturl.PathEscape(name))
}

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

	docs, obo, ok := r.source(ctx)
	if !ok {
		return
	}

	// Cheap pre-check: reject by declared size before transferring any bytes.
	meta, err := docs.Metadata(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return
	}
	if r.Config().RenderConfig().CheckSize(meta.Size) != nil {
		ctx.JSON(NotRenderable{DocumentID: id, Reason: "too_large", Mime: meta.Mime})

		return
	}

	content, err := docs.Content(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return
	}

	r.writeManifest(ctx, id, content, func(i int) string { return pageRef(id, i) }, textRef(id))
}

// previewInnerManifest renders one inner file of an ASiC-E container and returns its
// page manifest. A multi-file bundle absorbs its originals into the container, so an
// inner file has no document id of its own — it is addressed by (container id, inner
// name); the bytes are extracted on the user's behalf, then inspected exactly like a
// whole document.
//
// @operationId PreviewInnerManifest
// @title Inner-file preview manifest
// @description Extracts one inner file of an ASiC-E container on behalf of the user and returns its page manifest, or a typed not-renderable result.
// @param documentId path string true "Container document id"
// @param name path string true "Inner file name"
// @success 200 Manifest Manifest "The preview manifest, or a not-renderable result"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "Not found"
// @failure 502 string string "Source unavailable"
// @failure 503 {empty} "Document source not configured"
// @resource Preview
// @route /api/v1/previews/{documentId}/data-objects/{name} [get].
func (r *router) previewInnerManifest(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")
	name := pkweb.PathParam(ctx, "name")

	docs, obo, ok := r.source(ctx)
	if !ok {
		return
	}

	// No metadata pre-check: an inner file has no metadata endpoint; the extraction
	// returns just its bytes, which the size cap in writeManifest still guards.
	content, err := docs.ExtractObject(ctx, id, name, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return
	}

	r.writeManifest(ctx, id, content,
		func(i int) string { return innerPageRef(id, name, i) }, innerTextRef(id, name))
}

// writeManifest inspects already-fetched content and writes the preview manifest, or
// a typed not-renderable result (too_large / unsupported_format / a renderer
// renderability reason) — never an error the UI must guess at. The ref builders let
// a whole-document and an inner-file manifest share this tail.
func (r *router) writeManifest(ctx *azugo.Context, id string, content []byte, pageRefFn func(int) string, textRefStr string) {
	cfg := r.Config().RenderConfig()
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
		if errors.Is(err, render.ErrUpstreamUnavailable) {
			r.upstreamUnavailable(ctx, err)

			return
		}
		if reason, ok := renderableReason(err); ok {
			ctx.JSON(NotRenderable{DocumentID: id, Reason: reason, Mime: sniff})

			return
		}
		r.renderFailed(ctx, err)

		return
	}

	pages := make([]PageRef, len(doc.Pages))
	for i, p := range doc.Pages {
		pages[i] = PageRef{Index: i, Width: p.Width, Height: p.Height, ImageRef: pageRefFn(i)}
	}
	ctx.JSON(Manifest{
		PreviewID:    id,
		DocumentID:   id,
		Format:       doc.Format,
		PageCount:    doc.PageCount,
		Pages:        pages,
		TextLayerRef: textRefStr,
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

	r.writePage(ctx, content, sniff, n)
}

// previewInnerPage renders one page of one inner file of an ASiC-E container.
//
// @operationId PreviewInnerPage
// @title Rendered inner-file page image
// @description Renders one page of one inner file of an ASiC-E container on behalf of the user to an inert image.
// @param documentId path string true "Container document id"
// @param name path string true "Inner file name"
// @param n path int true "Zero-based page index"
// @success 200 {empty} "The inert page image"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "Not found"
// @failure 415 string string "Unsupported document"
// @failure 502 string string "Source unavailable"
// @resource Preview
// @route /api/v1/previews/{documentId}/data-objects/{name}/pages/{n} [get].
func (r *router) previewInnerPage(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")
	name := pkweb.PathParam(ctx, "name")
	n, err := ctx.Params.Int("n")
	if err != nil {
		ctx.Error(err)

		return
	}

	content, sniff, ok := r.fetchRenderableInner(ctx, id, name)
	if !ok {
		return
	}

	r.writePage(ctx, content, sniff, n)
}

// writePage renders one page of already-fetched content to an inert image, mapping
// the render outcomes onto the page endpoint's responses.
func (r *router) writePage(ctx *azugo.Context, content []byte, sniff string, n int) {
	img, err := r.Renderer().RenderPage(ctx, render.Input{Bytes: content, Mime: sniff}, n)
	if err != nil {
		if errors.Is(err, render.ErrPageOutOfRange) {
			ctx.Error(corehttp.NotFoundError{Resource: "page"})

			return
		}
		if errors.Is(err, render.ErrUpstreamUnavailable) {
			r.upstreamUnavailable(ctx, err)

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

	r.writeText(ctx, id, content, sniff)
}

// previewInnerText returns the plain-text layer of one inner file of a container.
//
// @operationId PreviewInnerText
// @title Inner-file text layer
// @description Extracts the plain-text layer of one inner file of an ASiC-E container on behalf of the user.
// @param documentId path string true "Container document id"
// @param name path string true "Inner file name"
// @success 200 TextLayer TextLayer "The per-page text layer"
// @failure 401 {empty} "Unauthorized"
// @failure 403 {empty} "Forbidden"
// @failure 404 {empty} "No text layer"
// @failure 415 string string "Unsupported document"
// @failure 502 string string "Source unavailable"
// @resource Preview
// @route /api/v1/previews/{documentId}/data-objects/{name}/text [get].
func (r *router) previewInnerText(ctx *azugo.Context) {
	id := ctx.Params.String("documentId")
	name := pkweb.PathParam(ctx, "name")

	content, sniff, ok := r.fetchRenderableInner(ctx, id, name)
	if !ok {
		return
	}

	r.writeText(ctx, id, content, sniff)
}

// writeText extracts the plain-text layer of already-fetched content, one entry per
// page; content with no extractable text yields 404 (no text layer).
func (r *router) writeText(ctx *azugo.Context, id string, content []byte, sniff string) {
	texts, err := r.Renderer().Text(ctx, render.Input{Bytes: content, Mime: sniff})
	if err != nil {
		if errors.Is(err, render.ErrUpstreamUnavailable) {
			r.upstreamUnavailable(ctx, err)

			return
		}
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
	// No extractable text (e.g. an image) is a normal, EMPTY layer — a 200 with no
	// pages, not a 404. The caller fetches the text layer best-effort; a 404 here only
	// produces misleading "failed request" noise in the browser console.
	if !hasText {
		ctx.JSON(TextLayer{DocumentID: id, Pages: []string{}})

		return
	}

	ctx.JSON(TextLayer{DocumentID: id, Pages: texts})
}

// source resolves the document client and the on-behalf identity, writing the
// failure response and returning ok=false when the source is unconfigured or the
// caller presented no subject token (the on-behalf read fails closed).
func (r *router) source(ctx *azugo.Context) (*clients.Documents, clients.OnBehalf, bool) {
	docs := r.Documents()
	if docs == nil {
		r.sourceUnavailable(ctx)

		return nil, clients.OnBehalf{}, false
	}
	obo, ok := r.onBehalf(ctx)
	if !ok {
		ctx.Error(corehttp.UnauthorizedError{})

		return nil, clients.OnBehalf{}, false
	}

	return docs, obo, true
}

// fetchRenderable resolves the page/text prelude for a whole document: the bytes are
// fetched on behalf of the user, then checked against the size cap and the format
// allowlist. It writes the failure response and returns ok=false on any miss.
func (r *router) fetchRenderable(ctx *azugo.Context, id string) ([]byte, string, bool) {
	docs, obo, ok := r.source(ctx)
	if !ok {
		return nil, "", false
	}

	content, err := docs.Content(ctx, id, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return nil, "", false
	}

	return r.checkRenderable(ctx, content)
}

// fetchRenderableInner is fetchRenderable for one inner file of a container: the
// bytes come from the container's `/data-objects/{name}` extraction rather than a
// whole-document content read.
func (r *router) fetchRenderableInner(ctx *azugo.Context, containerID, name string) ([]byte, string, bool) {
	docs, obo, ok := r.source(ctx)
	if !ok {
		return nil, "", false
	}

	content, err := docs.ExtractObject(ctx, containerID, name, obo)
	if err != nil {
		r.mapSourceError(ctx, err)

		return nil, "", false
	}

	return r.checkRenderable(ctx, content)
}

// checkRenderable applies the size cap and format allowlist to fetched bytes for the
// page/text endpoints, where a miss is a 415 — the manifest (the discovery endpoint)
// reports the same misses instead as a typed 200 not-renderable result.
func (r *router) checkRenderable(ctx *azugo.Context, content []byte) ([]byte, string, bool) {
	cfg := r.Config().RenderConfig()
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

// mapSourceError maps a document-source error onto a safe response. A not-found
// from the source (also how it reports a document the user does not own) becomes
// this service's own 404. Any other upstream error is relayed — its terminal code,
// source, and trace id are preserved and this hop appended, rather than collapsed
// to a bare gateway error; a client-actionable status is kept and a server-side
// failure maps to 502. A transport failure with no HTTP response at all becomes a
// uniform upstream-unavailable.
func (r *router) mapSourceError(ctx *azugo.Context, err error) {
	var he *clients.HTTPError
	if errors.As(err, &he) {
		if he.StatusCode == fasthttp.StatusNotFound {
			ctx.Error(corehttp.NotFoundError{Resource: "document"})

			return
		}
		outer := he.StatusCode
		if outer >= fasthttp.StatusInternalServerError {
			outer = fasthttp.StatusBadGateway
		}
		down, _ := pkerrors.ParseProblem([]byte(he.Body))
		ctx.Error(pkerrors.Relay(down, r.AppName, outer))

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

// upstreamUnavailable reports that a render backend's remote dependency (the
// Office document converter) could not be reached — a 502, distinct from both a
// not-renderable input (200 typed result) and a rendering bug (500): this is
// "try again," not "download to review."
func (r *router) upstreamUnavailable(ctx *azugo.Context, err error) {
	ctx.Log().Warn("preview render backend unavailable", zap.Error(err))
	ctx.Error(pkerrors.NewProblem("err:preview:upstreamUnavailable",
		pkerrors.WithStatus(fasthttp.StatusBadGateway)))
}

// unsupported reports a typed unsupported-input result on the image/text endpoints;
// the specific reason rides the detail.
func (r *router) unsupported(ctx *azugo.Context, reason string) {
	ctx.Error(pkerrors.NewProblem("err:preview:unsupported",
		pkerrors.WithStatus(fasthttp.StatusUnsupportedMediaType),
		pkerrors.WithDetail(reason)))
}

// sourceUnavailable reports that no document source is configured yet.
func (r *router) sourceUnavailable(ctx *azugo.Context) {
	ctx.Error(pkerrors.NewProblem("err:preview:notConfigured",
		pkerrors.WithStatus(fasthttp.StatusServiceUnavailable),
		pkerrors.WithDetail("document source not configured")))
}
