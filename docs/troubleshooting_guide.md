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

5. Adjust cache TTL settings in config.yaml

### High Memory Usage

**Symptom**: apt-cacher-go consuming excessive memory.

**Possible causes**:
- Too many concurrent connections
- Large files being fully buffered
- Memory leak in application

**Solutions**:
1. Monitor memory usage:
   ```
   ps -o pid,user,%mem,command ax | grep apt-cacher-go
   ```

2. Adjust concurrency settings in configuration:
   ```
   max_concurrent_downloads: 10  # Reduce this value
   ```

3. Check for unusually large downloads:
   ```
   find /var/cache/apt-cacher-go -type f -size +100M
   ```

4. Restart service if memory usage is continuously growing without stabilizing

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
   http://apt-cacher-server:3142/metrics

5. Consider hardware upgrades or tuning storage:
   - Use SSD for cache storage
   - Increase RAM for better disk caching

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
