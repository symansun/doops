# Unicom 0.2.42 one-time cutover

Target `unicom-k8s` is `doops-unicom/unicom-k8s`, namespace `doops-system`.
This transaction replaces revision 5 of `doops-unicom` with `doops-agent`.
The old Pod stays running until independent Gateway and model acceptance passes.
The old release's image has disappeared from the registry; do not use an
in-place upgrade or the missing-baseline override.

`values.yaml` starts from the original `helm get values` output. It changes
the image, workload/selector names and home directory. The model was imported
from this target's original settings into `unicom-existing-doagent-model`;
the unused legacy settings mount is removed. `/var/lib/doops-agent` did not
exist at preflight, so it does not share the old container's home.

Prerequisites: successful CI for the source commit and pinned image; image
already cached on `k8s-master`; no active task on this target; original
ServiceAccount `doops-unicom-agent` retains its existing permissions to
manage Jobs, Deployments, Helm Secrets and ConfigMaps. No RBAC is created.

From this repository, upload the committed directory:

```sh
~/.local/bin/doops -session doagent-all-20261108 push --target unicom-k8s \
  --src "$PWD/backend/deploy/instances/unicom-k8s"
```

Then use the audited target to create immutable assets and start the independent
Job. These are single-use names; existing objects require inspection, not replay.

```sh
~/.local/bin/doops -session doagent-all-20261108 exec --target unicom-k8s --cmd '
  kubectl -n doops-system create configmap doagent-unicom-042-assets-r2 \
    --from-file=cutover.py=/root/ws/doagent-all-20261108/cutover.py \
    --from-file=values.yaml=/root/ws/doagent-all-20261108/values.yaml &&
  kubectl -n doops-system patch configmap doagent-unicom-042-assets-r2 \
    --type=merge -p "{\"immutable\":true}" &&
  kubectl create -f /root/ws/doagent-all-20261108/cutover-job.yaml'
```

Observe Job logs via `doops k8s logs`. `candidate-ready` names the new Pod and
UID; it does not mean acceptance. Before writing the receipt, the operator must:

1. Read `targets --target unicom-k8s` and require commit
   `8683d67174e19a1750e9b19de99a0816cdb8ea79`.
2. Run `capabilities --target unicom-k8s --require structuredSSH`.
3. Use Gateway `exec` to require the receipt's hostname, `do-agent 0.2.42`,
   `/app/doops-agent -version` and ACP `/health` status `ok`.
4. Run a read-only `doops ask` requiring a real `mcp_doops_k8s_read` of
   `deployment/doops-agent` in `doops-system` and marker
   `RELEASE_SMOKE_UNICOM_042`; require successful RPC terminal state and
   observed tool execution, and preserve its transcript.

Only after all four pass, the same operator writes acceptance through Gateway
`exec` (same target/session). This binds it to the actual Gateway-selected Pod;
the controller independently rechecks its UID, Ready status and image before
retiring the old release:

```sh
~/.local/bin/doops -session doagent-all-20261108 exec --target unicom-k8s --cmd 'python3 - <<'"'"'PY'"'"'
import json, socket, subprocess
base = ["kubectl", "-n", "doops-system"]
name = "doagent-unicom-042-cutover"
data = json.loads(subprocess.check_output(base + ["get", "configmap", name, "-o", "json"]))["data"]
assert data["phase"] == "candidate-ready"
assert socket.gethostname() == data["podName"]
pod = json.loads(subprocess.check_output(base + ["get", "pod", data["podName"], "-o", "json"]))
assert pod["metadata"]["uid"] == data["podUID"]
patch = {"data": {"verifiedPodUID": data["podUID"],
                  "acceptanceSession": "doagent-all-20261108",
                  "functionMarker": "RELEASE_SMOKE_UNICOM_042"}}
subprocess.run(base + ["patch", "configmap", name, "--type=merge", "-p", json.dumps(patch)], check=True)
PY'
```

Wait for Job success and receipt `phase=complete`; verify the original release
and Deployment are gone, then repeat Gateway version/health checks. The backup
and acceptance receipt remain in `ConfigMap/doagent-unicom-042-cutover`.
Failure before acceptance removes only the candidate and waits for the old
Deployment to recover. Missing acceptance expires after 600 seconds. Failure
during old cleanup retains the accepted candidate and reports it explicitly;
never restart this single-use Job against changed state.
