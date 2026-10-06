# build the broker as a static binary
# runs on the build machine, go cross-compiles to the target platform
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
# static (no libc), without local paths nor debug symbols
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/broker ./cmd/broker

# download the official pass-cli and check it: a changed file fails the build
# runs on the build machine too: it only downloads the binary for TARGETARCH
FROM --platform=$BUILDPLATFORM debian:12-slim AS passcli
ARG TARGETARCH
ARG PASS_CLI_VERSION=2.4.1
ARG PASS_CLI_SHA256_AMD64=f4188430466e0a3d668b56791a8b430162cb20ceb108fed4fdbfcfe77d3080e6
ARG PASS_CLI_SHA256_ARM64=655a3f6c6ebf86b0bc67ccb2e099a0901a3adf13f8cc074d965886bfe70403fa
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN case "$TARGETARCH" in \
      amd64) arch=x86_64;  sum=$PASS_CLI_SHA256_AMD64 ;; \
      arm64) arch=aarch64; sum=$PASS_CLI_SHA256_ARM64 ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl -fsSLo /pass-cli "https://proton.me/download/pass-cli/${PASS_CLI_VERSION}/pass-cli-linux-${arch}" \
    && echo "${sum}  /pass-cli" | sha256sum -c - \
    && chmod 0555 /pass-cli

# pass-cli needs glibc >= 2.34 and libgcc_s: distroless/cc has both, no shell
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /out/broker /usr/local/bin/broker
COPY --from=passcli /pass-cli /usr/local/bin/pass-cli
ENTRYPOINT ["/usr/local/bin/broker"]
