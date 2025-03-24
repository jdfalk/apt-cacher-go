<!-- file: docs/deployment_guide.md -->

# apt-cacher-go Deployment Guide

This guide covers various deployment options and configurations for apt-cacher-go.

## System Requirements

- Go 1.18 or higher (for building)
- Linux-based OS (recommended)
- 512MB RAM minimum (2GB+ recommended for high-traffic deployments)
- Sufficient disk space for package cache (10GB+ recommended)

## Installation Options

### From Source

1. Clone the repository:
   ```
   git clone https://github.com/jdfalk/apt-cacher-go.git
   cd apt-cacher-go
   ```

2. Build the binary:
   ```
   go build -o apt-cacher-go cmd/main.go
   ```

3. Install to system:
   ```
   sudo mv apt-cacher-go /usr/local/bin/
   sudo mkdir -p /var/cache/apt-cacher-go
   sudo mkdir -p /etc/apt-cacher-go
   ```

4. Create configuration:
   ```
   sudo cp examples/config.yaml /etc/apt-cacher-go/config.yaml
   ```

### Using Docker

1. Build Docker image:
   ```
   docker build -t apt-cacher-go .
   ```

2. Run container:
   ```
   docker run -d --name apt-cacher-go \
     -p 3142:3142 \
     -v apt-cacher-data:/var/cache/apt-cacher-go \
     apt-cacher-go
   ```

## Configuration

### Basic Configuration

Create a file at `/etc/apt-cacher-go/config.yaml`:

```yaml
cache_dir: /var/cache/apt-cacher-go
listen_address: 0.0.0.0
port: 3142
max_cache_size: 10GB
log_level: info
```

### Advanced Configuration

For more advanced setup:

```yaml
cache_dir: /var/cache/apt-cacher-go
listen_address: 0.0.0.0
port: 3142
max_cache_size: 10GB
log_level: info

# TLS configuration
enable_tls: true
tls_cert_file: /etc/apt-cacher-go/cert.pem
tls_key_file: /etc/apt-cacher-go/key.pem
tls_port: 3143

# Repository mapping rules
mapping_rules:
  - type: regex
    pattern: "(security|archive).ubuntu.com/ubuntu"
    repository: ubuntu
    priority: 100

  - type: prefix
    pattern: "debian.org"
    repository: debian
    priority: 50

  - type: rewrite
    pattern: "ppa.launchpadcontent.net/([^/]+)/([^/]+)"
    repository: ppa/%1/%2
    rewrite_rule: "ppa/%1/%2"
    priority: 75

# Access control
allowed_ips:
  - 127.0.0.1
  - 192.168.0.0/24
  - 10.0.0.0/8

# Cache settings
cache_ttl:
  index: 1h
  package: 30d
  default: 7d

# Prefetch settings
enable_prefetch: true
prefetch_concurrency: 2
prefetch_delay: 5m
```

## Service Setup

### Systemd Service

Create a file at `/etc/systemd/system/apt-cacher-go.service`:

```ini
[Unit]
Description=APT Cacher Go
After=network.target

[Service]
ExecStart=/usr/local/bin/apt-cacher-go serve
Restart=always
User=apt-cacher
Group=apt-cacher
Environment=APT_CACHER_CONFIG_FILE=/etc/apt-cacher-go/config.yaml

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```
sudo systemctl enable apt-cacher-go
sudo systemctl start apt-cacher-go
```

## Client Configuration

### APT Client Setup

Configure Debian/Ubuntu systems to use apt-cacher-go by creating a file at `/etc/apt/apt.conf.d/01proxy`:

```
Acquire::http::Proxy "http://apt-cacher-server:3142";
Acquire::https::Proxy "http://apt-cacher-server:3142";
```

Replace `apt-cacher-server` with the hostname or IP address of your apt-cacher-go server.

### Docker APT Caching

For Docker builds, add to your Dockerfile:

```dockerfile
RUN echo 'Acquire::http::Proxy "http://apt-cacher-server:3142";' > /etc/apt/apt.conf.d/01proxy && \
    echo 'Acquire::https::Proxy "http://apt-cacher-server:3142";' >> /etc/apt/apt.conf.d/01proxy
```

## Monitoring

Access the built-in monitoring tools:

- Admin interface: http://apt-cacher-server:3142/admin
- Health check: http://apt-cacher-server:3142/health
- Prometheus metrics: http://apt-cacher-server:3142/metrics
