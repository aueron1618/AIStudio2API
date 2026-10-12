FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/aistudio2api ./cmd/aistudio2api

# Download on the build platform; select the browser archive for the target platform.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS browser
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*
# Keep the bundled browser aligned with the version used by the Go downloader.
COPY internal/camoufoxnative/download.go /tmp/camoufox-download.go
RUN set -eu; \
    version="$(sed -n 's/^const camoufoxRelease = "\([^"]*\)"$/\1/p' /tmp/camoufox-download.go)"; \
    test -n "$version"; \
    case "$TARGETARCH" in \
        amd64) arch=x86_64 ;; \
        arm64) arch=arm64 ;; \
        *) echo "Unsupported Camoufox architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl --fail --location --retry 3 --connect-timeout 30 --max-time 1800 \
        "https://github.com/daijro/camoufox/releases/download/v${version}/camoufox-${version}-lin.${arch}.zip" \
        --output /tmp/camoufox.zip; \
    mkdir -p /opt/camoufox; \
    unzip -q /tmp/camoufox.zip -d /opt/camoufox; \
    test -f /opt/camoufox/camoufox-bin; \
    chmod 755 /opt/camoufox/camoufox-bin; \
    rm /tmp/camoufox.zip /tmp/camoufox-download.go

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata fonts-liberation \
    libgtk-3-0 libasound2 libnss3 libdbus-glib-1-2 libxtst6 libxrandr2 libgbm1 libxkbcommon0 \
    libpango-1.0-0 libcairo2 libxcomposite1 libxdamage1 libxfixes3 libx11-xcb1 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/aistudio2api /usr/local/bin/aistudio2api
COPY --from=browser /opt/camoufox /opt/camoufox
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:2048 \
    CAMOUFOX_PATH=/opt/camoufox/camoufox-bin
EXPOSE 2048
CMD ["aistudio2api", "-open-ui=false"]
