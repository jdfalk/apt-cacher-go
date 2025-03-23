#!/bin/bash
# Master script to run all integration tests for apt-cacher-go

set -e

echo "============================================="
echo "  apt-cacher-go Integration Test Suite"
echo "============================================="

# Setup
TEST_DIR=$(dirname "$(readlink -f "$0")")
cd "$TEST_DIR"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Clean up from previous runs
echo -e "${YELLOW}Cleaning up from previous test runs...${NC}"
docker-compose down -v 2>/dev/null || true
rm -f /tmp/test-results-*.txt

# Run Go tests
echo -e "${YELLOW}Running Go integration tests...${NC}"
cd ..
go test -v ./integration/...
TEST_EXIT=$?

if [ $TEST_EXIT -ne 0 ]; then
  echo -e "${RED}Go integration tests failed!${NC}"
  exit $TEST_EXIT
else
  echo -e "${GREEN}Go integration tests passed!${NC}"
fi

# Return to integration directory
cd "$TEST_DIR"

# Run Docker-based tests
echo -e "${YELLOW}Building and starting Docker integration test environment...${NC}"
docker-compose build
docker-compose up --abort-on-container-exit

# Check results
echo -e "${YELLOW}Checking Docker test results...${NC}"
FAILED=0

# Check if all client containers completed successfully
for CLIENT in ubuntu-focal ubuntu-jammy debian-bookworm kali; do
  RESULT_FILE="/tmp/test-results-${CLIENT}.txt"

  if docker-compose logs "$CLIENT" | grep -q "Test for $CLIENT completed successfully"; then
    echo -e "${GREEN}✓ $CLIENT tests passed${NC}"
  else
    echo -e "${RED}✗ $CLIENT tests failed${NC}"
    FAILED=1
  fi
done

# Check load test results
if docker-compose logs load-test | grep -q "Load test completed"; then
  echo -e "${GREEN}✓ Load tests completed${NC}"
else
  echo -e "${RED}✗ Load tests failed${NC}"
  FAILED=1
fi

# Display cache stats
echo -e "${YELLOW}Cache statistics:${NC}"
curl -s http://localhost:3142/admin/stats | jq .

# Clean up
echo -e "${YELLOW}Cleaning up...${NC}"
docker-compose down -v

if [ $FAILED -eq 0 ]; then
  echo -e "${GREEN}All integration tests passed!${NC}"
  exit 0
else
  echo -e "${RED}Some integration tests failed!${NC}"
  exit 1
fi
