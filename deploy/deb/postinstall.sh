#!/bin/bash
set -e

# Create system user and group if they don't exist.
if ! getent group bugbarn > /dev/null 2>&1; then
    groupadd --system bugbarn
fi
if ! getent passwd bugbarn > /dev/null 2>&1; then
    useradd --system --gid bugbarn --no-create-home \
        --home-dir /var/lib/bugbarn \
        --shell /usr/sbin/nologin \
        --comment "BugBarn service account" bugbarn
fi

# Ensure state directory exists with correct ownership.
install -d -o bugbarn -g bugbarn -m 0750 /var/lib/bugbarn
install -d -o bugbarn -g bugbarn -m 0750 /var/lib/bugbarn/spool

# Ensure config directory exists.
install -d -m 0755 /etc/bugbarn

# Drop a sample config if no config exists yet.
if [ ! -f /etc/bugbarn/bugbarn.conf ]; then
    cp /etc/bugbarn/bugbarn.conf.example /etc/bugbarn/bugbarn.conf
    chmod 0640 /etc/bugbarn/bugbarn.conf
    chown root:bugbarn /etc/bugbarn/bugbarn.conf
    echo "BugBarn: sample config installed at /etc/bugbarn/bugbarn.conf — edit before starting."
fi

# Reload systemd and enable the service.
if command -v systemctl > /dev/null 2>&1; then
    systemctl daemon-reload
    systemctl enable bugbarn.service || true
    echo "BugBarn: service enabled. Start with: sudo systemctl start bugbarn"
fi
