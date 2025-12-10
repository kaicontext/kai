# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copy kai-core first (dependency)
COPY kai-core/ ./kai-core/

# Copy kailab
COPY kailab/ ./kailab/

WORKDIR /build/kailab

# Download dependencies
RUN go mod download

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags '-linkmode external -extldflags "-static"' -o kailabd ./cmd/kailabd

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy the binary
COPY --from=builder /build/kailab/kailabd .

# Create data directory
RUN mkdir -p /data

# Expose port
EXPOSE 7447

# Set environment variables
ENV KAILAB_DATA=/data
ENV KAILAB_LISTEN=:7447

# Run the server
ENTRYPOINT ["./kailabd"]
CMD ["--listen=:7447", "--data=/data"]
