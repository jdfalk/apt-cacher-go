# apt-cacher-go

A high-performance caching proxy for Debian/Ubuntu package repositories, written
in Go.

## Features

- **High Performance**: Multithreaded architecture with parallel downloads
- **Smart Caching**: Intelligent caching of package and index files with proper
  expiration
- **Access Control**: IP-based restrictions and authentication for
  administrative features
- **HTTPS Support**: Secure package delivery with TLS
- **Repository Mapping**: Sophisticated mapping of request paths to upstream
  repositories
- **Prefetching**: Background prefetching of popular packages
- **Web Interface**: Real-time statistics and administration
- **Docker Support**: Easy deployment with container support
- **Migration Tools**: Simple migration from apt-cacher-ng

## Quick Start

### Using Docker

```bash
docker pull jdfalk/apt-cacher-go:latest
docker run -d --name apt-cacher-go \
  -p 3142:3142 \
  -v apt-cacher-data:/var/cache/apt-cacher-go \
  jdfalk/apt-cacher-go
```

### Installing from Source

```bash
git clone https://github.com/jdfalk/apt-cacher-go.git
cd apt-cacher-go
go build -o apt-cacher-go ./cmd/apt-cacher-go
sudo install -m 755 apt-cacher-go /usr/local/bin/
sudo mkdir -p /etc/apt-cacher-go /var/cache/apt-cacher-go
sudo cp config.yaml /etc/apt-cacher-go/
sudo cp apt-cacher-go.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now apt-cacher-go
```

## Configuration

### Basic Configuration

Here's a simple configuration to get started:

```yaml
# Server configuration
listen_address: '0.0.0.0'
port: 3142

# Cache configuration
cache_dir: '/var/cache/apt-cacher-go'
max_cache_size: 20480 # 20GB

# Security
admin_auth: 'admin:changeMe' # Change this!

# Default backends
backends:
  - name: 'ubuntu-archive'
    url: 'http://archive.ubuntu.com/ubuntu'
    priority: 100
  - name: 'debian'
    url: 'http://deb.debian.org/debian'
    priority: 90
```

### Advanced Configuration

For production environments, consider this more detailed configuration:

```yaml
# Server configuration
listen_address: '0.0.0.0'
port: 3142

# TLS configuration
tls_enabled: true
tls_port: 3143
tls_cert_file: '/etc/apt-cacher-go/cert.pem'
tls_key_file: '/etc/apt-cacher-go/key.pem'

# Cache configuration
cache_dir: '/var/cache/apt-cacher-go'
max_cache_size: 40960 # 40GB
cache_cleanup_interval: 3600 # Cleanup every hour

# Security
allowed_ips:
  - '10.0.0.0/8' # Private network
  - '172.16.0.0/12' # Docker network
  - '192.168.0.0/16' # Home network
admin_auth: 'admin:securePassword'

# Download behavior
max_concurrent_downloads: 20
prefetch_enabled: true
prefetch_limit: 10

# Repository backends
backends:
  - name: 'ubuntu-archive'
    url: 'http://archive.ubuntu.com/ubuntu'
    priority: 100
  - name: 'ubuntu-security'
    url: 'http://security.ubuntu.com/ubuntu'
    priority: 95
  - name: 'debian'
    url: 'http://deb.debian.org/debian'
    priority: 90
  - name: 'debian-security'
    url: 'http://security.debian.org/debian-security'
    priority: 85
  - name: 'debian-backports'
    url: 'http://deb.debian.org/debian-backports'
    priority: 80
  - name: 'kali'
    url: 'http://http.kali.org/kali'
    priority: 70
```

### Configuration Options Reference

| Option                     | Description                   | Default                    |
| -------------------------- | ----------------------------- | -------------------------- |
| `listen_address`           | IP address to listen on       | "0.0.0.0"                  |
| `port`                     | HTTP port                     | 3142                       |
| `tls_enabled`              | Enable HTTPS support          | false                      |
| `tls_port`                 | HTTPS port                    | 3143                       |
| `tls_cert_file`            | Path to TLS certificate       | ""                         |
| `tls_key_file`             | Path to TLS private key       | ""                         |
| `cache_dir`                | Cache storage directory       | "/var/cache/apt-cacher-go" |
| `max_cache_size`           | Maximum cache size (MB)       | 40960                      |
| `cache_cleanup_interval`   | Seconds between cleanups      | 3600                       |
| `allowed_ips`              | List of allowed client CIDRs  | ["0.0.0.0/0"]              |
| `admin_auth`               | Admin credentials (user:pass) | "admin:admin"              |
| `max_concurrent_downloads` | Parallel download limit       | 10                         |
| `prefetch_enabled`         | Enable background prefetching | true                       |
| `prefetch_limit`           | Maximum prefetch connections  | 5                          |
| `backends`                 | Repository backend list       | (see example)              |

