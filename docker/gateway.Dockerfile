# build the gateway as a static binary
FROM golang:1.27.1-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# static (no libc), without local paths nor debug symbols
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# no shell, no package manager, no pass-cli: only the binary
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /gateway
ENTRYPOINT ["/gateway"]
