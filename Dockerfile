# docker build -t ghcr.io/nicko170/dismist:VERSION .
# .dockerignore allow-lists the sources, so private evidence/, local data/ and
# node_modules never reach the builder. Build stages run on the build host's
# platform and cross-compile, so multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src ./src
RUN npm run build

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
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/dismistd ./cmd/dismistd && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/dismist ./cmd/dismist

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/dismistd /usr/local/bin/dismistd
COPY --from=go-build /out/dismist /usr/local/bin/dismist
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/dismistd"]
