package main

import (
	app "github.com/go-make-bytes/previewbyte"

	"azugo.io/azugo/server"
	"azugo.io/core/cli"
)

func init() {
	cli.Register(server.HealthCommand("/healthz", server.Options{
		AppName:       "Document preview/render service (previewbyte)",
		AppVer:        Version,
		Configuration: app.NewConfiguration(),
	}))
}
