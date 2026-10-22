# Image for the GitHub Action: Go review pipeline + Node engine CLI in one container.
FROM golang:1.26 AS build
WORKDIR /src
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download
COPY gateway/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/action ./cmd/action

# Debian-based: @cursor/sdk ships glibc-only native binaries.
FROM node:24-slim
WORKDIR /app/engine
COPY engine/package.json engine/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY engine/src ./src
# node:*-slim has no system CA bundle (Node ships its own); the Go binary needs one for TLS.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/action /app/action
ENV ENGINE_COMMAND="node /app/engine/src/cli.js"
ENTRYPOINT ["/app/action"]
