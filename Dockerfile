# syntax=docker/dockerfile:1

# The web UI and the Go binary are built on the build machine's own platform
# and cross-compiled, so multi-arch images don't run compilers under emulation.

# 1. Web UI
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# 2. Server (static binary, web UI embedded)
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /web/dist ./internal/webui/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 go test ./... && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/coffer ./cmd/coffer

# 3. Media worker (optional container): the same binary plus ffmpeg and
# ImageMagick, for video previews and HEIC/RAW stills. Build with --target media.
FROM alpine:3.24 AS media
RUN apk add --no-cache ffmpeg imagemagick imagemagick-heic imagemagick-raw imagemagick-jpeg \
      imagemagick-tiff imagemagick-webp tini
# ImageMagick may only read the photo formats Coffer asks for and write JPEG:
# no MSL/MVG/TEXT/URL/SVG coders, "@file" arguments or shell delegates, and
# bounded image sizes, whatever a crafted file claims to be.
COPY <<'EOF' /etc/ImageMagick-7/policy.xml
<policymap>
  <policy domain="resource" name="memory" value="1GiB"/>
  <policy domain="resource" name="map" value="2GiB"/>
  <policy domain="resource" name="disk" value="4GiB"/>
  <policy domain="resource" name="width" value="64KP"/>
  <policy domain="resource" name="height" value="64KP"/>
  <policy domain="resource" name="area" value="1GP"/>
  <policy domain="resource" name="time" value="240"/>
  <policy domain="delegate" rights="none" pattern="*"/>
  <policy domain="path" rights="none" pattern="@*"/>
  <policy domain="coder" rights="none" pattern="*"/>
  <policy domain="coder" rights="read" pattern="{HEIC,HEIF,AVIF,TIFF,BMP,PSD,DNG,CR2,CR3,NEF,ARW,RAF,ORF,RW2,PEF,SRW,3FR,RAW}"/>
  <policy domain="coder" rights="write" pattern="JPEG"/>
</policymap>
EOF
COPY --from=build /out/coffer /coffer
ENV COFFER_DATA_DIR=/data \
    COFFER_STORAGE_DIR=/srv/files
USER 65532:65532
ENTRYPOINT ["/sbin/tini", "--", "/coffer", "worker"]

# 4. Runtime: no shell, no package manager, non-root by default
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/coffer /coffer
ENV COFFER_DATA_DIR=/data \
    COFFER_STORAGE_DIR=/srv/files \
    COFFER_LISTEN=:8080
EXPOSE 8080 8081
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/coffer", "healthcheck"]
ENTRYPOINT ["/coffer"]
