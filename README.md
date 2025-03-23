# apt-cacher-go

A high-performance caching proxy for Debian/Ubuntu package repositories, written in Go.

## Features

- Package caching for Debian, Ubuntu and other APT-based distributions
- Efficient parallel downloads with priority queue
- Smart prefetching of popular packages
- Configurable access control
- Web-based statistics and administration interface
- HTTPS/TLS support
- Docker container support

## Quick Start

### Using Docker

```bash
docker pull jdfalk/apt-cacher-go:latest
docker run -d --name apt-cacher-go -p 3142:3142 -v apt-cacher-data:/var/cache/apt-cacher-go jdfalk/apt-cacher-go
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

### Migrating from apt-cacher-ng

If you're migrating from apt-cacher-ng, you can use our migration script:

```bash
sudo ./scripts/migrate-from-acng.sh
```

The script will:
1. Stop apt-cacher-ng
2. Copy the cache files to apt-cacher-go's cache directory
3. Create a compatible configuration file
4. Help you set up apt-cacher-go

## Configuration

Edit `/etc/apt-cacher-go/config.yaml` to customize the behavior:

```yaml
# Server configuration
listen_address: "0.0.0.0"
port: 3142

# TLS configuration
tls_enabled: false
tls_port: 3143
tls_cert_file: "/etc/apt-cacher-go/cert.pem"
tls_key_file: "/etc/apt-cacher-go/key.pem"

# Cache configuration
cache_dir: "/var/cache/apt-cacher-go"
max_cache_size: 40960  # 40GB

# Security
allowed_ips:
  - "10.0.0.0/8"
  - "172.16.0.0/12"
  - "192.168.0.0/16"
admin_auth: "admin:password"  # Change this!

# Download queue
max_concurrent_downloads: 10

# Repository backends
backends:
  - name: "ubuntu-archive"
    url: "http://archive.ubuntu.com/ubuntu"
    priority: 100
  - name: "ubuntu-security"
    url: "http://security.ubuntu.com/ubuntu"
    priority: 90
  - name: "debian"
    url: "http://deb.debian.org/debian"
    priority: 80
  - name: "debian-security"
    url: "http://security.debian.org/debian-security"
    priority: 70
```

### Configuration Options

| Option                     | Description                                  | Default                    |
| -------------------------- | -------------------------------------------- | -------------------------- |
| `listen_address`           | IP address to listen on                      | "0.0.0.0"                  |
| `port`                     | HTTP port to listen on                       | 3142                       |
| `tls_enabled`              | Enable HTTPS support                         | false                      |
| `tls_port`                 | HTTPS port to listen on                      | 3143                       |
| `tls_cert_file`            | Path to TLS certificate file                 | ""                         |
| `tls_key_file`             | Path to TLS private key file                 | ""                         |
| `cache_dir`                | Directory to store cached files              | "/var/cache/apt-cacher-go" |
| `max_cache_size`           | Maximum cache size in MB                     | 40960                      |
| `allowed_ips`              | List of CIDR notation IPs allowed to connect | ["0.0.0.0/0"]              |
| `admin_auth`               | Admin username:password                      | "admin:password"           |
| `max_concurrent_downloads` | Maximum parallel downloads                   | 10                         |
| `backends`                 | List of repository backends                  | See example                |

### Setting up TLS

To enable HTTPS support, generate a self-signed certificate:

```bash
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/apt-cacher-go/key.pem \
  -out /etc/apt-cacher-go/cert.pem
```

Then update your configuration:

```yaml
tls_enabled: true
tls_port: 3143
tls_cert_file: "/etc/apt-cacher-go/cert.pem"
tls_key_file: "/etc/apt-cacher-go/key.pem"
```

## Client Setup

### Automatic Configuration

Use our client configuration script:

```bash
sudo ./scripts/configure-client.sh --host proxy.example.com
```

### Manual Configuration

On your APT clients, create a file at `/etc/apt/apt.conf.d/00proxy` with:

```
# For HTTP
Acquire::http::Proxy "http://your-proxy-server:3142";

# For HTTPS (if TLS is enabled)
Acquire::https::Proxy "https://your-proxy-server:3143";
```

## Administration

Access the admin interface at `http://your-server:3142/admin` using the credentials in your config file.

The admin interface provides:
- Cache usage statistics
- Request statistics and hit rate
- Popular packages list
- Top clients list
- Cache management tools

## Performance Benchmarking

To compare performance with apt-cacher-ng, use the included benchmark tool:

```bash
go run cmd/benchmark/main.go --proxy http://localhost:3142 --concurrency 20 --iterations 500
```

## Architecture

apt-cacher-go is designed with a modular architecture:

- **Cache**: Implements efficient file storage with expiration policies
- **Backend**: Handles package downloading from upstream repositories
- **Mapper**: Maps request paths to appropriate backends
- **Parser**: Processes repository metadata files
- **Queue**: Manages parallel downloads with priority
- **Security**: Implements access control
- **Metrics**: Collects and reports usage statistics
- **Server**: Handles HTTP/HTTPS and admin interface

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

GPL 3
