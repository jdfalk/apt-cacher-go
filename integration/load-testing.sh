#!/bin/bash
# Load test script for apt-cacher-go

set -e

CONCURRENCY=10
ITERATIONS=5
PROXY_URL="http://apt-cacher-go:3142"
RESULTS_FILE="/tmp/load-test-results.txt"

echo "Starting load test at $(date)" | tee -a $RESULTS_FILE
echo "Proxy URL: $PROXY_URL" | tee -a $RESULTS_FILE
echo "Concurrency: $CONCURRENCY" | tee -a $RESULTS_FILE
echo "Iterations: $ITERATIONS" | tee -a $RESULTS_FILE

# Common package URLs to test
declare -a TEST_URLS=(
  "/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb"
  "/ubuntu/pool/main/p/python3.10/python3.10_3.10.6-1~22.04_amd64.deb"
  "/ubuntu/pool/main/g/gcc-11/gcc-11_11.3.0-1ubuntu1~22.04_amd64.deb"
  "/debian/pool/main/n/nginx/nginx_1.22.1-9_amd64.deb"
  "/debian/pool/main/o/openssl/openssl_3.0.9-1_amd64.deb"
)

# Function to download a URL and time it
download_url() {
  local url=$1
  local iteration=$2
  local start_time=$(date +%s.%N)

  # Use curl to download the file and discard the output
  curl -s -o /dev/null "$PROXY_URL$url"
  local status=$?

  local end_time=$(date +%s.%N)
  local elapsed=$(echo "$end_time - $start_time" | bc)

  echo "$url,$iteration,$status,$elapsed" >> $RESULTS_FILE
}

# Clear the results file
echo "URL,Iteration,Status,Time" > $RESULTS_FILE

# First pass - prime the cache
echo "Priming the cache..." | tee -a $RESULTS_FILE
for url in "${TEST_URLS[@]}"; do
  curl -s -o /dev/null "$PROXY_URL$url"
  echo "Primed: $url" | tee -a $RESULTS_FILE
done

# Wait a moment for the cache to settle
sleep 2

# Run load test
echo "Running load test..." | tee -a $RESULTS_FILE
for ((i=1; i<=$ITERATIONS; i++)); do
  echo "Iteration $i" | tee -a $RESULTS_FILE

  # Launch parallel downloads
  pids=()
  for url in "${TEST_URLS[@]}"; do
    for ((j=1; j<=$CONCURRENCY; j++)); do
      download_url "$url" "$i-$j" &
      pids+=($!)
    done
  done

  # Wait for all downloads to complete
  for pid in "${pids[@]}"; do
    wait $pid
  done
done

# Calculate summary statistics
echo -e "\nSummary:" | tee -a $RESULTS_FILE
echo "Total requests: $((${#TEST_URLS[@]} * CONCURRENCY * ITERATIONS))" | tee -a $RESULTS_FILE

# Calculate average time per URL
echo -e "\nAverage download time per URL:" | tee -a $RESULTS_FILE
for url in "${TEST_URLS[@]}"; do
  avg_time=$(grep "$url" $RESULTS_FILE | awk -F',' '{sum+=$4; count++} END {print sum/count}')
  echo "$url: $avg_time seconds" | tee -a $RESULTS_FILE
done

echo "Load test completed at $(date)" | tee -a $RESULTS_FILE
