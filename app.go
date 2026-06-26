// Package previewbyte is the document preview/render service: it turns a document
// into a safe, review-only rendering — inert per-page images plus an optional
// plain-text layer — so a person can read a document before they sign or send it.
//
// It is the one place in the platform that opens untrusted document bytes, so its
// whole reason to exist is to do that dangerous, complex job once behind hard
// isolation, instead of every product rendering inline. The parser runs inside a
// memory-isolated WebAssembly runtime; the container around it adds no egress, a
// read-only filesystem, and resource caps.
//
// It holds no durable data (the document source owns the bytes) and no signing
// crypto. It reads the source on behalf of the user via token exchange, so it can
// only ever see documents the caller could see. Cross-cutting concerns (logging
// with redaction, tracing, correlation) are installed once by the shared
// platform-kit and are never wired per-service.
package previewbyte

import (
	"fmt"

	"azugo.io/azugo"
	"azugo.io/azugo/server"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/gmb-lib/go-authbyte/authclient"
	"github.com/gmb-lib/go-platform-kit/platform"

	"github.com/go-make-bytes/previewbyte/clients"
	"github.com/go-make-bytes/previewbyte/render"
)

// App is the preview/render application container.
type App struct {
	*azugo.App

	config *Configuration

	// Inbound service authentication (DPoP). The caller is the backend-for-frontend,
	// presenting a service token for this service's audience and reading on behalf
	// of the user.
	authClient *authclient.Client
	authMW     azugo.RequestHandlerFunc

	// Outbound DPoP service client — reads the source on behalf of the user via
	// token exchange. Nil until a document source is configured.
	outboundClient *authclient.Client

	// documents reads document metadata and content on behalf of the user. Nil until
	// a document source is configured; the by-reference routes then report not-ready.
	documents *clients.Documents

	// renderer turns document bytes into an inert preview inside the isolated
	// WebAssembly runtime. Always present after init.
	renderer render.Renderer
}

// New constructs the application.
func New(cmd *cobra.Command, version string) (*App, error) {
	config := NewConfiguration()

	a, err := server.New(cmd, server.Options{
		AppName:       "Document preview/render service (previewbyte)",
		AppVer:        version,
		Configuration: config,
	})
	if err != nil {
		return nil, err
	}

	app := &App{App: a, config: config}
	if err := app.init(); err != nil {
		return nil, err
	}

	return app, nil
}

func (a *App) init() error {
	cfg := a.config

	// Platform glue FIRST: logging + redaction, tracing, correlation.
	if err := platform.Setup(a.App, platform.Options{Config: cfg.BaseConfiguration}); err != nil {
		return err
	}

	var err error

	// Inbound service authentication (DPoP). Development-only concession (mirrors the
	// document + signing services): accept the demo single-page app's public-client
	// user token (aud = DevUserAudience) and relax per-endpoint scope checks.
	if cfg.DevAcceptUserToken {
		a.Log().Warn("DEV_ACCEPT_USER_TOKEN is set — accepting public-client USER tokens (aud="+cfg.DevUserAudience+") on the preview API and RELAXING scope checks. DEVELOPMENT ONLY; never enable in production.",
			zap.String("dev_user_audience", cfg.DevUserAudience))
		cfg.Auth.ServiceAudience = cfg.DevUserAudience
	}

	a.authClient, err = authclient.New(cfg.Auth)
	if err != nil {
		return fmt.Errorf("previewbyte: auth client: %w", err)
	}
	a.authMW = a.authClient.Authenticate()

	// Outbound DPoP service client — reads the source on behalf of the user.
	if cfg.OutboundEnabled() {
		a.outboundClient, err = authclient.New(cfg.OutboundAuthClientConfig())
		if err != nil {
			return fmt.Errorf("previewbyte: outbound auth client: %w", err)
		}
	}

	// Document source (bytes + metadata read on behalf of the user).
	if cfg.DocumentEnabled() {
		a.documents = clients.NewDocuments(a.outboundClient, cfg.DocumentBaseURL, cfg.DocumentAudience)
	} else {
		a.Log().Warn("no document base URL set (DOCUMENT_BASE_URL) — the by-reference preview will report not-ready until it is configured")
	}

	// The renderer: a pool of WebAssembly PDFium instances. A render of untrusted
	// bytes runs entirely inside the sandboxed runtime; a parser fault fails one job.
	a.renderer, err = render.NewPDFium(cfg.RenderConfig())
	if err != nil {
		return fmt.Errorf("previewbyte: renderer: %w", err)
	}

	return nil
}

// Stop releases the renderer pool, then stops the server.
func (a *App) Stop() {
	if a.renderer != nil {
		a.renderer.Close()
	}
	a.App.Stop()
}

// Config returns the loaded configuration.
func (a *App) Config() *Configuration {
	if a.config == nil || !a.config.Ready() {
		panic("configuration is not loaded")
	}

	return a.config
}

// AuthMiddleware returns the inbound service-authentication middleware.
func (a *App) AuthMiddleware() azugo.RequestHandlerFunc { return a.authMW }

// Documents returns the document-source client (nil until a source is configured).
func (a *App) Documents() *clients.Documents { return a.documents }

// Renderer returns the preview renderer.
func (a *App) Renderer() render.Renderer { return a.renderer }

// DevAcceptUserToken reports whether the development-only user-token concession is
// on (scope checks are then relaxed). Always false in production.
func (a *App) DevAcceptUserToken() bool { return a.config.DevAcceptUserToken }

// SetAuthMiddleware overrides the inbound auth middleware (test use only).
func (a *App) SetAuthMiddleware(mw azugo.RequestHandlerFunc) { a.authMW = mw }

// SetDocuments injects the document-source client (test use only).
func (a *App) SetDocuments(d *clients.Documents) { a.documents = d }

// SetRenderer injects the renderer (test use only).
func (a *App) SetRenderer(r render.Renderer) { a.renderer = r }
