#!/usr/bin/env bash
set -euo pipefail
umask 077
instance=${1:?mission instance required}
[[ "$instance" =~ ^[a-zA-Z0-9_-]+$ ]] || exit 2
unit="agentos@${instance}.service"
backup_root=${AGENTOS_BACKUP_ROOT:-/var/backups/agentos}
[[ "$backup_root" == /* ]] || exit 2
restart=false
if systemctl is-active --quiet "$unit"; then restart=true; fi
finish() {
    result=$?
    trap - EXIT
    if "$restart"; then
        systemctl start "$unit" || result=1
    fi
    exit "$result"
}
trap finish EXIT
trap 'restart=false; exit 143' TERM INT
# Explicit stop suppresses supervisor restart while acquiring the state lock.
systemctl stop "$unit"
destination="$backup_root/$instance"
mkdir -p "$destination"
archive="$destination/$(date -u +%Y%m%dT%H%M%S%N)-$$.tar"
/opt/agentos/agentos backup --state "/var/lib/agentos/$instance" --output "$archive" > "$archive.receipt.json"
# Internal verification is mandatory before this run is considered successful.
# The receipt's outer SHA-256 is retained separately for restore verification.
/opt/agentos/agentos backup-verify --archive "$archive"