## Setting Up APT Clients

### Automatic Configuration

Use our client configuration script:

```bash
sudo ./scripts/configure-client.sh --host proxy.example.com
```

For HTTPS:

```bash
sudo ./scripts/configure-client.sh --host proxy.example.com --use-tls
```

### Manual Configuration

Create a file at `/etc/apt/apt.conf.d/00proxy` with:

```
// HTTP proxy
Acquire::http::Proxy "http://your-proxy-server:3142";
// HTTPS proxy (if TLS is enabled)
Acquire::https::Proxy "https://your-proxy-server:3143";
```

## Security Considerations

### Access Control

By default, apt-cacher-go allows connections from any IP address. For production
use, we strongly recommend limiting access:

```yaml
allowed_ips:
  - '10.0.0.0/8' # Internal network
  - '127.0.0.1/8' # Localhost
```

### TLS Setup

For secure connections, generate a certificate:

```bash
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/apt-cacher-go/key.pem \
  -out /etc/apt-cacher-go/cert.pem
```

For production deployments, consider using Let's Encrypt for valid certificates.

### Admin Authentication

Always change the default admin password:

```yaml
admin_auth: 'admin:your-secure-password'
```

## Administration

### Web Interface

Access the admin dashboard at `http://your-server:3142/admin` using your
configured credentials.

The interface provides:

- Real-time cache statistics
- Request history and hit rates
- Top packages and clients
- Cache management tools

### Cache Management

The following admin actions are available:

- **Clear Cache**: Remove all cached files
- **Flush Expired**: Remove only expired files
- **Search Cache**: Find specific packages

## Benchmarking

Compare performance with apt-cacher-ng using the benchmark tool:

```bash
go run cmd/benchmark/main.go --proxy http://localhost:3142 --concurrency 20 --iterations 200
```

Sample output:

```
=== BENCHMARK SUMMARY ===
Total requests: 1000 (Success: 998, Failed: 2)
Total data transferred: 1256.72 MB
Overall average throughput: 82.45 MB/s
```

## Migrating from apt-cacher-ng

Our migration tool makes it easy to switch from apt-cacher-ng:

```bash
sudo ./scripts/migrate-from-acng.sh
```

The script will:

1. Stop apt-cacher-ng service
2. Copy cache files to apt-cacher-go's location
3. Convert configuration settings
4. Set up apt-cacher-go's configuration file

## Docker Deployment

### Basic Usage

```bash
docker run -d --name apt-cacher-go -p 3142:3142 jdfalk/apt-cacher-go
```

### Persistent Cache

```bash
docker run -d --name apt-cacher-go \
  -p 3142:3142 \
  -v /path/on/host:/var/cache/apt-cacher-go \
  jdfalk/apt-cacher-go
```

### Custom Configuration

```bash
docker run -d --name apt-cacher-go \
  -p 3142:3142 \
  -v /path/to/config.yaml:/etc/apt-cacher-go/config.yaml \
  -v apt-cache-vol:/var/cache/apt-cacher-go \
  jdfalk/apt-cacher-go
```

### Docker Compose Example

```yaml
version: '3.8'

services:
  apt-cacher-go:
    image: jdfalk/apt-cacher-go:latest
    container_name: apt-cacher-go
    ports:
      - '3142:3142'
      - '3143:3143' # For HTTPS if enabled
    volumes:
      - apt-cache-data:/var/cache/apt-cacher-go
      - ./config.yaml:/etc/apt-cacher-go/config.yaml:ro
      - ./certs:/etc/apt-cacher-go/certs:ro # Optional, for TLS
    restart: unless-stopped
    environment:
      - TZ=UTC

volumes:
  apt-cache-data:
    driver: local
```

## Advanced Topics

### Repository Path Mapping

apt-cacher-go intelligently maps request paths to backends using a series of
rules. You can customize these in the source code if needed.

### Prefetching Logic

When an index file is downloaded, apt-cacher-go analyzes it to identify popular
packages and prefetches them in the background, improving subsequent client
request performance.

### Adding Custom Repositories

For specialized repositories, add them to your configuration:

```yaml
backends:
  # ... default backends ...
  - name: 'custom-repo'
    url: 'http://packages.example.com/debian'
    priority: 60
```

## Troubleshooting

### Common Issues

1. **Connection Refused**: Check that the server is running and the port is
   accessible
2. **Unauthorized Errors**: Ensure your client IP is in the allowed_ips list
3. **Package Not Found**: Verify the backend repositories are correctly
   configured
4. **Slow Downloads**: Check max_concurrent_downloads setting and network
   connectivity

### Logs

apt-cacher-go logs to stdout/stderr by default. When running as a systemd
service, check logs with:

```bash
journalctl -u apt-cacher-go
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT
