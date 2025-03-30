<!-- file: docs/troubleshooting_guide.md -->

# apt-cacher-go Troubleshooting Guide

This guide provides solutions for common issues encountered when running apt-cacher-go.

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
   systemctl status apt-cacher-go
   ```

2. Verify listening ports:
   ```
   ss -tulpn | grep apt-cacher-go
   ```

3. Check firewall rules:
   ```
   sudo ufw status
   sudo iptables -L -n
   ```

4. Verify configuration (listen_address and port):
   ```
   cat /etc/apt-cacher-go/config.yaml
   ```

### Cache Not Being Used

**Symptom**: Packages are being downloaded repeatedly rather than served from cache.

**Possible causes**:

- Cache directory permissions issue
- Incorrect path mapping
- Cache expiration too aggressive
- Disk full

**Solutions**:

1. Check cache directory permissions:
   ```
   ls -la /var/cache/apt-cacher-go
   ```

2. Verify cache contents:
   ```
   find /var/cache/apt-cacher-go -type f | wc -l
   ls -la /var/cache/apt-cacher-go
   ```

3. Check free disk space:
   ```
   df -h /var/cache/apt-cacher-go
   ```

4. Review logs for mapping errors:
   ```
   journalctl -u apt-cacher-go | grep "mapping"
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
   ps -o pid,user,%mem,command ax | grep apt-cacher-go
   ```

2. Adjust concurrency settings in configuration:
   ```yaml
   max_concurrent_downloads: 10  # Reduce this value
   ```

3. Check for unusually large downloads:
   ```
   find /var/cache/apt-cacher-go -type f -size +100M
   ```

4. Adjust memory watermark settings:
   ```yaml
   memory_high_watermark: 1024    # MB, lower for constrained systems
   memory_critical_watermark: 1536  # MB, lower for constrained systems
   ```

5. Restart service if memory usage is continuously growing without stabilizing:
   ```
   sudo systemctl restart apt-cacher-go
   ```

### Slow Response Times

**Symptom**: Packages download slowly through the cache.

**Possible causes**:

- Network bottlenecks
- Disk I/O limitations
- Too many concurrent clients
- CPU or memory constraints

**Solutions**:

1. Check system load:
   ```
   top
   iostat -x 1
   ```

2. Monitor network bandwidth:
   ```
   iftop -i <interface>
   ```

3. Check disk performance:
   ```
   sudo iostat -xz 1
   ```

4. Review metrics endpoint for bottlenecks:
   ```
   curl http://apt-cacher-server:3142/metrics
   ```

5. Consider hardware upgrades or tuning storage:
   - Use SSD for cache storage
   - Increase RAM for better disk caching
   - Mount cache directory with noatime option

### Deadlocks During Shutdown

**Symptom**: apt-cacher-go hangs during shutdown or restart.

**Possible causes**:

- Concurrent cache operations during shutdown
- Long-running background tasks
- Lock contention

**Solutions**:

1. Check for zombie processes:
   ```
   ps aux | grep apt-cacher-go
   ```

2. Force kill if necessary:
   ```
   sudo pkill -9 apt-cacher-go
   ```

3. Adjust shutdown timeout in systemd service:
   ```
   sudo systemctl edit apt-cacher-go
   # Add the following:
   # [Service]
   # TimeoutStopSec=10
   ```

4. Update to the latest version which includes deadlock fixes

## Log Analysis

### Understanding Log Messages

The log format follows this pattern:
```
[TIME] [LEVEL] [COMPONENT] Message
```

Common log components:

- `[SERVER]`: HTTP server events
- `[CACHE]`: Cache operations
- `[BACKEND]`: Upstream repository interactions
- `[MAPPER]`: Path mapping operations
- `[MEMORY]`: Memory monitoring alerts

### Common Error Messages

1. **"Failed to map path"**:
   - Issue with repository mapping rules
   - Check the request URL and your mapping configuration

2. **"Cache write failed"**:
   - Disk space issue or permissions problem
   - Check available space and directory permissions

3. **"Backend connection failed"**:
   - Network connectivity issue to upstream repository
   - Check network settings and repository availability

4. **"Access denied from IP"**:
   - Client IP not in allowed list
   - Update allowed_ips configuration if needed

5. **"Warning: Cache close timed out"**:
   - Deadlock during cache state saving
   - Safe to ignore during server restart
   - May indicate need for performance tuning

6. **"Memory pressure"**:
   - System experiencing memory constraints
   - Consider increasing available RAM or reducing max_concurrent_downloads

## Diagnostic Commands

### System Status

1. Check memory and CPU usage:
   ```
   top -p $(pgrep apt-cacher-go)
   ```

2. Check file descriptors:
   ```
   ls -la /proc/$(pgrep apt-cacher-go)/fd | wc -l
   ```

3. Check network connections:
   ```
   netstat -anp | grep apt-cacher-go
   ```

4. Check for disk IO contention:
   ```
   sudo iotop -p $(pgrep apt-cacher-go)
   ```

5. Examine systemd journal:
   ```
   journalctl -u apt-cacher-go -n 100 --no-pager
   ```

### Application Status

1. View application health:
   ```
   curl http://localhost:3142/health
   ```

2. Get detailed health information:
   ```
   curl http://localhost:3142/health?detailed=true
   ```

3. Get metrics:
   ```
   curl http://localhost:3142/metrics
   ```

4. View cache statistics:
   ```
   curl http://localhost:3142/admin/stats
   ```

5. Check backend status:
   ```
   curl http://localhost:3142/admin/backends
   ```

## Resetting the Cache

If the cache needs to be cleared completely:

1. Using the admin interface:
   - Visit http://apt-cacher-server:3142/admin
   - Click "Clear Cache" button

2. Using API:
   ```
   curl -X POST http://apt-cacher-server:3142/admin/clearcache
   ```

3. Manually:
   ```
   sudo systemctl stop apt-cacher-go
   sudo rm -rf /var/cache/apt-cacher-go/*
   sudo systemctl start apt-cacher-go
   ```

4. Selectively clean only index files:
   ```
   find /var/cache/apt-cacher-go -name "Release" -o -name "Packages" -o -name "InRelease" | xargs rm
   ```

## Repository Mapping Issues

If packages aren't being properly mapped to repositories:

1. Check your mapping rules in config.yaml
2. Verify backend URLs match those in client requests
3. Increase log verbosity:
   ```yaml
   log_level: debug
   ```
4. Check logs for mapping decisions:
   ```
   journalctl -u apt-cacher-go | grep "\[MAPPER\]"
   ```
5. Consider adding explicit rules:
   ```yaml
   mapping_rules:
     - type: regex
       pattern: "(security|archive).ubuntu.com/ubuntu"
       repository: ubuntu
       priority: 100
   ```

## Getting Support

If issues persist after trying the solutions in this guide:

1. Gather diagnostic information:
   ```
   apt-cacher-go diagnostics > diagnostics.log
   ```

2. Check GitHub issues for similar problems:
   https://github.com/jdfalk/apt-cacher-go/issues

3. Submit a new issue with:
   - Detailed description of the problem
   - Steps to reproduce
   - Configuration file (with sensitive information redacted)
   - Diagnostic logs
   - System information

4. For commercial support options, contact the maintainers directly
