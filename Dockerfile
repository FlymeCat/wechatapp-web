# syntax=docker/dockerfile:1

# ============================================================================
# Stage 1: build the Go binary
# ============================================================================
FROM golang:1.22-alpine AS builder

# Module proxy override (helpful in restricted networks).
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a static binary.
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/wechatapp-server ./cmd/server

# ============================================================================
# Stage 2: minimal runtime image
# ============================================================================
FROM alpine:3.20

# CA certificates for outbound HTTPS calls to api.remove.bg, plus a non-root user.
RUN apk add --no-cache ca-certificates \
    && addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/wechatapp-server /app/wechatapp-server

USER app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/wechatapp-server"]
