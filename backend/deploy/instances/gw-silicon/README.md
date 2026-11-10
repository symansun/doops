# Silicon 0.2.42 socket-controller upgrade

Target `gw-silicon` is `doops-silicon/vm-49`. It runs a standalone Docker
Agent with host networking, `privileged=true`, and an audited Docker socket
mount, but the configured SSH endpoint at `49.51.244.8:2022` has no usable
handshake. The socket is therefore used only to start a short-lived sibling
controller container. The current Agent never replaces its own container.

The controller source is `backend/scripts/release-host-agent.py` from this
repository. It must be started with `--socket-controller` and the exact
`DOOPS_SOCKET_CONTROLLER=1` environment marker. Without both, containerized
execution fails closed. The controller keeps the existing container command,
environment, mounts, network, PID, privilege, restart policy, and model source.
It verifies the candidate labels and `/app/doops-agent -version` before
stopping the old container, then probes the replacement through Docker. Its
state and lock directories are mounted from the host and remain available
after the old Agent reconnects.

Fixed release inputs:

- DoOps `v1.3.123`, source commit
  `8683d67174e19a1750e9b19de99a0816cdb8ea79`
- DoAgent `0.2.42`
- Candidate image
  `docker.cnb.cool/l8ai/ai/doops.sh@sha256:fd20c510411d9dde8e2061ed390e3b91877781ade2d4400a39b715280524b02a`
- Existing container: `doops-agent`
- Gateway identity: `doops-silicon/vm-49`

Before starting the controller, use the audited Gateway path to prepare the
candidate image with Docker and deliver the verified controller source to the
Agent's `/root` host bind. Confirm the remote source hash matches the committed
file. Resolve the host path for that bind from `docker inspect doops-agent`;
the current path is `/home/ubuntu/doops-agent-candidate-home-20260927`.

Run a real read-only dry-run in a disposable sibling controller first. The
controller image may be the current image, whose local config digest is
`sha256:280c77f34fd66538891ee36ba67e3160df830ee8ac4608443230ab10e52fa559`.
The sibling needs only the Docker socket, the controller source, the host
state directory, `/run/lock`, and the host Docker CLI binary. The controller
must use `--network none`; it talks to Docker over the socket and does not
need network access.

The apply controller is a single-use detached container with a bounded
runtime. It must write a `state.json` receipt under
`/var/lib/doops-host-releases/doops-agent/<session>/`. The operator waits for
the controller's terminal Docker status and then verifies through Gateway:

1. the exact target route reports the fixed DoOps commit;
2. the replacement container ID differs from the old ID and is running on the
   pinned image;
3. `do-agent --version`, ACP health on port `7750`, foreground/background
   execution, and structured SSH probes pass;
4. one real read-only model tool call returns marker
   `RELEASE_SMOKE_SILICON_042`;
5. the receipt reaches `phase=host_ready` and the old container remains under
   its exact rollback name.

If any post-replacement acceptance fails, invoke the same controller with
`--rollback` and the same controller ID/session. Do not replay an unknown
Docker launch or start a second controller while the first receipt is
non-terminal.

## Observed result on 2026-11-08

The apply controller exited 0 at 09:50:56Z and reported `host_upgrade_ready`.
The replacement is
`537b481436243cb61592780d589cba59d708a6d5b96045591190b1737ac303eb`.
Its image, runtime versions, ACP port 7750, and Agent readiness port **42223**
were verified. The original container remains under the documented rollback
name. Do not launch the controller again for this release.

Functional acceptance remains incomplete: first delivery of an active
`modelProvider` fails with `credential_secret_get_failed` on this host without
Kubernetes. Generic credential cleanup attempts a Kubernetes Secret read when
no native model delivery record exists. The attempted binding was revoked;
neither a fabricated record nor a foreign kubeconfig was installed.
See the internal 2026-11-08 DoAgent 0.2.42 all-environments archive for the
reproduction, source path, current evidence, and required repair.
`host_upgrade_ready` alone does not mean the model smoke passed.
