#!/bin/bash
# Script to migrate from apt-cacher-ng to apt-cacher-go

set -e

# Default paths
ACNG_CACHE_DIR="/var/cache/apt-cacher-ng"
ACGO_CACHE_DIR="/var/cache/apt-cacher-go"
ACNG_CONF="/etc/apt-cacher-ng/acng.conf"
ACGO_CONF="/etc/apt-cacher-go/config.yaml"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --acng-cache=*)
      ACNG_CACHE_DIR="${1#*=}"
      shift
      ;;
    --acng-conf=*)
      ACNG_CONF="${1#*=}"
      shift
      ;;
    --acgo-cache=*)
      ACGO_CACHE_DIR="${1#*=}"
      shift
      ;;
    --acgo-conf=*)
      ACGO_CONF="${1#*=}"
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  --acng-cache=DIR   Set apt-cacher-ng cache directory (default: $ACNG_CACHE_DIR)"
      echo "  --acng-conf=FILE   Set apt-cacher-ng config file (default: $ACNG_CONF)"
      echo "  --acgo-cache=DIR   Set apt-cacher-go cache directory (default: $ACGO_CACHE_DIR)"
      echo "  --acgo-conf=FILE   Set apt-cacher-go config file (default: $ACGO_CONF)"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Check if apt-cacher-ng is installed
if ! dpkg -s apt-cacher-ng &>/dev/null; then
  echo "apt-cacher-ng is not installed. Nothing to migrate."
  exit 0
fi

echo "Starting migration from apt-cacher-ng to apt-cacher-go..."

# Ensure apt-cacher-ng is stopped
echo "Stopping apt-cacher-ng..."
systemctl stop apt-cacher-ng || true

# Create cache directory if it doesn't exist
echo "Creating cache directory: $ACGO_CACHE_DIR"
mkdir -p "$ACGO_CACHE_DIR"

# Copy cache files
echo "Copying cache files from $ACNG_CACHE_DIR to $ACGO_CACHE_DIR..."
rsync -a "$ACNG_CACHE_DIR/" "$ACGO_CACHE_DIR/"

# Extract configuration from apt-cacher-ng
echo "Extracting configuration from apt-cacher-ng..."

# Parse apt-cacher-ng configuration
PORT=$(grep -oP '^Port:\s*\K\d+' "$ACNG_CONF" || echo "3142")
BIND_ADDRESS=$(grep -oP '^BindAddress:\s*\K.*' "$ACNG_CONF" || echo "0.0.0.0")
MAX_SIZE=$(grep -oP '^CacheLimit:\s*\K\d+' "$ACNG_CONF" || echo "40960")
ALLOWED_IPS=$(grep -oP '^Allowed:\s*\K.*' "$ACNG_CONF" || echo "")
ADMIN_AUTH=$(grep -oP '^AdminAuth:\s*\K.*' "$ACNG_CONF" || echo "")

# Create apt-cacher-go configuration
echo "Creating apt-cacher-go configuration..."

# Make sure the directory exists
mkdir -p "$(dirname "$ACGO_CONF")"

cat > "$ACGO_CONF" <<EOL
# Configuration migrated from apt-cacher-ng
listen_address: "$BIND_ADDRESS"
port: $PORT

# Cache configuration
cache_dir: "$ACGO_CACHE_DIR"
max_cache_size: $MAX_SIZE

# Security
allowed_ips:
EOL

# Add allowed IPs
if [ -n "$ALLOWED_IPS" ]; then
  for ip in $ALLOWED_IPS; do
    echo "  - \"$ip\"" >> "$ACGO_CONF"
  done
else
  echo "  - \"0.0.0.0/0\"" >> "$ACGO_CONF"
fi

# Add admin auth
if [ -n "$ADMIN_AUTH" ]; then
  echo "admin_auth: \"$ADMIN_AUTH\"" >> "$ACGO_CONF"
else
  echo "admin_auth: \"admin:admin\"  # Default password - CHANGE THIS!" >> "$ACGO_CONF"
fi

# Add download queue configuration
echo -e "\n# Download queue\nmax_concurrent_downloads: 10\n" >> "$ACGO_CONF"

# Add backend repositories
echo -e "# Repository backends\nbackends:" >> "$ACGO_CONF"
echo "  - name: \"ubuntu-archive\"" >> "$ACGO_CONF"
echo "    url: \"http://archive.ubuntu.com/ubuntu\"" >> "$ACGO_CONF"
echo "    priority: 100" >> "$ACGO_CONF"
echo "  - name: \"ubuntu-security\"" >> "$ACGO_CONF"
echo "    url: \"http://security.ubuntu.com/ubuntu\"" >> "$ACGO_CONF"
echo "    priority: 90" >> "$ACGO_CONF"
echo "  - name: \"debian\"" >> "$ACGO_CONF"
echo "    url: \"http://deb.debian.org/debian\"" >> "$ACGO_CONF"
echo "    priority: 80" >> "$ACGO_CONF"
echo "  - name: \"debian-security\"" >> "$ACGO_CONF"
echo "    url: \"http://security.debian.org/debian-security\"" >> "$ACGO_CONF"
echo "    priority: 70" >> "$ACGO_CONF"

echo "Migration completed successfully!"
echo "Configuration has been written to $ACGO_CONF"
echo "You can now install and start apt-cacher-go"
