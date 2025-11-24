#!/bin/bash
################################################################################
# PutLive v3.0 Production Installation Script
# Supports: Ubuntu 20.04/22.04, Debian 11, Amazon Linux 2
################################################################################

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Configuration
PUTLIVE_VERSION="3.0"
SRS_VERSION="6.0"
GO_VERSION="1.21.5"
INSTALL_DIR="/opt/putlive"
CONFIG_DIR="/etc/putlive"
DATA_DIR="/var/lib/putlive"
LOG_DIR="/var/log/putlive"

# Parse arguments
AUTO_MODE=false
DOMAIN=""
EMAIL=""
ENABLE_SSL=false
ENABLE_MONITORING=false
ENABLE_SWAP=true
ENABLE_AUTH=true

while [[ $# -gt 0 ]]; do
    case $1 in
        --auto) AUTO_MODE=true; shift ;;
        --domain) DOMAIN="$2"; shift 2 ;;
        --email) EMAIL="$2"; shift 2 ;;
        --ssl) ENABLE_SSL=true; shift ;;
        --monitoring) ENABLE_MONITORING=true; shift ;;
        --swap) ENABLE_SWAP=true; shift ;;
        --auth) ENABLE_AUTH=true; shift ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

log() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
    exit 1
}

warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "This script must be run as root"
    fi
}

detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        OS_VERSION=$VERSION_ID
    else
        error "Cannot detect OS"
    fi
    
    log "Detected OS: $OS $OS_VERSION"
}

install_dependencies() {
    log "Installing system dependencies..."
    
    if [[ "$OS" == "ubuntu" ]] || [[ "$OS" == "debian" ]]; then
        apt-get update
        apt-get install -y \
            build-essential \
            git \
            curl \
            wget \
            nginx \
            sqlite3 \
            ffmpeg \
            logrotate \
            fail2ban \
            htop \
            net-tools \
            jq \
            bc \
            unzip
    elif [[ "$OS" == "amzn" ]]; then
        yum update -y
        yum install -y \
            gcc \
            gcc-c++ \
            make \
            git \
            curl \
            wget \
            nginx \
            sqlite \
            ffmpeg \
            logrotate \
            fail2ban \
            htop \
            net-tools \
            jq \
            bc \
            unzip
    fi
}

create_user() {
    log "Creating putlive user..."
    
    if ! id -u putlive > /dev/null 2>&1; then
        useradd -r -s /bin/bash -d /opt/putlive putlive
    fi
}

create_directories() {
    log "Creating directory structure..."
    
    mkdir -p "$INSTALL_DIR"/{api,web,scripts}
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"/{videos/{raw,processed},recordings,database,backups}
    mkdir -p "$LOG_DIR"
    mkdir -p /dev/shm/srs
    
    chown -R putlive:putlive "$INSTALL_DIR" "$DATA_DIR"
    chmod 755 "$DATA_DIR"
    chmod 777 /dev/shm/srs
}

install_go() {
    log "Installing Go $GO_VERSION..."
    
    if command -v go &> /dev/null; then
        CURRENT_GO=$(go version | awk '{print $3}' | sed 's/go//')
        if [[ "$CURRENT_GO" == "$GO_VERSION" ]]; then
            log "Go $GO_VERSION already installed"
            return
        fi
    fi
    
    cd /tmp
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
    
    cat >> /etc/profile.d/go.sh << 'EOF'
export PATH=$PATH:/usr/local/go/bin
export GOPATH=/opt/putlive/go
EOF
    
    source /etc/profile.d/go.sh
    go version
}

install_srs() {
    log "Installing SRS $SRS_VERSION..."
    
    cd /tmp
    git clone -b ${SRS_VERSION}release https://github.com/ossrs/srs.git --depth 1
    cd srs/trunk
    
    ./configure --jobs=$(nproc)
    make -j$(nproc)
    
    mkdir -p /usr/local/srs/{conf,objs}
    cp objs/srs /usr/local/srs/objs/
    chmod +x /usr/local/srs/objs/srs
    
    log "SRS installed successfully"
}

configure_swap() {
    if [[ "$ENABLE_SWAP" == true ]]; then
        log "Configuring 2GB swap..."
        
        if [[ ! -f /swapfile ]]; then
            fallocate -l 2G /swapfile
            chmod 600 /swapfile
            mkswap /swapfile
            swapon /swapfile
            
            if ! grep -q '/swapfile' /etc/fstab; then
                echo '/swapfile none swap sw 0 0' >> /etc/fstab
            fi
            
            sysctl vm.swappiness=10
            echo 'vm.swappiness=10' >> /etc/sysctl.conf
        fi
    fi
}

