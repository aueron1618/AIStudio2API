FROM --platform=$BUILDPLATFORM node:24.15.0-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
COPY internal/i18n/messages/ /src/internal/i18n/messages/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
ARG TARGETARCH
ARG VERSION=dev
ARG ZIG=0.16.0
ARG SPIDERMONKEY=v158.0.1-aistudio2api.1
ARG LIBWEBP=v1.6.0-aistudio2api.1
RUN apt-get update && apt-get install -y --no-install-recommends xz-utils && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://ziglang.org/download/$ZIG/zig-$(uname -m)-linux-$ZIG.tar.xz | tar -xJ -C /opt \
    && mv /opt/zig-* /opt/zig \
    && mkdir /opt/spidermonkey /opt/libwebp \
    && curl -fsSL https://github.com/Mag1cFall/firefox/releases/download/$SPIDERMONKEY/spidermonkey-$SPIDERMONKEY-linux-$TARGETARCH.tar.gz | tar -xz -C /opt/spidermonkey \
    && curl -fsSL https://github.com/Mag1cFall/libwebp/releases/download/$LIBWEBP/libwebp-$LIBWEBP-linux-$TARGETARCH.tar.gz | tar -xz -C /opt/libwebp
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    target=$([ "$TARGETARCH" = arm64 ] && echo aarch64 || echo x86_64) \
    && CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH \
    CC="/opt/zig/zig cc -target $target-linux-musl" CXX="/opt/zig/zig c++ -target $target-linux-musl" \
    CGO_CPPFLAGS="-I/opt/spidermonkey/include -I/opt/libwebp/include" CGO_LDFLAGS="-L/opt/spidermonkey/lib -L/opt/libwebp/lib" \
    go build -mod=readonly -trimpath -tags netgo,osusergo \
    -ldflags="-s -w -X $(go list -m)/internal/app.version=${VERSION} -linkmode external -extldflags '-static -Wl,--strip-all -Wl,--gc-sections'" -o /out/aistudio2api ./cmd/aistudio2api

# Shared runtime settings, certificates and timezone data for both variants.
FROM debian:bookworm-slim AS runtime-base
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /data
ENV LISTEN_ADDR=0.0.0.0:2048 \
    PREVIEW_LISTEN_ADDR=0.0.0.0:2050
EXPOSE 2048 2050
CMD ["aistudio2api", "-open-ui=false"]

FROM runtime-base AS no-browser
COPY --from=build /out/aistudio2api /usr/local/bin/aistudio2api

# CI reuses the versioned, statically linked Linux package for each architecture.
FROM runtime-base AS release-no-browser
ARG TARGETARCH
COPY release/linux-$TARGETARCH/aistudio2api /usr/local/bin/aistudio2api

# The font pruner runs on the build platform; it never enters the runtime image.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS browser-tools
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/prune-camoufox/ ./cmd/prune-camoufox/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/prune-camoufox ./cmd/prune-camoufox

# Download on the build platform; select the browser archive for the target platform.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS browser-download
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*
# Keep the bundled browser aligned with the version used by the Go downloader.
COPY internal/camoufoxnative/download.go /tmp/camoufox-download.go
COPY internal/camoufoxnative/windows_profile.json internal/camoufoxnative/windows_fonts.json /tmp/
COPY --from=browser-tools /out/prune-camoufox /usr/local/bin/prune-camoufox
# Keep all fingerprint fonts, aliases and locale fallbacks without altering any glyphs.
RUN set -eux; \
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
    test -f /opt/camoufox/application.ini; \
    test -f /opt/camoufox/fontconfig/windows/fonts.conf; \
    test -f /opt/camoufox/fonts/groups.json; \
    prune-camoufox -root /opt/camoufox -profile /tmp/windows_profile.json -font-data /tmp/windows_fonts.json; \
    test -s /opt/camoufox/fonts.sha256; \
    du -sh /opt/camoufox; \
    chmod 755 /opt/camoufox/camoufox-bin; \
    rm /tmp/camoufox.zip /tmp/camoufox-download.go /tmp/windows_profile.json /tmp/windows_fonts.json

FROM runtime-base AS runtime-browser
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgtk-3-0 libasound2 libnss3 libdbus-glib-1-2 libxtst6 libxrandr2 libgbm1 libxkbcommon0 \
    libpango-1.0-0 libcairo2 libxcomposite1 libxdamage1 libxfixes3 libx11-xcb1 \
    xvfb libfontconfig1 libegl1 libgl1 libgl1-mesa-dri libglx-mesa0 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=browser-download /opt/camoufox /opt/camoufox
ENV CAMOUFOX_PATH=/opt/camoufox/camoufox-bin

FROM runtime-browser AS release-browser
ARG TARGETARCH
COPY release/linux-$TARGETARCH/aistudio2api /usr/local/bin/aistudio2api

# Keep the browser-enabled source build as the default docker build target.
FROM runtime-browser AS browser
COPY --from=build /out/aistudio2api /usr/local/bin/aistudio2api
