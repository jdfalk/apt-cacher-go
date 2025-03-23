#!/bin/bash
# Script to configure an APT client to use apt-cacher-go

set -e

# Default values
PROXY_HOST="localhost"
PROXY_PORT=3142
PROXY_TLS_PORT=3143
USE_TLS=false

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -h|--host)
      PROXY_HOST="$2"
      shift 2
      ;;
    -p|--port)
      PROXY_PORT="$2"
      shift 2
      ;;
    -s|--tls-port)
      PROXY_TLS_PORT="$2"
      shift 2
      ;;
    -t|--use-tls)
      USE_TLS=true
      shift
      ;;
    --help)
      echo "Usage: $0 [options]"
      echo "Options:"
      echo "  -h, --host HOST      Proxy hostname (default: localhost)"
      echo "  -p, --port PORT      Proxy HTTP port (default: 3142)"
      echo "  -s, --tls-port PORT  Proxy HTTPS port (default: 3143)"
      echo "  -t, --use-tls        Configure to use HTTPS/TLS"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Check if we're running as root
if [ "$EUID" -ne 0 ]; then
  echo "This script should be run as root to configure APT. Please re-run with sudo."
  exit 1
fi

# Proxy configuration file path
APT_PROXY_CONF="/etc/apt/apt.conf.d/00proxy"

# Create backup if file exists
if [ -f "$APT_PROXY_CONF" ]; then
  echo "Backing up existing proxy configuration..."
  cp "$APT_PROXY_CONF" "$APT_PROXY_CONF.bak"
fi

# Write new configuration
echo "Creating APT proxy configuration file at $APT_PROXY_CONF..."

if [ "$USE_TLS" = true ]; then
  cat > "$APT_PROXY_CONF" << EOF
// Proxy configuration for apt-cacher-go (HTTPS)
Acquire::http::Proxy "http://$PROXY_HOST:$PROXY_PORT";
Acquire::https::Proxy "https://$PROXY_HOST:$PROXY_TLS_PORT";
EOF
else
  cat > "$APT_PROXY_CONF" << EOF
// Proxy configuration for apt-cacher-go (HTTP)
Acquire::http::Proxy "http://$PROXY_HOST:$PROXY_PORT";
EOF
fi

# Set permissions
chmod 644 "$APT_PROXY_CONF"

echo "APT client configuration complete!"
echo "Your system will now use apt-cacher-go at $PROXY_HOST for package downloads."

# Optionally update APT to verify it works
echo -n "Would you like to test the configuration with 'apt update'? (y/n) "
read -r response
if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
  echo "Running apt update to test configuration..."
  apt update
  echo "Configuration test completed."
fi
