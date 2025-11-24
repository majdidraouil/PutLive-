#!/bin/bash
################################################################################
# PutLive Health Check Script
# Used by systemd watchdog and monitoring systems
################################################################################

set -euo pipefail

HEALTHY=0
UNHEALTHY=1

check_srs() {
    # Check if SRS process is running
    if ! pgrep -x srs > /dev/null; then
        echo "CRITICAL: SRS process not running"
        return 1
    fi
    
    # Check SRS HTTP API responds
    if ! curl -sf http://127.0.0.1:1985/api/v1/versions > /dev/null 2>&1; then
        echo "CRITICAL: SRS HTTP API not responding"
        return 1
    fi
    
    return 0
}

check_api() {
    # Check if API process is running
    if ! pgrep -f putlive-api > /dev/null; then
        echo "CRITICAL: PutLive API not running"
        return 1
    fi
    
    # Check API health endpoint
    if ! curl -sf http://127.0.0.1:3000/api/health | grep -q '"status":"ok"' 2>&1; then
        echo "CRITICAL: API health check failed"
        return 1
    fi
    
    return 0
}

check_disk() {
    DISK_USAGE=$(df / | tail -1 | awk '{print $5}' | sed 's/%//')
    if [ "$DISK_USAGE" -gt 95 ]; then
        echo "CRITICAL: Disk usage at ${DISK_USAGE}%"
        return 1
    elif [ "$DISK_USAGE" -gt 90 ]; then
        echo "WARNING: Disk usage at ${DISK_USAGE}%"
    fi
    return 0
}

check_memory() {
    # Check if free command exists
    if ! command -v free &> /dev/null; then
        return 0
    fi
    
    MEM_USAGE=$(free | grep Mem | awk '{printf "%.0f", $3/$2 * 100}')
    if [ "$MEM_USAGE" -gt 95 ]; then
        echo "CRITICAL: Memory usage at ${MEM_USAGE}%"
        return 1
    elif [ "$MEM_USAGE" -gt 90 ]; then
        echo "WARNING: Memory usage at ${MEM_USAGE}%"
    fi
    return 0
}

check_cpu_load() {
    if ! command -v uptime &> /dev/null; then
        return 0
    fi
    
    CPU_CORES=$(nproc)
    LOAD_5MIN=$(uptime | awk -F'load average:' '{print $2}' | awk -F',' '{print $2}' | xargs)
    
    # Convert to integer comparison (multiply by 100)
    LOAD_INT=$(echo "$LOAD_5MIN * 100" | bc -l 2>/dev/null | cut -d'.' -f1 || echo "0")
    THRESHOLD=$((CPU_CORES * 200)) # 2.0 * cores
    
    if [ "$LOAD_INT" -gt "$THRESHOLD" ]; then
        echo "WARNING: CPU load average (5min): $LOAD_5MIN (threshold: $CPU_CORES cores)"
    fi
    
    return 0
}

check_nginx() {
    if ! pgrep -x nginx > /dev/null; then
        echo "WARNING: Nginx not running"
        return 0  # Don't fail on nginx, it's optional
    fi
    return 0
}

# Main health check
main() {
    OVERALL_STATUS=$HEALTHY
    FAILED_COMPONENTS=""
    
    echo "=== PutLive Health Check ==="
    echo "Timestamp: $(date '+%Y-%m-%d %H:%M:%S')"
    echo ""
    
    # Check SRS
    if check_srs; then
        echo "✓ SRS: OK"
    else
        echo "✗ SRS: FAILED"
        OVERALL_STATUS=$UNHEALTHY
        FAILED_COMPONENTS="$FAILED_COMPONENTS SRS"
    fi
    
    # Check API
    if check_api; then
        echo "✓ API: OK"
    else
        echo "✗ API: FAILED"
        OVERALL_STATUS=$UNHEALTHY
        FAILED_COMPONENTS="$FAILED_COMPONENTS API"
    fi
    
    # Check Nginx
    if check_nginx; then
        echo "✓ Nginx: OK"
    else
        echo "⚠ Nginx: WARNING"
    fi
    
    # Check disk
    if check_disk; then
        echo "✓ Disk: OK"
    else
        echo "⚠ Disk: WARNING (not critical)"
    fi
    
    # Check memory
    if check_memory; then
        echo "✓ Memory: OK"
    else
        echo "⚠ Memory: WARNING"
    fi
    
    # Check CPU load
    check_cpu_load
    
    echo ""
    
    # Notify systemd watchdog if running under systemd
    if [ -n "${WATCHDOG_USEC:-}" ]; then
        systemd-notify WATCHDOG=1
    fi
    
    # Final status
    if [ $OVERALL_STATUS -eq $HEALTHY ]; then
        echo "Status: HEALTHY"
        
        # Send CloudWatch metric
        if command -v aws &> /dev/null; then
            aws cloudwatch put-metric-data \
                --namespace "PutLive/Production" \
                --metric-name HealthCheckStatus \
                --value 1 \
                --unit None 2>/dev/null || true
        fi
        
        exit $HEALTHY
    else
        echo "Status: UNHEALTHY"
        echo "Failed components:$FAILED_COMPONENTS"
        
        # Send CloudWatch metric
        if command -v aws &> /dev/null; then
            aws cloudwatch put-metric-data \
                --namespace "PutLive/Production" \
                --metric-name HealthCheckStatus \
                --value 0 \
                --unit None 2>/dev/null || true
        fi
        
        exit $UNHEALTHY
    fi
}

main "$@"
