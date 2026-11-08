# Unicom Compose 0.2.42 transaction

This single-use controller upgrades the existing `doops-unicom/unicom-1`
Compose service, without migrating it to Kubernetes or removing Compose
ownership labels. It runs as a host systemd service, outside the Agent container.
The operator enters the host through audited DoOps `nsenter`; no SSH is needed.

The recorded baseline is container
`fc58d9a6253b1f29b9185b2cbafcfa04e84a0d82b6d10018866963b6745c7335`,
project/service `doops-agent`, source `/opt/doops-agent/compose.yaml`.
The exact old image must exist in Docker and receives a retained rollback tag
before replacement. Compose recreates the container; rollback therefore restores
the exact old image and configuration in a new container, not the original ID.
The original compose source is backed up privately on the host.

The only configuration changes are the pinned image in `compose.yaml` and
persistence of the existing kubeconfig. The live container currently has an
untracked symlink `/root/.kube/config -> /host/root/.kube/config`. Its target
was byte-compared with `/etc/kubernetes/admin.conf`. The controller repeats that
check before adding a read-only mount of the same file; it does not grant a new
Kubernetes identity. Host networking, host PID, privilege, model source and
existing named volumes remain unchanged. The normalized Compose configuration
must otherwise match exactly.

Before apply, require successful CI for the controller commit and the pinned
DoOps v1.3.123 image, and a successful live dry-run. `docker pull` has already
completed with terminal exit 0 and digest `fd20c510...`. This controller never
builds or pulls an image.

The old Agent cannot materialize COS workspaces (`cos_workspace_not_updated`),
so this small script is delivered with the existing Gateway text-write protocol.
This is explicit file transport, not a downgraded CLI or a second deployment path:

```sh
~/.local/bin/doops -session doagent-unicom-compose-20261108 write \
  --target unicom-1 --storage gateway --path compose-upgrade.py \
  --file "$PWD/backend/deploy/instances/unicom-1/compose-upgrade.py"
~/.local/bin/doops -session doagent-unicom-compose-20261108 exec --target unicom-1 \
  --cmd 'nsenter -t 1 -m -u -i -n -- python3 /data/docker/volumes/doops-agent_doops-workspace/_data/doagent-unicom-compose-20261108/compose-upgrade.py --dry-run'
```

Compare the remote script SHA256 with the committed file. Then start the
independent, bounded controller through `unicom-k8s`, the verified management
channel on the same host. It remains available while the Compose Agent is replaced:

```sh
~/.local/bin/doops -session doagent-unicom-compose-20261108 exec --target unicom-k8s \
  --cmd 'nsenter -t 1 -m -u -i -n -- systemd-run --unit=doagent-unicom1-042-20261108 --property=RuntimeMaxSec=1200 --property=TimeoutStopSec=300 /usr/bin/python3 /data/docker/volumes/doops-agent_doops-workspace/_data/doagent-unicom-compose-20261108/compose-upgrade.py --apply'
```

Never replay an unknown launch result. `unicom-k8s` is an independently verified
management channel on the same host; it can read the systemd unit and the
metadata receipt at `/var/lib/doops-releases/unicom1-042-20261108/receipt.json`
through the same audited `nsenter` path while the Compose Agent is replaced.

The old Agent rejects the modelProvider Secret-import format. A temporary
`doops-system/doagent-unicom1-upgrade-source-20261108` Secret was created from
this container's own `/opt/doagent_config/settings.json`; only credential
metadata `cred_f26c3073d35ba2e447ccea3b` was created. Once the new Agent is online:

```sh
~/.local/bin/doops -session doagent-unicom-compose-20261108 credential configure \
  --target unicom-1 --namespace doops-system \
  --id cred_f26c3073d35ba2e447ccea3b --use modelProvider \
  --from-secret doagent-unicom1-upgrade-source-20261108 --keys settings.json
```

Require verify/promote success, then delete the temporary Secret. No credential
payload is returned or copied to this repository.

Within 600 seconds of `candidate-ready`, independently require the exact Gateway
release commit, live DoAgent 0.2.42/ACP health, structuredSSH capability, real
foreground/background exec terminal success, and a real read-only model K8s
tool call with marker `RELEASE_SMOKE_UNICOM1_042`. Only then write
`acceptance.json` in the receipt directory through audited host `exec`:

```json
{
  "containerID": "<exact candidate-ready containerID, checked against Docker inspect>",
  "acceptanceSession": "doagent-unicom-compose-20261108",
  "functionMarker": "RELEASE_SMOKE_UNICOM1_042"
}
```

Require systemd terminal exit 0, receipt `phase=complete`, unchanged running
container ID, exact image and normalized Compose config matching this directory.
Failure or missing acceptance restores the recorded old image using
`--pull never` and verifies container health; independently verify Gateway
reconnection afterwards. Recovery failure is explicit, never a successful
rollback. The receipt directory and retained rollback image remain available;
existing receipts prevent automatic replay.
