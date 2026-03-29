FROM golang:1.23-alpine AS builder

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gda-cleaner .

# Runtime image
FROM alpine:3.20

# ca-certificates needed for TLS connections to AWS and SMTP servers
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /gda-cleaner /usr/local/bin/gda-cleaner

# Non-root user for security
RUN adduser -D -H appuser
USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["gda-cleaner"]
