# syntax=docker/dockerfile:1.7

# Build stage runs on the host platform (native) to avoid slow QEMU emulation.
# Go cross-compiles the binary for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS TARGETARCH TARGETVARIANT

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-${TARGETOS}-${TARGETARCH}${TARGETVARIANT} \
    GOARM=$(echo "${TARGETVARIANT}" | sed 's/^v//') \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/fmlocal ./cmd/fmlocal

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/fmlocal /app/fmlocal

EXPOSE 9080 9081
USER nonroot:nonroot
ENTRYPOINT ["/app/fmlocal"]
CMD ["-config", "/etc/fmlocal/config.yaml"]
