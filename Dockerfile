ARG GO_VERSION=1.26.4

FROM golang:${GO_VERSION} AS build
WORKDIR /src

COPY . .

RUN go mod download
# CGO is disabled: the PDF engine runs as WebAssembly inside a pure-Go runtime, so
# the binary is fully static and needs no system libraries.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server

EXPOSE 8080/tcp
ENTRYPOINT ["/server", "web"]
HEALTHCHECK --start-period=20s --start-interval=5s --interval=1m --timeout=10s --retries=5 \
    CMD ["/server", "health"]

# Deployment note (enforced by the orchestrator, not this image): this service
# opens untrusted document content, so run the container locked down — no network
# egress, a read-only root filesystem with a small tmpfs scratch mount, no added
# capabilities and no privilege escalation, and memory/CPU limits. The WebAssembly
# runtime isolates the parser in-process; these container controls are the second
# layer around it.
