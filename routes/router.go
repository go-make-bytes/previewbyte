// Package routes registers the preview/render service HTTP API. The inbound API is
// DPoP-gated and scope-checked; every document read is owner-filtered end-to-end by
// going out on behalf of the user. All outputs are inert — images, text, and JSON.
package routes

import (
	previewbyte "github.com/go-make-bytes/previewbyte"

	"azugo.io/azugo"
	corehttp "azugo.io/core/http"
	"go.uber.org/zap"

	"github.com/gmb-lib/go-platform-kit/broker"
	"github.com/gmb-lib/go-sec-events/secevents"
)

type router struct {
	*previewbyte.App
}

// Scopes are modelled as group:level; the preview API requires preview:read.
const (
	scopeGroupPreview = "preview"
	scopeLevelRead    = "read"
)

// Init registers all routes.
func Init(a *previewbyte.App) error {
	r := &router{App: a}

	// Public liveness/readiness.
	a.Get("/healthz", r.healthz)
	a.Get("/readyz", r.readyz)

	// Authenticated, scope-checked API.
	v1 := a.Group("/api/v1")
	v1.Use(a.AuthMiddleware())
	v1.Use(r.requireScope(scopeGroupPreview, scopeLevelRead))

	v1.Get("/previews/{documentId}", r.previewManifest)
	v1.Get("/previews/{documentId}/pages/{n}", r.previewPage)
	v1.Get("/previews/{documentId}/text", r.previewText)

	return nil
}

// requireScope rejects callers without the given scope group at the given level.
// The development-only user-token concession relaxes the check (it accepts the demo
// app's public-client token, which carries no service scopes); it is never on in
// production. A denial is also a NIS2-audit security event — previewbyte's own
// inbound boundary is the one thing only it can see with full fidelity.
func (r *router) requireScope(group, level string) azugo.RequestHandlerFunc {
	relaxed := r.DevAcceptUserToken()
	requiredScope := group + ":" + level

	return func(next azugo.RequestHandler) azugo.RequestHandler {
		return func(ctx *azugo.Context) {
			if !relaxed && !ctx.User().HasScopeLevel(group, level) {
				r.denied(ctx, requiredScope)
				ctx.Error(corehttp.ForbiddenError{})

				return
			}
			next(ctx)
		}
	}
}

// denied emits the platform-standard authz.denied security event on a scope
// denial. Mirrors document-store's Audit().Denied — same event, same call shape.
func (r *router) denied(ctx *azugo.Context, requiredScope string) {
	sec := r.SecEvents()
	if sec == nil {
		return
	}
	if err := sec.AuthZDenied(ctx, secevents.Denial{
		Actor:         broker.Actor{ID: ctx.User().ID(), Type: "service"},
		RequiredScope: requiredScope,
		Reason:        "missing scope",
	}); err != nil {
		ctx.Log().Error("secevents denied emit failed", zap.Error(err))
	}
}
