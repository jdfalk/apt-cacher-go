<!-- file: docs/troubleshooting_guide.md -->

# apt-cacher-go Troubleshooting Guide

This guide provides solutions for common issues encountered when running
apt-cacher-go.

## Common Issues

### Connection Refused

**Symptom**: Clients cannot connect to the apt-cacher-go service.

**Possible causes**:

- apt-cacher-go service not running
- Firewall blocking the port
- apt-cacher-go listening on wrong interface

**Solutions**:

1. Check if the service is running:

   ```
   sudo systemctl status apt-cacher-go
   # or for Docker
   docker ps | grep apt-cacher-go
   ```

2. Verify listening ports:

   ```
   sudo netstat -tulpn | grep apt-cacher-go
   # or
   sudo ss -tulpn | grep 3142
   ```

3. Check firewall rules:

   ```
   sudo iptables -L | grep 3142
   # or
   sudo ufw status
   ```

4. Verify configuration (listen_address and port):
   ```
   cat /etc/apt-cacher-go/config.yaml | grep -E 'listen_address|port'
   ```

### Cache Not Being Used

**Symptom**: Packages are being downloaded repeatedly rather than served from
cache.

**Possible causes**:

- Cache directory permissions issue
- Incorrect path mapping
- Cache expiration too aggressive
- Disk full

**Solutions**:

1. Check cache directory permissions:

   ```
   ls -la /var/cache/apt-cacher-go
   sudo chown -R apt-cacher:apt-cacher /var/cache/apt-cacher-go
   sudo chmod -R 755 /var/cache/apt-cacher-go
   ```

2. Verify cache contents:

   ```
   ls -la /var/cache/apt-cacher-go
   df -h /var/cache/apt-cacher-go
   ```

3. Check free disk space:

   ```
   df -h
   ```

4. Review logs for mapping errors:

   ```
   journalctl -u apt-cacher-go | grep -i "map"
   ```

5. Adjust cache TTL settings in config.yaml:
   ```yaml
   cache_ttl:
     index: 1h
     package: 30d
     default: 7d
   ```

### High Memory Usage

**Symptom**: apt-cacher-go consuming excessive memory.

**Possible causes**:

- Too many concurrent connections
- Large files being fully buffered
- Memory leak in application
- Memory pressure thresholds set too high

**Solutions**:

1. Monitor memory usage:

   ```
   ps aux | grep apt-cacher-go
   curl http://localhost:3142/health?detailed=true | grep memory
   ```

2. Adjust concurrency settings in configuration:

   ```yaml
   max_concurrent_downloads: 5 # Reduce from default
   prefetch:
     enabled: true
     max_concurrent: 2 # Reduce from default
   ```

3. Check for unusually large downloads:

   ```
   find /var/cache/apt-cacher-go -type f -size +100M | sort -nk2
   ```

4. Set appropriate memory watermarks:

   ```yaml
   memory_high_watermark: 512 # Set lower value (MB)
   memory_critical_watermark: 768 # Set lower value (MB)
   ```

5. Add memory limits to systemd service:
   ```
   sudo systemctl edit apt-cacher-go
   # Add these lines:
   [Service]
   MemoryHigh=768M
   MemoryMax=1024M
   ```

### Database Issues

**Symptom**: Errors related to database operations or corruption.

**Possible causes**:

- Unclean shutdown
- Disk I/O errors
- Insufficient permissions
- Database corruption

**Solutions**:

1. Check database logs:

   ```
   journalctl -u apt-cacher-go | grep -i "database\|pebble\|storage"
   ```

2. Verify database permissions:

   ```
   ls -la /var/cache/apt-cacher-go/db
   ```

3. Rebuild database if corrupted:

   ```
   sudo systemctl stop apt-cacher-go
   sudo mv /var/cache/apt-cacher-go/db /var/cache/apt-cacher-go/db.bak
   sudo systemctl start apt-cacher-go
   # Database will be recreated automatically
   ```

4. Check disk health where database is stored:
   ```
   sudo smartctl -a /dev/sda
   ```

### Hash Mapping Issues

