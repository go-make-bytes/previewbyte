package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	api "github.com/go-make-bytes/previewbyte"
	"github.com/go-make-bytes/previewbyte/clients"
	"github.com/go-make-bytes/previewbyte/render"

	"azugo.io/azugo"
	"github.com/gmb-lib/go-authbyte/authclient"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"
)

// fakeDoer stands in for the on-behalf service client: it answers the document
// metadata and content reads from canned values, keyed on the URL shape.
type fakeDoer struct {
	meta          clients.Meta
	content       []byte
	metaStatus    int
	contentStatus int
	metaBody      []byte // when set, the raw metadata-read body (e.g. an upstream problem+json)
}

func (f *fakeDoer) DoServiceOnBehalf(_ context.Context, _, _, _, subjectToken, _, fullURL string, _ http.Header, _ []byte) (*authclient.BackgroundResponse, error) {
	if subjectToken == "" {
		// The real client fails closed before reaching here; guard anyway.
		return &authclient.BackgroundResponse{StatusCode: http.StatusUnauthorized}, nil
	}
	// The content read declares its render-conduit purpose in the query, so
	// match the path with or without it.
	if strings.HasSuffix(fullURL, "/content") || strings.Contains(fullURL, "/content?") {
		return &authclient.BackgroundResponse{StatusCode: orDefault(f.contentStatus), Body: f.content}, nil
	}
	body := f.metaBody
	if body == nil {
		body, _ = json.Marshal(f.meta)
	}

	return &authclient.BackgroundResponse{StatusCode: orDefault(f.metaStatus), Body: body}, nil
}

func orDefault(s int) int {
	if s == 0 {
		return http.StatusOK
	}

	return s
}

// fakeRenderer returns canned render output so the handler logic is tested without
// a real PDF fixture.
type fakeRenderer struct {
	doc        *render.Document
	img        *render.Image
	text       []string
	inspectErr error
	pageErr    error
	textErr    error
}

func (f *fakeRenderer) Inspect(context.Context, render.Input) (*render.Document, error) {
	return f.doc, f.inspectErr
}

func (f *fakeRenderer) RenderPage(context.Context, render.Input, int) (*render.Image, error) {
	return f.img, f.pageErr
}

func (f *fakeRenderer) Text(context.Context, render.Input) ([]string, error) {
	return f.text, f.textErr
}

func (f *fakeRenderer) Ready(context.Context) error { return nil }
func (f *fakeRenderer) Close()                      {}

// pdfBytes sniffs as application/pdf; unsupportedBytes sniffs as
// application/octet-stream — binary, not text, and matches no allowlisted
// signature (unlike GIF/PNG/JPEG/plain text, which this service now renders).
var (
	pdfBytes         = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF")
	unsupportedBytes = []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0xfd, 0x10, 0x11, 0x12}
)

func testApp(t testing.TB, docs *clients.Documents, rnd render.Renderer) *azugo.TestApp {
	app := api.TestApp(t)
	if app.Renderer() != nil {
		app.Renderer().Close()
	}
	app.SetRenderer(rnd)
	app.SetDocuments(docs)

	qt.Assert(t, qt.IsNil(Init(app)))

	return azugo.NewTestApp(app.App)
}

func fakeDocs(d clients.Doer) *clients.Documents {
	return clients.NewDocuments(d, "http://document.test", "svc:document")
}

const (
	hdrScopes = "X-Test-Scopes"
	bearer    = "Bearer test-subject-token"
)

func TestHealthzOK(t *testing.T) {
	app := testApp(t, fakeDocs(&fakeDoer{}), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	resp, err := app.TestClient().Get("/healthz")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))
	fasthttp.ReleaseResponse(resp)
}

func TestReadyzOK(t *testing.T) {
	app := testApp(t, fakeDocs(&fakeDoer{}), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	resp, err := app.TestClient().Get("/readyz")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))
	fasthttp.ReleaseResponse(resp)
}

// The preview API requires authentication.
func TestManifestRequiresAuth(t *testing.T) {
	app := testApp(t, fakeDocs(&fakeDoer{}), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	resp, err := app.TestClient().Get("/api/v1/previews/doc-1")
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusUnauthorized))
	fasthttp.ReleaseResponse(resp)
}

