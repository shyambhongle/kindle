#!/bin/sh
# Installs the backend as a launchd user agent on macOS: starts at login, restarts if it crashes.
# Run from the backend folder:  sh deploy/install-launchd.sh
set -e
DIR="$(cd "$(dirname "$0")/.." && pwd)"
PLIST="$HOME/Library/LaunchAgents/com.shyam.dashboard.plist"
[ -x "$DIR/dashboard" ] || { echo "build first: make build"; exit 1; }
[ -f "$DIR/.env" ] || { echo "create .env first (cp .env.example .env)"; exit 1; }
mkdir -p "$HOME/Library/LaunchAgents"
sed "s|__DIR__|$DIR|g" "$DIR/deploy/com.shyam.dashboard.plist" > "$PLIST"
launchctl unload "$PLIST" 2>/dev/null || true
launchctl load "$PLIST"
echo "installed: $PLIST"
echo "logs:      $DIR/dashboard.log"
echo "stop:      launchctl unload $PLIST"
echo "restart:   launchctl kickstart -k gui/$(id -u)/com.shyam.dashboard"