**Symptom**: Packages requested by hash cannot be found.

**Possible causes**:

- Hash mapping database not populated
- Incorrect hash in request
- Package not available in source repository

**Solutions**:

1. Check hash mapping database:

   ```
   curl http://localhost:3142/admin/stats | grep hash
   ```

2. Force rebuild of hash mappings:

   ```
   curl -X POST http://localhost:3142/admin/rebuild-hash-mappings
   ```

3. Verify hash in client request:

   ```
   journalctl -u apt-cacher-go | grep -i "by-hash"
   ```

4. Check if package exists in cache:
   ```
   find /var/cache/apt-cacher-go -name "*.deb" | wc -l
   ```

### HTTPS and SSL Issues

**Symptom**: HTTPS connections fail or certificate errors.

**Possible causes**:

- TLS certificate issues
- Wrong port configuration
- Protocol handling issues

**Solutions**:

1. Verify HTTPS configuration:

   ```
   cat /etc/apt-cacher-go/config.yaml | grep -i "tls\|ssl\|cert\|https"
   ```

2. Check certificate validity:

   ```
   openssl x509 -in /etc/apt-cacher-go/cert.pem -text -noout
   ```

3. Test HTTPS connection:

   ```
   curl -v https://localhost:3143 --insecure
   ```

4. Regenerate certificate if needed:

   ```
   openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
     -keyout /etc/apt-cacher-go/key.pem \
     -out /etc/apt-cacher-go/cert.pem
   ```

5. Check client configuration:
   ```
   cat /etc/apt/apt.conf.d/01proxy
   # Should contain:
   # Acquire::https::Proxy "http://apt-cacher-server:3142";
   ```

## Performance Optimization

### Slow Downloads

**Symptom**: Package downloads are slower than direct downloads.

**Possible causes**:

- Network bottlenecks
- Disk I/O limitations
- Too many concurrent downloads
- CPU or memory constraints

**Solutions**:

1. Check network performance:

   ```
   iperf3 -c apt-cacher-server
   ```

2. Monitor disk I/O:

   ```
   iostat -x 1
   ```

3. Optimize prefetch settings:

   ```yaml
   prefetch:
     enabled: true
     max_concurrent: 5
     batch_size: 20
   ```

4. Check for disk contention:

   ```
   for i in {1..3}; do
     dd if=/dev/zero of=/var/cache/apt-cacher-go/test$i bs=8k count=100000;
   done
   ```

5. Consider using a separate disk for cache:
   ```
   # Mount a separate SSD or fast disk
   sudo mkdir -p /mnt/ssd/apt-cache
   sudo mount /dev/nvme0n1p1 /mnt/ssd/apt-cache
   # Update configuration
   sudo sed -i 's|cache_dir:.*|cache_dir: /mnt/ssd/apt-cache|g' /etc/apt-cacher-go/config.yaml
   ```

### Cache Hit Rate Too Low

**Symptom**: Too many cache misses reported in metrics.

**Possible causes**:

- Cache TTL too short
- Different client architectures
- Repository changes
- Cache being pruned too aggressively

**Solutions**:

1. Check current hit rate:

   ```
   curl http://localhost:3142/metrics | grep "cache_hit_ratio"
   ```

2. Increase cache TTL:

   ```yaml
   cache_ttl:
     index: 2h # Increase from default
     package: 60d # Increase from default
   ```

3. Enable prefetching for all needed architectures:

   ```yaml
   prefetch:
     architectures:
       - amd64
       - arm64
       - i386
   ```

4. Increase cache size limit:

   ```yaml
   max_cache_size: 20GB # Increase from default
   ```

5. Analyze cache contents for opportunities:
   ```
   curl http://localhost:3142/admin/cache/stats
   ```

## Diagnostics

### Collecting Diagnostic Information

1. Generate diagnostic report:

   ```
   apt-cacher-go diag > diagnostic-report.txt
   ```

2. Collect service logs:

   ```
   journalctl -u apt-cacher-go --since "24 hours ago" > apt-cacher-logs.txt
   ```

