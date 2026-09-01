package routes

import (
	"testing"

	api "github.com/go-make-bytes/previewbyte"

	"azugo.io/azugo"
	"github.com/go-quicktest/qt"
	"github.com/valyala/fasthttp"

	"github.com/gmb-lib/go-platform-kit/broker"
	"github.com/gmb-lib/go-sec-events/secevents"
)

// capturingSink is a secevents.Sink test double that records every envelope
// handed to it instead of writing to a logger/SIEM — mirrors document-store's
// own test double for the same interface.
type capturingSink struct {
	events []*broker.Envelope
}

func (s *capturingSink) Emit(_ *azugo.Context, ev *broker.Envelope) error {
	s.events = append(s.events, ev)

	return nil
}

// A scope denial emits exactly one authz.denied NIS2-audit security event —
// previewbyte's own inbound boundary, the one signal only it can see with full
// detail (a caller only ever observes the resulting 403).
func TestScopeDeniedEmitsSecurityEvent(t *testing.T) {
	app := api.TestApp(t)
	if app.Renderer() != nil {
		app.Renderer().Close()
	}
	app.SetRenderer(&fakeRenderer{})
	app.SetDocuments(fakeDocs(&fakeDoer{}))

	sink := &capturingSink{}
	app.SetSecEvents(secevents.NewEmitter(sink))

	qt.Assert(t, qt.IsNil(Init(app)))
	testApp := azugo.NewTestApp(app.App)
	testApp.Start(t)
	defer testApp.Stop()

	tc := testApp.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1",
		tc.WithHeader(hdrScopes, "documents:read"),
		tc.WithHeader("X-Test-Sub", "svc:portal-api"))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(resp.StatusCode(), fasthttp.StatusForbidden))
	fasthttp.ReleaseResponse(resp)

	qt.Assert(t, qt.Equals(len(sink.events), 1))
	ev := sink.events[0]
	qt.Assert(t, qt.Equals(ev.EventType, secevents.EventAuthZDenied))
	qt.Assert(t, qt.IsTrue(ev.Actor != nil))
	qt.Assert(t, qt.Equals(ev.Actor.ID, "svc:portal-api"))
	qt.Assert(t, qt.Equals(ev.Attributes[secevents.AttrRequiredScope], "preview:read"))
}

// An in-scope request emits no security event at all.
func TestScopeGrantedEmitsNoSecurityEvent(t *testing.T) {
	app := api.TestApp(t)
	if app.Renderer() != nil {
		app.Renderer().Close()
	}
	app.SetRenderer(&fakeRenderer{doc: nil, inspectErr: nil})
	app.SetDocuments(fakeDocs(&fakeDoer{metaStatus: fasthttp.StatusNotFound}))

	sink := &capturingSink{}
	app.SetSecEvents(secevents.NewEmitter(sink))

	qt.Assert(t, qt.IsNil(Init(app)))
	testApp := azugo.NewTestApp(app.App)
	testApp.Start(t)
	defer testApp.Stop()

	tc := testApp.TestClient()
	resp, err := tc.Get("/api/v1/previews/doc-1",
		tc.WithHeader(hdrScopes, "preview:read"),
		tc.WithHeader("Authorization", bearer))
	qt.Assert(t, qt.IsNil(err))
	fasthttp.ReleaseResponse(resp)

	qt.Assert(t, qt.Equals(len(sink.events), 0))
}
