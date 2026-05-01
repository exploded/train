#!/bin/bash
# server-setup.sh
#
# One-time setup for train.mchugh.au on Linode Debian.
# Run as root or with sudo:
#   sudo bash scripts/server-setup.sh
#
# Assumes Caddy and the "deploy" user (with GitHub Actions SSH key) already
# exist from the moon project setup. If they don't, run moon's
# server-setup.sh first. Caddy auto-provisions TLS via Let's Encrypt.
#
# After running, you still need to (manually):
#   1. Add DNS A record train.mchugh.au -> Linode IP
#   2. Add a site block to /etc/caddy/Caddyfile proxying to 127.0.0.1:8080
#      and run: sudo systemctl reload caddy
#   3. Create Google OAuth Client ID at console.cloud.google.com
#   4. Edit /var/www/train/.env with real GOOGLE_CLIENT_*, SESSION_KEY values
#   5. systemctl enable --now train

set -e

DEPLOY_USER="deploy"

echo "=== Train App - Server Deployment Setup ==="

# ---------------------------------------------------------------
# 1. Application directory
# ---------------------------------------------------------------
APP_DIR="/var/www/train"
if [ -d "$APP_DIR" ]; then
    echo "[ok] Application directory $APP_DIR already exists"
else
    mkdir -p "$APP_DIR"
    chown www-data:www-data "$APP_DIR"
    echo "[ok] Created application directory $APP_DIR"
fi

# ---------------------------------------------------------------
# 2. .env template
# ---------------------------------------------------------------
ENV_FILE="$APP_DIR/.env"
if [ -f "$ENV_FILE" ]; then
    echo "[ok] .env file already exists at $ENV_FILE (not overwriting)"
else
    SESSION_KEY=$(openssl rand -hex 32)
    cat > "$ENV_FILE" << ENV_TEMPLATE
# Server
PORT=8080
PROD=True
APP_TIMEZONE=Australia/Sydney

# Google OAuth - replace with real values from console.cloud.google.com
GOOGLE_CLIENT_ID=your_client_id_here
GOOGLE_CLIENT_SECRET=your_client_secret_here
OAUTH_REDIRECT_URL=https://train.mchugh.au/auth/google/callback

# 32-byte HMAC key (auto-generated)
SESSION_KEY=$SESSION_KEY
ENV_TEMPLATE
    chown www-data:www-data "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    echo "[ok] Created .env at $ENV_FILE - edit GOOGLE_CLIENT_ID/SECRET before starting"
fi

# ---------------------------------------------------------------
# 3. systemd service
# ---------------------------------------------------------------
SERVICE_FILE="/etc/systemd/system/train.service"
cat > "$SERVICE_FILE" << 'SERVICE'
[Unit]
Description=Train iPhone Workout Tracker
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/var/www/train
EnvironmentFile=/var/www/train/.env
ExecStart=/var/www/train/train
Restart=on-failure
RestartSec=5

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
# SQLite needs to write its DB + WAL files in the working directory
ReadWritePaths=/var/www/train

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
echo "[ok] Created systemd service at $SERVICE_FILE"

# ---------------------------------------------------------------
# 4. Install the deploy script (runs as root via sudo).
# ---------------------------------------------------------------
DEPLOY_SCRIPT_URL="https://raw.githubusercontent.com/exploded/train/master/scripts/deploy-train"
if ! curl -fsSL "$DEPLOY_SCRIPT_URL" -o /usr/local/bin/deploy-train; then
    echo "[error] Failed to download deploy-train from $DEPLOY_SCRIPT_URL"
    echo "        (If this is the first deploy and the repo is empty,"
    echo "         the deploy bundle's self-update logic will install it"
    echo "         on first deployment. You can also copy it manually.)"
fi
[ -f /usr/local/bin/deploy-train ] && chmod +x /usr/local/bin/deploy-train

# ---------------------------------------------------------------
# 5. sudoers - allow the existing deploy user to run our deploy script
# ---------------------------------------------------------------
SUDOERS_FILE="/etc/sudoers.d/train-deploy"
cat > "$SUDOERS_FILE" << 'EOF'
deploy ALL=(ALL) NOPASSWD: /usr/local/bin/deploy-train
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop train
EOF
chmod 440 "$SUDOERS_FILE"
visudo -c -f "$SUDOERS_FILE"
echo "[ok] sudoers entry created at $SUDOERS_FILE"

# ---------------------------------------------------------------
# 6. Caddy site block - print snippet for the user to add manually
# ---------------------------------------------------------------
echo "[note] Add the following block to /etc/caddy/Caddyfile, then run:"
echo "       sudo systemctl reload caddy"
echo "       Caddy will provision the TLS cert on first request."
cat << 'CADDY'

train.mchugh.au {
    reverse_proxy 127.0.0.1:8080
}

CADDY

echo ""
echo "=== Setup complete ==="
echo "Next manual steps:"
echo "  1. DNS: point train.mchugh.au A record at this server"
echo "  2. Add the Caddy block above to /etc/caddy/Caddyfile, then:"
echo "     sudo systemctl reload caddy"
echo "  3. console.cloud.google.com -> create OAuth 2.0 Client ID:"
echo "     - Authorized redirect URI: https://train.mchugh.au/auth/google/callback"
echo "     - Authorized JS origin:    https://train.mchugh.au"
echo "  4. Edit $ENV_FILE with real GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET"
echo "  5. systemctl enable --now train"
echo ""
echo "Existing GitHub Actions secrets (DEPLOY_HOST/USER/SSH_KEY) from moon"
echo "can be re-used; just push to master to deploy."