3. Capture configuration:

   ```
   cp /etc/apt-cacher-go/config.yaml config-backup.yaml
   ```

4. Get system information:

   ```
   uname -a > system-info.txt
   free -h >> system-info.txt
   df -h >> system-info.txt
   ```

5. Collect metrics data:
   ```
   curl http://localhost:3142/metrics > metrics-data.txt
   curl http://localhost:3142/health?detailed=true > health-data.json
   ```

### Enabling Debug Logging

1. Change log level in config:

   ```yaml
   log_level: debug
   ```

2. Restart service:

   ```
   sudo systemctl restart apt-cacher-go
   ```

3. Collect detailed logs:

   ```
   journalctl -u apt-cacher-go -f
   ```

4. Return to normal logging when done:
   ```yaml
   log_level: info
   ```

### Using Prometheus for Monitoring

1. Add apt-cacher-go target to Prometheus config:

   ```yaml
   scrape_configs:
     - job_name: 'apt-cacher-go'
       static_configs:
         - targets: ['apt-cacher-server:3142']
       metrics_path: /metrics
       scheme: http
   ```

2. Import Grafana dashboard:

   ```
   # Use the JSON from docs/grafana-dashboard.json
   ```

3. Set up alerts for common issues:

   ```yaml
   - alert: AptCacherHighMemory
     expr: apt_cacher_memory_pressure > 80
     for: 5m
     labels:
       severity: warning
     annotations:
       summary: 'High memory pressure on apt-cacher-go'
       description: 'Memory pressure is {{ $value }}%'

   - alert: AptCacherHighCacheMiss
     expr: apt_cacher_cache_hit_ratio < 0.5
     for: 1h
     labels:
       severity: info
     annotations:
       summary: 'Low cache hit ratio'
       description: 'Cache hit ratio is {{ $value }}'
   ```

## Advanced Troubleshooting

### Analyzing Database Performance

1. Check database size:

   ```
   du -sh /var/cache/apt-cacher-go/db
   ```

2. Monitor database operations:

   ```
   apt-cacher-go debug database
   ```

3. Optimize for your disk type:

   ```yaml
   storage:
     batch_size: 1000
     memory_limit: 256MB
   ```

4. Consider moving database to faster storage:
   ```
   sudo systemctl stop apt-cacher-go
   sudo mv /var/cache/apt-cacher-go/db /ssd/apt-cache/db
   sudo ln -s /ssd/apt-cache/db /var/cache/apt-cacher-go/db
   sudo systemctl start apt-cacher-go
   ```

### Long-term Monitoring and Analysis

1. Set up log rotation:

   ```
   sudo vim /etc/logrotate.d/apt-cacher-go
   # Add:
   /var/log/apt-cacher-go/*.log {
       daily
       missingok
       rotate 14
       compress
       delaycompress
       notifempty
       create 0640 apt-cacher apt-cacher
   }
   ```

2. Regular health checks:

   ```
   # Add to crontab
   @hourly curl -s http://localhost:3142/health > /dev/null || systemctl restart apt-cacher-go
   ```

3. Usage statistics collection:

   ```
   # Add to crontab
   @daily curl -s http://localhost:3142/metrics | grep "apt_cacher" > /var/log/apt-cacher-go/metrics-$(date +\%Y\%m\%d).log
   ```

4. Performance data collection:

   ```
   # Create a script /usr/local/bin/apt-cacher-perf.sh:
   #!/bin/bash
   echo "=== $(date) ===" >> /var/log/apt-cacher-go/performance.log
   echo "MEMORY:" >> /var/log/apt-cacher-go/performance.log
   curl -s http://localhost:3142/health?detailed=true | jq '.memory' >> /var/log/apt-cacher-go/performance.log
   echo "CACHE:" >> /var/log/apt-cacher-go/performance.log
   curl -s http://localhost:3142/health?detailed=true | jq '.cache' >> /var/log/apt-cacher-go/performance.log
   echo "" >> /var/log/apt-cacher-go/performance.log

   # Add to crontab
   @hourly /usr/local/bin/apt-cacher-perf.sh
   ```
