# syntax=docker/dockerfile:1.12

FROM node:24.14.0-alpine AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile=false
COPY web/ ./
RUN DOCKSIDE_WEB_OUT_DIR=dist pnpm build

FROM golang:1.26.5-alpine AS go-build
RUN apk add --no-cache ca-certificates git tzdata
WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web-build /src/web/dist ./internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dockside ./cmd/app
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dockside-worker ./cmd/worker
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dockside-engine ./cmd/engine

FROM alpine:3.22 AS runtime
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
RUN mkdir -p /var/lib/dockside/servers /var/lib/dockside/backups \
    && chown -R 10001:10001 /var/lib/dockside
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/dockside-engine"]
