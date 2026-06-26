// Package routes registers the preview/render service HTTP API. The inbound API is
// DPoP-gated and scope-checked; every document read is owner-filtered end-to-end by
// going out on behalf of the user. All outputs are inert — images, text, and JSON.
package routes

import (
	previewbyte "github.com/go-make-bytes/previewbyte"

	"azugo.io/azugo"
	corehttp "azugo.io/core/http"
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
// production.
func (r *router) requireScope(group, level string) azugo.RequestHandlerFunc {
	relaxed := r.DevAcceptUserToken()

	return func(next azugo.RequestHandler) azugo.RequestHandler {
		return func(ctx *azugo.Context) {
			if !relaxed && !ctx.User().HasScopeLevel(group, level) {
				ctx.Error(corehttp.ForbiddenError{})

				return
			}
			next(ctx)
		}
	}
}
