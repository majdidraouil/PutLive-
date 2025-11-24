#!/bin/bash
################################################################################
# PutLive FFmpeg Cleanup Script
# Runs every 5 minutes via cron to kill zombie/leaked FFmpeg processes
################################################################################

set -euo pipefail

LOG_TAG="putlive-cleanup"
MAX_FFMPEG_AGE_SECONDS=3600  # Kill if running > 1 hour
MAX_ZOMBIES_BEFORE_ALERT=5

log() {
    logger -t "$LOG_TAG" "$1"
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

error() {
    logger -t "$LOG_TAG" -p err "$1"
    echo "[ERROR] $1" >&2
}

# Count current FFmpeg processes
count_ffmpeg() {
    ps aux | grep -c '[f]fmpeg' || echo "0"
}

# Find zombie/defunct processes
find_zombies() {
    ps aux | grep '[f]fmpeg' | grep -E 'defunct|Z' | awk '{print $2}' || true
}

# Find long-running FFmpeg processes
find_long_running() {
    while read -r pid runtime cmd; do
        if [ "$runtime" -gt "$MAX_FFMPEG_AGE_SECONDS" ]; then
            echo "$pid"
        fi
    done < <(ps -eo pid,etimes,cmd | grep '[f]fmpeg' | awk '{print $1, $2, $0}')
}

# Main cleanup logic
main() {
    log "Starting FFmpeg cleanup check"
    
    INITIAL_COUNT=$(count_ffmpeg)
    log "Found $INITIAL_COUNT FFmpeg processes"
    
    KILLED_COUNT=0
    
    # Kill zombie processes
    ZOMBIES=$(find_zombies)
    if [ -n "$ZOMBIES" ]; then
        log "Found zombie FFmpeg processes: $ZOMBIES"
        echo "$ZOMBIES" | while read -r pid; do
            if [ -n "$pid" ]; then
                log "Killing zombie process $pid"
                kill -9 "$pid" 2>/dev/null || true
                ((KILLED_COUNT++))
            fi
        done
    fi
    
    # Kill long-running processes
    LONG_RUNNING=$(find_long_running)
    if [ -n "$LONG_RUNNING" ]; then
        echo "$LONG_RUNNING" | while read -r pid; do
            if [ -n "$pid" ]; then
                RUNTIME=$(ps -p "$pid" -o etimes= 2>/dev/null || echo "0")
                log "Killing long-running FFmpeg PID $pid (runtime: ${RUNTIME}s)"
                kill -9 "$pid" 2>/dev/null || true
                ((KILLED_COUNT++))
            fi
        done
    fi
    
    # Clean stale lock files
    LOCK_FILES=$(find /var/run -name 'putlive-ffmpeg-*.lock' -mmin +60 2>/dev/null || true)
    if [ -n "$LOCK_FILES" ]; then
        log "Cleaning stale lock files"
        echo "$LOCK_FILES" | xargs rm -f 2>/dev/null || true
    fi
    
    # Check tmpfs usage
    if [ -d /dev/shm/srs ]; then
        TMPFS_USAGE=$(df /dev/shm | tail -1 | awk '{print $5}' | sed 's/%//')
        if [ "$TMPFS_USAGE" -gt 80 ]; then
            log "WARNING: tmpfs usage at ${TMPFS_USAGE}% - cleaning old segments"
            find /dev/shm/srs -type f -mmin +5 -delete 2>/dev/null || true
        fi
    fi
    
    # Clean old HLS segments from main directory
    if [ -d /var/www/putlive/live ]; then
        find /var/www/putlive/live -name "*.ts" -mmin +10 -delete 2>/dev/null || true
        find /var/www/putlive/live -name "*.m3u8" -mmin +10 -delete 2>/dev/null || true
    fi
    
    FINAL_COUNT=$(count_ffmpeg)
    
    if [ "$KILLED_COUNT" -gt 0 ]; then
        log "Cleanup complete: killed $KILLED_COUNT processes ($INITIAL_COUNT → $FINAL_COUNT)"
    else
        log "Cleanup complete: no processes killed"
    fi
    
    # Alert if too many zombies
    if [ "$KILLED_COUNT" -ge "$MAX_ZOMBIES_BEFORE_ALERT" ]; then
        error "WARNING: Killed $KILLED_COUNT FFmpeg processes - possible leak!"
        # Send CloudWatch metric if AWS CLI available
        if command -v aws &> /dev/null; then
            aws cloudwatch put-metric-data \
                --namespace "PutLive/Production" \
                --metric-name FFmpegZombiesKilled \
                --value "$KILLED_COUNT" \
                --unit Count 2>/dev/null || true
        fi
    fi
}

main "$@"
