#!/bin/bash
# Test client script for apt-cacher-go testing

set -e

CLIENT_NAME=$1
RESULTS_FILE="/tmp/test-results-${CLIENT_NAME}.txt"

echo "Starting test for $CLIENT_NAME at $(date)" | tee -a $RESULTS_FILE

# Update package lists
echo "Running apt-get update..." | tee -a $RESULTS_FILE
apt-get update | tee -a $RESULTS_FILE

# Verify we're using the proxy
echo "Checking proxy usage..." | tee -a $RESULTS_FILE
APT_CONFIG=$(cat /etc/apt/apt.conf.d/00proxy)
echo "APT configuration: $APT_CONFIG" | tee -a $RESULTS_FILE

# Install packages in the test list
echo "Installing test packages..." | tee -a $RESULTS_FILE
start_time=$(date +%s)

# Read packages from test-packages.txt and install them
xargs -a /tmp/test-packages.txt apt-get install -y --no-install-recommends | tee -a $RESULTS_FILE

end_time=$(date +%s)
elapsed=$((end_time - start_time))

echo "Test completed in $elapsed seconds" | tee -a $RESULTS_FILE
echo "Test for $CLIENT_NAME completed successfully" | tee -a $RESULTS_FILE

# Report installed packages
echo "Installed packages:" | tee -a $RESULTS_FILE
dpkg -l | grep -F "$(cat /tmp/test-packages.txt)" | tee -a $RESULTS_FILE

# Keep the container running for inspection if needed
if [ "${KEEP_RUNNING}" = "true" ]; then
  echo "Keeping container running for inspection..."
  tail -f /dev/null
fi

exit 0
