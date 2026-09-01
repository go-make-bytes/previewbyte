package main

import (
	"os"

	"azugo.io/core/cli"
)

// Version is overridden at build time via -ldflags.
var Version = "1.0.0-dev"

func main() {
	if _, ok := os.LookupEnv("SERVER_URLS"); !ok {
		_ = os.Setenv("SERVER_URLS", "http://0.0.0.0:8080")
	}

	cli.Run(cli.Options{
		Use:     "previewbyte",
		Short:   "Document preview/render service",
		Long:    "Renders a document into a safe, review-only preview. Starts the web server by default.",
		Version: Version,
	})
}
