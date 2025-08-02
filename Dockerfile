FROM golang:1.24-alpine AS builder

# Set working directory
WORKDIR /app

# Copy module files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 go build -o /bin/apt-cacher-go ./cmd/apt-cacher-go

# Create runtime image
FROM alpine:latest

# Install CA certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create directories
RUN mkdir -p /etc/apt-cacher-go /var/cache/apt-cacher-go

# Copy the binary
COPY --from=builder /bin/apt-cacher-go /bin/apt-cacher-go

# Copy default config
COPY config.yaml /etc/apt-cacher-go/config.yaml

# Expose port
EXPOSE 3142

# Set cache directory as a volume
VOLUME /var/cache/apt-cacher-go

# Run the binary
ENTRYPOINT ["/bin/apt-cacher-go", "--config", "/etc/apt-cacher-go/config.yaml"]
