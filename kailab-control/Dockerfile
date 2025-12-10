# Build frontend stage
FROM node:22-alpine AS frontend

WORKDIR /build/frontend

# Copy frontend source
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci

COPY frontend/ ./

# Build the SvelteKit app (outputs to ../internal/api/web)
RUN mkdir -p ../internal/api/web && npm run build

# Build Go binary stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Copy built frontend from previous stage
COPY --from=frontend /build/internal/api/web ./internal/api/web

# Build the binary (no CGO needed - uses pure Go SQLite)
RUN CGO_ENABLED=0 GOOS=linux go build -o kailab-control ./cmd/kailab-control

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy the binary
COPY --from=builder /build/kailab-control .

# Create data directory
RUN mkdir -p /data

# Expose port
EXPOSE 8080

# Run the server (config via env vars: KLC_LISTEN, KLC_DB_URL, etc.)
ENTRYPOINT ["./kailab-control"]