configure_tmpfs() {
    log "Configuring tmpfs limits..."
    
    if ! grep -q 'tmpfs /dev/shm tmpfs' /etc/fstab; then
        echo 'tmpfs /dev/shm tmpfs defaults,size=500M 0 0' >> /etc/fstab
        mount -o remount /dev/shm
    fi
}

install_api() {
    log "Building PutLive API..."
    
    cd "$INSTALL_DIR/api"
    
    # Create go.mod if not exists
    if [[ ! -f go.mod ]]; then
        /usr/local/go/bin/go mod init putlive
    fi
    
    # Download dependencies
    /usr/local/go/bin/go get github.com/golang-jwt/jwt/v5
    /usr/local/go/bin/go get github.com/mattn/go-sqlite3
    /usr/local/go/bin/go get github.com/prometheus/client_golang/prometheus
    
    # Build
    /usr/local/go/bin/go build -ldflags="-s -w" -o putlive-api
    chmod +x putlive-api
    chown putlive:putlive putlive-api
}

configure_srs() {
    log "Configuring SRS..."
    
    cp "$INSTALL_DIR/../config/srs.conf" /usr/local/srs/conf/putlive.conf
}

configure_nginx() {
    log "Configuring Nginx..."
    
    cp "$INSTALL_DIR/../config/nginx.conf" /etc/nginx/sites-available/putlive
    ln -sf /etc/nginx/sites-available/putlive /etc/nginx/sites-enabled/
    
    if [[ -n "$DOMAIN" ]]; then
        sed -i "s/server_name _;/server_name $DOMAIN;/" /etc/nginx/sites-available/putlive
    fi
    
    nginx -t
}

install_ssl() {
    if [[ "$ENABLE_SSL" == true ]] && [[ -n "$DOMAIN" ]] && [[ -n "$EMAIL" ]]; then
        log "Installing SSL certificate..."
        
        if ! command -v certbot &> /dev/null; then
            if [[ "$OS" == "ubuntu" ]] || [[ "$OS" == "debian" ]]; then
                apt-get install -y certbot python3-certbot-nginx
            fi
        fi
        
        certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos -m "$EMAIL"
    fi
}

install_systemd_services() {
    log "Installing systemd services..."
    
    cp "$INSTALL_DIR/../systemd/srs.service" /etc/systemd/system/
    cp "$INSTALL_DIR/../systemd/putlive-api.service" /etc/systemd/system/
    cp "$INSTALL_DIR/../systemd/putlive-scheduler.service" /etc/systemd/system/
    
    systemctl daemon-reload
    systemctl enable srs putlive-api putlive-scheduler
}

install_cron_jobs() {
    log "Installing cron jobs..."
    
    cp "$INSTALL_DIR/scripts/cleanup-ffmpeg.sh" /usr/local/bin/
    cp "$INSTALL_DIR/scripts/health-check.sh" /usr/local/bin/
    cp "$INSTALL_DIR/scripts/backup.sh" /usr/local/bin/
    
    chmod +x /usr/local/bin/{cleanup-ffmpeg.sh,health-check.sh,backup.sh}
    
    cat > /etc/cron.d/putlive << 'EOF'
*/5 * * * * root /usr/local/bin/cleanup-ffmpeg.sh >> /var/log/putlive/cleanup.log 2>&1
* * * * * root /usr/local/bin/health-check.sh >> /var/log/putlive/health.log 2>&1
0 */6 * * * root /usr/local/bin/backup.sh >> /var/log/putlive/backup.log 2>&1
0 2 * * 0 root find /var/lib/putlive/recordings -type f -mtime +30 -delete
EOF
}

