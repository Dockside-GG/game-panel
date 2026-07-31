# syntax=docker/dockerfile:1.12

ARG DOCKSIDE_VERSION=dev
ARG DOCKSIDE_REVISION=unknown
ARG DOCKSIDE_BUILT_AT=unknown

FROM node:24.14.0-alpine AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY web/ ./
RUN DOCKSIDE_WEB_OUT_DIR=dist pnpm build

FROM golang:1.26.5-alpine AS go-build
ARG DOCKSIDE_VERSION
ARG DOCKSIDE_REVISION
ARG DOCKSIDE_BUILT_AT
RUN apk add --no-cache ca-certificates git tzdata
WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web-build /src/web/dist ./internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/dockside-gg/game-panel/internal/buildinfo.Version=${DOCKSIDE_VERSION} -X github.com/dockside-gg/game-panel/internal/buildinfo.Revision=${DOCKSIDE_REVISION} -X github.com/dockside-gg/game-panel/internal/buildinfo.BuiltAt=${DOCKSIDE_BUILT_AT}" \
    -o /out/dockside ./cmd/app
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/dockside-gg/game-panel/internal/buildinfo.Version=${DOCKSIDE_VERSION} -X github.com/dockside-gg/game-panel/internal/buildinfo.Revision=${DOCKSIDE_REVISION} -X github.com/dockside-gg/game-panel/internal/buildinfo.BuiltAt=${DOCKSIDE_BUILT_AT}" \
    -o /out/dockside-worker ./cmd/worker
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/dockside-gg/game-panel/internal/buildinfo.Version=${DOCKSIDE_VERSION} -X github.com/dockside-gg/game-panel/internal/buildinfo.Revision=${DOCKSIDE_REVISION} -X github.com/dockside-gg/game-panel/internal/buildinfo.BuiltAt=${DOCKSIDE_BUILT_AT}" \
    -o /out/dockside-engine ./cmd/engine

FROM alpine:3.22 AS runtime
ARG DOCKSIDE_VERSION
ARG DOCKSIDE_REVISION
ARG DOCKSIDE_BUILT_AT
LABEL org.opencontainers.image.title="Dockside.GG Game Panel" \
      org.opencontainers.image.description="Discord-first, Docker-native game server management" \
      org.opencontainers.image.url="https://github.com/Dockside-GG/game-panel" \
      org.opencontainers.image.source="https://github.com/Dockside-GG/game-panel" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${DOCKSIDE_VERSION}" \
      org.opencontainers.image.revision="${DOCKSIDE_REVISION}" \
      org.opencontainers.image.created="${DOCKSIDE_BUILT_AT}"
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 dockside \
    && adduser -D -H -u 10001 -G dockside dockside
WORKDIR /app

FROM runtime AS app
COPY --from=go-build /out/dockside /dockside
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/dockside"]

FROM runtime AS worker
COPY --from=go-build /out/dockside-worker /dockside-worker
USER 10001:10001
ENTRYPOINT ["/dockside-worker"]

FROM runtime AS engine
COPY --from=go-build /out/dockside-engine /dockside-engine
RUN apk add --no-cache docker-cli docker-cli-compose \
    && mkdir -p /var/lib/dockside/servers /var/lib/dockside/backups \
    && chown -R 10001:10001 /var/lib/dockside
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/dockside-engine"]
