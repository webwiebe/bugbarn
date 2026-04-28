#!/bin/bash
set -e

if command -v systemctl > /dev/null 2>&1; then
    systemctl stop bugbarn.service || true
    systemctl disable bugbarn.service || true
    systemctl daemon-reload || true
fi
