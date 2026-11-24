# Stage 1: Build static Go binary
FROM golang:1.26.4-alpine AS builder

WORKDIR /src

# Cache Go modules before copying application source
COPY go.mod go.sum ./
RUN go mod download

# Copy application source and compile a static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/rate-limiter ./cmd/webapp

# Stage 2: Minimal non-root runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S appgroup \
    && adduser -S -G appgroup appuser

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /out/rate-limiter /app/rate-limiter

USER appuser

ENV APP_PORT=8080
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null -T 2 "http://127.0.0.1:${APP_PORT}/health" || exit 1

ENTRYPOINT ["/app/rate-limiter"]
