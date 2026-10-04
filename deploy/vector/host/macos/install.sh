#!/bin/sh
# Installs or upgrades the Vector agent on a macOS host as a per-user
# LaunchAgent (no root needed: `log stream` and host_metrics both work as the
# login user). Run it on the host from a checkout of this repo:
#
#   sops -d --extract '["stringData"]["api_key"]' deploy/vector/host/secret.yaml |
#     deploy/vector/host/macos/install.sh
#
# The key is read from stdin and written to ~/.vector/secrets/api_key (0600);
# with nothing on stdin an existing key is kept. Re-running upgrades Vector to
# the pinned version and reloads the agent.
set -eu
VERSION=0.58.0
here=$(cd "$(dirname "$0")" && pwd)
home="$HOME/.vector"
label=xyz.wiebe.bugbarn.vector
plist="$HOME/Library/LaunchAgents/$label.plist"

case "$(uname -m)" in
arm64) arch=arm64 ;;
x86_64) arch=x86_64 ;;
*) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$home/bin" "$home/data" "$home/secrets" "$HOME/Library/LaunchAgents"
chmod 700 "$home/secrets"

if [ ! -t 0 ]; then
	key=$(cat)
	if [ -n "$key" ]; then
		umask 077
		printf '%s' "$key" > "$home/secrets/api_key"
	fi
fi
test -s "$home/secrets/api_key" || { echo "no API key: pipe it on stdin" >&2; exit 1; }

if [ "$("$home/bin/vector" --version 2>/dev/null | awk '{print $2}')" != "$VERSION" ]; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL "https://packages.timber.io/vector/$VERSION/vector-$VERSION-$arch-apple-darwin.tar.gz" |
		tar -xz -C "$tmp"
	install -m 0755 "$(find "$tmp" -type f -path '*/bin/vector' | head -1)" "$home/bin/vector"
fi

sed "s|__VECTOR_HOME__|$home|g" "$here/vector.yaml" > "$home/vector.yaml"
"$home/bin/vector" validate --no-environment "$home/vector.yaml"

cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$home/bin/vector</string>
    <string>--config</string>
    <string>$home/vector.yaml</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>$home/vector.log</string>
  <key>StandardErrorPath</key><string>$home/vector.log</string>
</dict>
</plist>
EOF

uid=$(id -u)
launchctl bootout "gui/$uid/$label" 2>/dev/null || true
launchctl bootstrap "gui/$uid" "$plist"
echo "vector $VERSION running as $label; log: $home/vector.log"