configure_logrotate() {
    log "Configuring log rotation..."
    
    cat > /etc/logrotate.d/putlive << 'EOF'
/var/log/putlive/*.log {
    daily
    missingok
    rotate 7
    compress
    delaycompress
    notifempty
    create 0640 putlive putlive
    sharedscripts
    postrotate
        /bin/systemctl reload srs putlive-api > /dev/null 2>&1 || true
    endscript
    size 100M
    maxage 7
}
EOF
}

configure_fail2ban() {
    log "Configuring Fail2Ban..."
    
    cat > /etc/fail2ban/filter.d/putlive.conf << 'EOF'
[Definition]
failregex = ^\[error\].*client=<HOST>.*
            ^.*Invalid stream key from <HOST>
            ^.*Failed login attempt.*ip=<HOST>
ignoreregex =
EOF
    
    cat > /etc/fail2ban/jail.d/putlive.conf << 'EOF'
[putlive-srs]
enabled = true
port = 1935
filter = putlive
logpath = /var/log/putlive/srs.log
maxretry = 5
findtime = 600
bantime = 3600

[putlive-api]
enabled = true
port = 3000
filter = putlive
logpath = /var/log/putlive/api.log
maxretry = 3
findtime = 300
bantime = 7200
EOF
    
    systemctl restart fail2ban
}

generate_config() {
    log "Generating configuration..."
    
    JWT_SECRET=$(openssl rand -hex 32)
    ADMIN_PASS=$(openssl rand -base64 16)
    
    cat > "$CONFIG_DIR/config.yaml" << EOF
version: "3.0"

server:
  domain: "${DOMAIN:-localhost}"
  http_port: 3000
  https_enabled: ${ENABLE_SSL}

streaming:
  rtmp_port: 1935
  hls_port: 8080
  max_concurrent_streams: 3
  default_quality: "480p"

authentication:
  jwt_secret: "$JWT_SECRET"
  token_expiry: "24h"
  require_auth: ${ENABLE_AUTH}
  default_admin_user: "admin"
  default_admin_pass: "$ADMIN_PASS"

database:
  type: "sqlite"
  path: "$DATA_DIR/database/putlive.db"
  backup_enabled: true

monitoring:
  prometheus_enabled: ${ENABLE_MONITORING}
  prometheus_port: 9090

reliability:
  swap:
    enabled: ${ENABLE_SWAP}
    size: "2G"
  log_rotation:
    enabled: true
  ffmpeg_cleanup:
    enabled: true
    interval: "5m"
  watchdog:
    srs_timeout: "30s"
    api_timeout: "15s"
EOF
    
    chmod 600 "$CONFIG_DIR/config.yaml"
    chown putlive:putlive "$CONFIG_DIR/config.yaml"
    
    log "Admin password: $ADMIN_PASS"
    echo "$ADMIN_PASS" > "$CONFIG_DIR/.admin_password"
    chmod 600 "$CONFIG_DIR/.admin_password"
}

initialize_database() {
    log "Initializing database..."
    
    sqlite3 "$DATA_DIR/database/putlive.db" << 'EOF'
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS stream_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key_hash TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    revoked BOOLEAN DEFAULT 0,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS videos (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    filename TEXT NOT NULL,
    duration_seconds INTEGER,
    size_bytes INTEGER,
    status TEXT DEFAULT 'processing',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS schedules (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL,
    start_time DATETIME NOT NULL,
    end_time DATETIME,
    quality TEXT DEFAULT '480p',
    loop BOOLEAN DEFAULT 0,
    status TEXT DEFAULT 'scheduled',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (video_id) REFERENCES videos(id)
);

CREATE TABLE IF NOT EXISTS analytics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    stream_name TEXT,
    event_type TEXT,
    viewer_count INTEGER,
    bitrate_kbps INTEGER,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);
EOF
    
    chown putlive:putlive "$DATA_DIR/database/putlive.db"
}

start_services() {
    log "Starting services..."
    
    systemctl start srs
    systemctl start putlive-api
    systemctl start nginx
    
    sleep 3
    
    systemctl status srs --no-pager
    systemctl status putlive-api --no-pager
}

print_summary() {
    log "Installation complete!"
    
    cat << EOF

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
🎉 PutLive v${PUTLIVE_VERSION} Installation Complete!
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

📍 Dashboard: http://${DOMAIN:-$(hostname -I | awk '{print $1}')}
🔐 Admin User: admin
🔑 Admin Pass: $(cat $CONFIG_DIR/.admin_password)

📊 RTMP URL: rtmp://${DOMAIN:-$(hostname -I | awk '{print $1}')}/live/stream
📺 HLS URL: http://${DOMAIN:-$(hostname -I | awk '{print $1}')}/live/stream.m3u8

📁 Directories:
   - Config: $CONFIG_DIR
   - Data: $DATA_DIR
   - Logs: $LOG_DIR

🔧 Service Management:
   sudo systemctl status srs putlive-api
   sudo systemctl restart srs putlive-api
   sudo journalctl -u srs -f

📈 Monitoring:
   Health: http://localhost:3000/api/health
   Metrics: http://localhost:9090 (if enabled)

⚠️  IMPORTANT: 
   1. Change admin password on first login
   2. Configure firewall (ports 22, 443, 1935)
   3. Set up SSL if not done: certbot --nginx
   4. Review config: $CONFIG_DIR/config.yaml

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EOF
}

main() {
    log "Starting PutLive v${PUTLIVE_VERSION} installation..."
    
    check_root
    detect_os
    install_dependencies
    create_user
    create_directories
    install_go
    install_srs
    configure_swap
    configure_tmpfs
    configure_srs
    configure_nginx
    generate_config
    initialize_database
    install_systemd_services
    install_cron_jobs
    configure_logrotate
    configure_fail2ban
    
    if [[ "$ENABLE_SSL" == true ]]; then
        install_ssl
    fi
    
    start_services
    print_summary
}

main "$@"
