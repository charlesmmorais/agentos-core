#!/usr/bin/env bash
# Run only on the disposable GitHub Actions VM, never on a deployment host.
set -euo pipefail
[[ "${GITHUB_ACTIONS:-}" == true && "$EUID" == 0 ]] || exit 2
project=${1:?absolute checkout path required}
cleanup() {
    systemctl stop agentos-backup@ci.service agentos@ci.service || true
}
trap cleanup EXIT
id agentos >/dev/null 2>&1 || useradd --system --home-dir /var/lib/agentos --shell /usr/sbin/nologin agentos
install -d /opt/agentos/deploy /etc/agentos /var/lib/agentos /var/backups/agentos
install -m 755 "$project/agentos" /opt/agentos/agentos
install -m 644 "$project/deploy/backup.sh" /opt/agentos/deploy/backup.sh
install -m 644 "$project/deploy/agentos@.service" "$project/deploy/agentos-backup@.service" "$project/deploy/agentos-backup@.timer" /etc/systemd/system/
systemd-analyze verify /etc/systemd/system/agentos@.service /etc/systemd/system/agentos-backup@.service /etc/systemd/system/agentos-backup@.timer
install -d /opt/agentos/input
printf '%s\n' 'print("{\"ok\":true}")' > /opt/agentos/observe.py
env PATH=/usr/bin:/bin /opt/agentos/agentos init --executor trusted-host --state /var/lib/agentos/ci --workspace /opt/agentos/input --script /opt/agentos/observe.py --cycles 10
/opt/agentos/agentos pause --state /var/lib/agentos/ci
chown -R agentos:agentos /var/lib/agentos/ci
printf '%s\n' 'AGENTOS_LISTEN=127.0.0.1:18089' 'AGENTOS_API_TOKEN=ci-supervisor-token-0123456789abcdef' > /etc/agentos/ci.env
chmod 600 /etc/agentos/ci.env
systemctl daemon-reload
systemctl start agentos@ci.service
old_pid=$(systemctl show --value --property MainPID agentos@ci.service)
[[ "$old_pid" != 0 ]]
systemctl kill --kill-whom=main --signal=SIGKILL agentos@ci.service
for attempt in $(seq 1 20); do
    new_pid=$(systemctl show --value --property MainPID agentos@ci.service)
    if [[ "$new_pid" != 0 && "$new_pid" != "$old_pid" ]]; then break; fi
    sleep 1
done
[[ "$new_pid" != 0 && "$new_pid" != "$old_pid" ]]
systemctl is-active --quiet agentos@ci.service
python3 - <<'PY'
import json
from pathlib import Path
state=json.loads(Path('/var/lib/agentos/ci/state.json').read_text())
assert state['status']=='paused' and state['completed']==0
PY
systemctl start agentos-backup@ci.service
systemctl is-active --quiet agentos@ci.service
python3 - <<'PY'
import json, subprocess
from pathlib import Path
archives=list(Path('/var/backups/agentos/ci').glob('*.tar'))
assert len(archives)==1
receipt=json.loads(Path(str(archives[0])+'.receipt.json').read_text())
subprocess.run(['/opt/agentos/agentos','backup-verify','--archive',str(archives[0]),'--sha256',receipt['sha256']],check=True)
state=json.loads(Path('/var/lib/agentos/ci/state.json').read_text())
assert state['status']=='paused' and state['completed']==0
PY
