#!/bin/bash
################################################################################
# PutLive Backup Script
# Backs up database and configuration to S3
################################################################################

set -euo pipefail

# Configuration
BACKUP_DIR="/tmp/putlive-backup-$(date +%Y%m%d-%H%M%S)"
S3_BUCKET="${PUTLIVE_S3_BUCKET:-putlive-backups}"
RETENTION_DAYS=30

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

error() {
    echo "[ERROR] $1" >&2
    exit 1
}

# Create backup directory
mkdir -p "$BACKUP_DIR"

log "Starting PutLive backup to $BACKUP_DIR"

# Backup database
if [ -f /var/lib/putlive/database/putlive.db ]; then
    log "Backing up database..."
    sqlite3 /var/lib/putlive/database/putlive.db ".backup $BACKUP_DIR/putlive.db"
    
    # Verify backup
    if ! sqlite3 "$BACKUP_DIR/putlive.db" "PRAGMA integrity_check;" | grep -q "ok"; then
        error "Database backup verification failed"
    fi
    log "Database backed up successfully"
fi

# Backup configuration
if [ -d /etc/putlive ]; then
    log "Backing up configuration..."
    cp -r /etc/putlive "$BACKUP_DIR/"
    
    # Remove sensitive data
    if [ -f "$BACKUP_DIR/putlive/.admin_password" ]; then
        rm "$BACKUP_DIR/putlive/.admin_password"
    fi
    log "Configuration backed up"
fi

# Backup video metadata (not actual videos, too large)
if [ -d /var/lib/putlive/videos ]; then
    log "Backing up video metadata..."
    find /var/lib/putlive/videos -name "*.json" -exec cp {} "$BACKUP_DIR/" \; 2>/dev/null || true
fi

# Create tarball
log "Creating archive..."
ARCHIVE_NAME="putlive-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
tar -czf "/tmp/$ARCHIVE_NAME" -C "$(dirname $BACKUP_DIR)" "$(basename $BACKUP_DIR)"

# Upload to S3 if AWS CLI available
if command -v aws &> /dev/null; then
    log "Uploading to S3..."
    
    if aws s3 cp "/tmp/$ARCHIVE_NAME" "s3://$S3_BUCKET/backups/$ARCHIVE_NAME"; then
        log "Backup uploaded to s3://$S3_BUCKET/backups/$ARCHIVE_NAME"
        
        # Clean old backups from S3
        log "Cleaning old backups (retention: $RETENTION_DAYS days)..."
        CUTOFF_DATE=$(date -d "$RETENTION_DAYS days ago" +%Y%m%d 2>/dev/null || date -v-${RETENTION_DAYS}d +%Y%m%d)
        
        aws s3 ls "s3://$S3_BUCKET/backups/" | while read -r line; do
            BACKUP_FILE=$(echo "$line" | awk '{print $4}')
            if [[ $BACKUP_FILE =~ putlive-backup-([0-9]{8}) ]]; then
                BACKUP_DATE="${BASH_REMATCH[1]}"
                if [ "$BACKUP_DATE" -lt "$CUTOFF_DATE" ]; then
                    log "Deleting old backup: $BACKUP_FILE"
                    aws s3 rm "s3://$S3_BUCKET/backups/$BACKUP_FILE"
                fi
            fi
        done
        
        # Send CloudWatch metric
        aws cloudwatch put-metric-data \
            --namespace "PutLive/Production" \
            --metric-name BackupSuccess \
            --value 1 \
            --unit Count 2>/dev/null || true
    else
        error "Failed to upload backup to S3"
    fi
else
    log "AWS CLI not available, backup saved locally: /tmp/$ARCHIVE_NAME"
fi

# Cleanup
rm -rf "$BACKUP_DIR"

log "Backup completed successfully"
