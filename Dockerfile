# syntax=docker/dockerfile:1
# docker build -t ghcr.io/nicko170/dismist:VERSION .
# .dockerignore allow-lists the sources, so private evidence/, local data/ and
# node_modules never reach the builder. Build stages run on the build host's
# platform and cross-compile, so multi-arch images need no emulation.

# Legacy UI (web/), served until it is retired.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src ./src
RUN npm run build

# The v1 UI (ui/). Only a stub of ui/dist is committed, so the image always
# builds it, and refuses to continue if the build left the stub in place.
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build && \
    test -s dist/index.html && \
    if grep -q 'UI not built' dist/index.html; then echo 'ui/dist/index.html is still the stub' >&2; exit 1; fi && \
    if [ -z "$(find dist/_ui -type f ! -name .keep | head -n 1)" ]; then echo 'ui/dist/_ui has no assets' >&2; exit 1; fi

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY webassets.go ./
COPY --from=web /src/web/dist ./web/dist
COPY --from=ui /src/ui/dist ./ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/dismistd ./cmd/dismistd && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/dismist ./cmd/dismist

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/dismistd /usr/local/bin/dismistd
COPY --from=go-build /out/dismist /usr/local/bin/dismist
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/dismistd"]
