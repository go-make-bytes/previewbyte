package routes

import (
	"azugo.io/azugo"
	"github.com/valyala/fasthttp"
)

// healthz is liveness: the process is up.
func (r *router) healthz(ctx *azugo.Context) {
	ctx.StatusCode(fasthttp.StatusOK)
	ctx.JSON(map[string]string{"status": "ok"})
}

// readyz is readiness: the render engine pool can hand out a live instance.
func (r *router) readyz(ctx *azugo.Context) {
	if err := r.Renderer().Ready(ctx); err != nil {
		ctx.StatusCode(fasthttp.StatusServiceUnavailable)
		ctx.JSON(map[string]string{"status": "not_ready", "error": "render engine unavailable"})

		return
	}
	ctx.StatusCode(fasthttp.StatusOK)
	ctx.JSON(map[string]string{"status": "ready"})
}
