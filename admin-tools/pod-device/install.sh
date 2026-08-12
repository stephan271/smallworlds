#!/usr/bin/env bash
#
# Install the pod archive agent on a home device.
#
#   sudo ./install.sh '<enrolment-string>'
#
# The device connects outbound only: no ports are opened, no dynamic DNS is
# needed, and it can neither write to nor delete anything on the cluster.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_USER="podarchive"
LIB_DIR="/usr/local/lib/pod-archive"
CONFIG_DIR="/etc/pod-archive"
DATA_DIR="${POD_DATA_DIR:-/var/lib/pod-archive}"

if [[ $# -ne 1 ]]; then
    echo "Usage: sudo ./install.sh '<enrolment-string>'" >&2
    exit 1
fi
ENROLLMENT="$1"

if [[ "$(id -u)" -ne 0 ]]; then
    echo "This installer must run as root (it creates a system user and units)." >&2
    exit 1
fi

command -v python3 >/dev/null || { echo "python3 is required." >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd is required." >&2; exit 1; }

echo "Validating the enrolment string..."
python3 - "$ENROLLMENT" <<'PY'
import base64, json, sys
try:
    document = json.loads(base64.b64decode(sys.argv[1]))
except Exception as exc:
    sys.exit(f"Enrolment string is not valid: {exc}")
missing = [k for k in ("url", "user_id", "token") if not document.get(k)]
if missing:
    sys.exit(f"Enrolment string is missing: {', '.join(missing)}")
PY

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    echo "Creating the ${SERVICE_USER} system user..."
    useradd --system --home-dir "$DATA_DIR" --create-home \
            --shell /usr/sbin/nologin "$SERVICE_USER"
fi

echo "Installing the agent..."
install -d -m 0755 "$LIB_DIR"
install -m 0755 "${SCRIPT_DIR}/pod-agent.py" "${LIB_DIR}/pod-agent.py"

install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$CONFIG_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "${DATA_DIR}/objects"

echo "Writing the device credential..."
umask 077
python3 - "$ENROLLMENT" "${CONFIG_DIR}/config.json" <<'PY'
import base64, json, sys
document = json.loads(base64.b64decode(sys.argv[1]))
with open(sys.argv[2], "w") as handle:
    json.dump(document, handle, indent=2)
PY
chown "$SERVICE_USER:$SERVICE_USER" "${CONFIG_DIR}/config.json"
chmod 0600 "${CONFIG_DIR}/config.json"

echo "Installing the systemd units..."
install -m 0644 "${SCRIPT_DIR}/pod-archive.service" /etc/systemd/system/
install -m 0644 "${SCRIPT_DIR}/pod-archive.timer" /etc/systemd/system/
if [[ "$DATA_DIR" != "/var/lib/pod-archive" ]]; then
    mkdir -p /etc/systemd/system/pod-archive.service.d
    cat > /etc/systemd/system/pod-archive.service.d/data-dir.conf <<EOF
[Service]
Environment=POD_DATA=${DATA_DIR}
ReadWritePaths=${DATA_DIR}
EOF
fi

systemctl daemon-reload
systemctl enable --now pod-archive.timer

cat <<EOF

Installed. The device will pull every 15 minutes, starting shortly.

  Watch the first sync:   journalctl -u pod-archive.service -f
  Force a run now:        sudo systemctl start pod-archive.service
  Re-verify every file:   sudo -u ${SERVICE_USER} ${LIB_DIR}/pod-agent.py --verify-only
  Your data lives in:     ${DATA_DIR}/objects

Two things worth doing before this device holds the only copy of anything:

  1. Encrypt the disk (LUKS). The archive is stored as plain files, so anyone
     who takes the device can read every photo on it.
  2. Check on it occasionally. The operator can see whether it is still
     reporting in, but a device that is quietly full or failing is still your
     responsibility.
EOF
