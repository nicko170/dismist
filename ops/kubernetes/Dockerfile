# Build with the repository root as context:
# docker build -f ops/kubernetes/Dockerfile -t registry.example/dismist:VERSION .
# Explicit COPY paths keep private evidence, local data, and node_modules out of the image.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src ./src
RUN npm run build

FROM golang:1.27-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY webassets.go ./
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dismistd ./cmd/dismistd && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dismist ./cmd/dismist

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/dismistd /usr/local/bin/dismistd
COPY --from=go-build /out/dismist /usr/local/bin/dismist
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/dismistd"]
