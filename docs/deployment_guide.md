<!-- file: docs/deployment_guide.md -->

# apt-cacher-go Deployment Guide

This guide covers various deployment options and configurations for
apt-cacher-go.

## System Requirements

- Go 1.18 or higher (for building)
- Linux-based OS (recommended)
- 512MB RAM minimum (2GB+ recommended for high-traffic deployments)
- Sufficient disk space for package cache (10GB+ recommended)
- A compatible filesystem with proper permission support (ext4 recommended)

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

3. For HTTPS support:

   ```
   docker run -d --name apt-cacher-go \
     -p 3142:3142 \
     -p 3143:3143 \
     -v apt-cacher-data:/var/cache/apt-cacher-go \
     -v ./certs:/etc/apt-cacher-go/certs \
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
admin_port: 9283
max_cache_size: 10GB
log_level: info

# TLS configuration
tls_enabled: true
tls_cert: /etc/apt-cacher-go/cert.pem
tls_key: /etc/apt-cacher-go/key.pem
tls_port: 3143

# Memory management
memory_high_watermark: 1024 # MB, when to start GC
memory_critical_watermark: 1536 # MB, when to take emergency action
memory_check_interval: 30s

# Repository mapping rules
mapping_rules:
  - type: regex
    pattern: '(security|archive).ubuntu.com/ubuntu'
    repository: ubuntu
    priority: 100

  - type: prefix
    pattern: 'debian.org'
    repository: debian
    priority: 50

  - type: rewrite
    pattern: 'ppa.launchpadcontent.net/([^/]+)/([^/]+)'
    repository: ppa/%1/%2
    rewrite_rule: 'ppa/%1/%2'
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
prefetch:
  enabled: true
  max_concurrent: 5
  architectures:
    - amd64
    - arm64
  warmup_on_startup: true
  batch_size: 10
  verbose_logging: false

# Key management for repository signatures
key_management:
  enabled: true
  auto_retrieve: true
  key_ttl: 365d
  keyservers:
    - hkp://keyserver.ubuntu.com
  key_dir: /var/cache/apt-cacher-go/keys
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

# Memory limits
MemoryHigh=1536M
MemoryMax=2048M

# File limits
LimitNOFILE=65535

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

Configure Debian/Ubuntu systems to use apt-cacher-go by creating a file at
`/etc/apt/apt.conf.d/01proxy`:

```
Acquire::http::Proxy "http://apt-cacher-server:3142";
Acquire::https::Proxy "http://apt-cacher-server:3142";
```

Replace `apt-cacher-server` with the hostname or IP address of your
apt-cacher-go server.

### Docker APT Caching

For Docker builds, add to your Dockerfile:

```dockerfile
RUN echo 'Acquire::http::Proxy "http://apt-cacher-server:3142";' > /etc/apt/apt.conf.d/01proxy && \
    echo 'Acquire::https::Proxy "http://apt-cacher-server:3142";' >> /etc/apt/apt.conf.d/01proxy
```

## HTTPS Configuration

To enable HTTPS support:

1. Generate or obtain TLS certificates:

   ```
   openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
     -keyout /etc/apt-cacher-go/key.pem \
     -out /etc/apt-cacher-go/cert.pem
   ```

2. Update configuration:

   ```yaml
   tls_enabled: true
   tls_cert: /etc/apt-cacher-go/cert.pem
   tls_key: /etc/apt-cacher-go/key.pem
   tls_port: 3143
   ```

3. Configure clients to use HTTPS:
   ```
   Acquire::https::Proxy "https://apt-cacher-server:3143";
   ```

## Memory Optimization

Apt-cacher-go includes memory monitoring that can be tuned:

1. Set appropriate watermarks based on system memory:

   ```yaml
   memory_high_watermark: 1024 # MB, when to start GC
   memory_critical_watermark: 1536 # MB, when to throttle operations
   ```

2. Limit prefetch concurrency on memory-constrained systems:

   ```yaml
   prefetch:
     enabled: true
     max_concurrent: 3 # Lower for less memory usage
   ```

3. Add systemd memory limits for additional protection:
   ```ini
   [Service]
   MemoryHigh=1536M
   MemoryMax=2048M
   ```

## Monitoring

Access the built-in monitoring tools:

- Admin interface: <http://apt-cacher-server:3142/admin>
- Health check: <http://apt-cacher-server:3142/health>
- Detailed health: <http://apt-cacher-server:3142/health?detailed=true>
- Prometheus metrics: <http://apt-cacher-server:3142/metrics>
- Readiness check: <http://apt-cacher-server:3142/ready>

## Security Considerations

1. Configure access control to limit which clients can use the proxy:

   ```yaml
   allowed_ips:
     - 127.0.0.1
     - 192.168.0.0/24 # Internal network
   ```

2. Enable admin authentication to secure the admin interface:

   ```yaml
   admin_auth: true
   username: admin
   password: secure_password
   ```

3. If exposing to untrusted networks, use a reverse proxy with TLS termination.

4. Consider using a dedicated user with limited permissions for the service.
