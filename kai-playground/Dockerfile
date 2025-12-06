# Multi-stage build for Kai Playground

# Stage 1: Build kai binary
FROM golang:1.24-alpine AS kai-builder
# Install build dependencies for CGO (tree-sitter requires C compiler)
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY kai-cli/ ./kai-cli/
COPY kai-core/ ./kai-core/
WORKDIR /build/kai-cli
ENV CGO_ENABLED=1
RUN go build -o kai ./cmd/kai

# Stage 2: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /build
COPY kai-playground/frontend/ ./
RUN npm install && npm run build

# Stage 3: Build backend
FROM golang:1.24-alpine AS backend-builder
WORKDIR /build
COPY kai-playground/backend/ ./
RUN go build -o server .

# Stage 4: Final image
FROM alpine:3.19

# Install runtime dependencies including Node.js for running tests
RUN apk add --no-cache ca-certificates tree nodejs npm

WORKDIR /app

# Copy binaries
COPY --from=kai-builder /build/kai-cli/kai /usr/local/bin/kai
COPY --from=backend-builder /build/server /app/server
COPY --from=frontend-builder /build/dist /app/frontend/dist

# Set environment
ENV KAI_BIN=/usr/local/bin/kai
ENV STATIC_DIR=/app/frontend/dist
ENV PORT=8080

EXPOSE 8080

CMD ["/app/server"]
