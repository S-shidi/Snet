# ---- Stage 1: Build snetd + snetctl ----
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN apk add --no-cache git

# Cache dependencies
WORKDIR /src
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# Build static binaries
COPY . .
RUN LDFLAGS="-s -w -X snet/internal/buildinfo.Version=${VERSION} -X snet/internal/buildinfo.Commit=${COMMIT} -X snet/internal/buildinfo.BuildDate=${BUILD_DATE}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="$LDFLAGS" -o /out/snetd   ./cmd/client/snetd && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="$LDFLAGS" -o /out/snetctl ./cmd/client/snetctl

# ---- Stage 1b: Build web UI from shared TypeScript ----
FROM node:20-alpine AS webbuilder

RUN npm install -g esbuild@0.25.12

WORKDIR /src
# Copy package manifests first so npm can install the web runtime deps
# (qrcode-generator) that the shared UI imports; --ignore-scripts avoids
# running postinstall hooks in the builder.
COPY deploy/docker/web-build/package.json deploy/docker/web-build/package-lock.json ./
RUN npm install --omit=dev --ignore-scripts

# Copy shared TypeScript source + HTML/CSS
COPY shared/web/ ./shared/web/
# Copy web adapter (preserve deploy/docker/web-build/ layout so relative
# imports resolve identically locally and in the builder).
COPY deploy/docker/web-build/web.ts ./deploy/docker/web-build/web.ts

# Bundle TypeScript → single app.js (ES2022 target, IIFE format)
RUN esbuild deploy/docker/web-build/web.ts \
    --bundle --format=iife --target=es2022 \
    --outfile=web-out/app.js \
    --minify

# ---- Stage 2: Runtime ----
FROM alpine:3.19

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="SNET" \
      org.opencontainers.image.description="SNET client daemon and web console" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

RUN apk add --no-cache wireguard-tools curl nginx

COPY --from=builder /out/snetd   /usr/local/bin/snetd
COPY --from=builder /out/snetctl /usr/local/bin/snetctl

RUN install -d -m 700 /data

# Web UI static files (from webbuilder stage)
RUN install -d -m 755 /var/www/snet
COPY --from=webbuilder /src/shared/web/index.html /var/www/snet/
COPY --from=webbuilder /src/shared/web/styles.css  /var/www/snet/
COPY --from=webbuilder /src/web-out/app.js         /var/www/snet/

# Nginx config
COPY deploy/docker/nginx.conf /etc/nginx/http.d/default.conf

COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /data

# Web UI (nginx)
EXPOSE 8080
# Web UI HTTPS
EXPOSE 8443
# WireGuard 数据面 (每个网络一个端口，从 52100 起)
EXPOSE 52100-52163/udp

ENTRYPOINT ["/entrypoint.sh"]