// An authenticated caller without the preview scope is refused (403).
func TestManifestScopeForbidden(t *testing.T) {
	app := testApp(t, fakeDocs(&fakeDoer{}), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1", tc.WithHeader(hdrScopes, "documents:read"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusForbidden))
	fasthttp.ReleaseResponse(resp)
}

// With the scope but no inbound token, the on-behalf read fails closed (401).
func TestManifestNoSubjectToken(t *testing.T) {
	app := testApp(t, fakeDocs(&fakeDoer{}), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1", tc.WithHeader(hdrScopes, "preview:read"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusUnauthorized))
	fasthttp.ReleaseResponse(resp)
}

// A renderable PDF returns a manifest with the page list.
func TestManifestRenderable(t *testing.T) {
	doer := &fakeDoer{meta: clients.Meta{ID: "doc-1", Mime: "application/pdf", Size: int64(len(pdfBytes))}, content: pdfBytes}
	rnd := &fakeRenderer{doc: &render.Document{Format: "pdf", PageCount: 2, Pages: []render.PageDim{{Width: 800, Height: 1000}, {Width: 800, Height: 1000}}}}

	app := testApp(t, fakeDocs(doer), rnd)
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))

	body, err := resp.BodyUncompressed()
	fasthttp.ReleaseResponse(resp)
	qt.Assert(t, qt.IsNil(err))

	var m Manifest
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &m)))
	qt.Assert(t, qt.IsTrue(m.Renderable))
	qt.Assert(t, qt.Equals(m.PageCount, 2))
	qt.Assert(t, qt.Equals(len(m.Pages), 2))
	qt.Assert(t, qt.Equals(m.Pages[0].ImageRef, "/api/v1/previews/doc-1/pages/0"))
	qt.Assert(t, qt.Equals(m.TextLayerRef, "/api/v1/previews/doc-1/text"))
}

// A non-previewable type yields a typed not-renderable result, not an error.
func TestManifestUnsupported(t *testing.T) {
	doer := &fakeDoer{meta: clients.Meta{ID: "doc-2", Mime: "application/octet-stream", Size: int64(len(unsupportedBytes))}, content: unsupportedBytes}

	app := testApp(t, fakeDocs(doer), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-2", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))

	body, err := resp.BodyUncompressed()
	fasthttp.ReleaseResponse(resp)
	qt.Assert(t, qt.IsNil(err))

	var nr NotRenderable
	qt.Assert(t, qt.IsNil(json.Unmarshal(body, &nr)))
	qt.Assert(t, qt.IsFalse(nr.Renderable))
	qt.Assert(t, qt.Equals(nr.Reason, "unsupported_format"))
}

// Owner isolation: a document the user does not own is not-found at the source and
// surfaces as 404 — never another user's content.
func TestManifestNotOwned(t *testing.T) {
	doer := &fakeDoer{metaStatus: http.StatusNotFound}

	app := testApp(t, fakeDocs(doer), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/not-mine", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusNotFound))
	fasthttp.ReleaseResponse(resp)
}

// A non-404 upstream error is relayed, not collapsed to a bare 502: the
// client-actionable status is preserved and the terminal code survives the hop.
func TestManifestUpstreamErrorRelayed(t *testing.T) {
	doer := &fakeDoer{
		metaStatus: http.StatusForbidden,
		metaBody:   []byte(`{"title":"Forbidden","status":403,"code":"err:document:forbidden"}`),
	}

	app := testApp(t, fakeDocs(doer), &fakeRenderer{})
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-x", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusForbidden)) // preserved, not collapsed to 502
	body, err := resp.BodyUncompressed()
	fasthttp.ReleaseResponse(resp)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(string(body), "err:document:forbidden"))) // terminal code relayed, not rewritten
}

// A render backend whose remote dependency is unreachable (the Office converter)
// is a distinguishable 502, not the generic 500 renderFailed falls through to, and
// not a false "unsupported_format".
func TestManifestUpstreamUnavailable(t *testing.T) {
	doer := &fakeDoer{meta: clients.Meta{ID: "doc-3", Mime: "application/pdf", Size: int64(len(pdfBytes))}, content: pdfBytes}
	rnd := &fakeRenderer{inspectErr: render.ErrUpstreamUnavailable}

	app := testApp(t, fakeDocs(doer), rnd)
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-3", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusBadGateway))
	fasthttp.ReleaseResponse(resp)
}

// A page renders to an inert image.
func TestPageImage(t *testing.T) {
	doer := &fakeDoer{meta: clients.Meta{ID: "doc-1", Mime: "application/pdf", Size: int64(len(pdfBytes))}, content: pdfBytes}
	rnd := &fakeRenderer{img: &render.Image{Bytes: []byte("\x89PNG\r\n\x1a\n"), ContentType: "image/png", Width: 800, Height: 1000}}

	app := testApp(t, fakeDocs(doer), rnd)
	app.Start(t)
	defer app.Stop()

	tc := app.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1/pages/0", tc.WithHeader(hdrScopes, "preview:read"), tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusOK))
	qt.Assert(t, qt.IsTrue(strings.HasPrefix(string(resp.Header.ContentType()), "image/png")))
	fasthttp.ReleaseResponse(resp)
}
