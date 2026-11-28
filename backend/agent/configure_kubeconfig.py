#!/usr/bin/env python3
import ipaddress
import os
import sys
from pathlib import Path

import yaml


def _named_entry(items, name, subject):
    if not isinstance(items, list):
        raise ValueError(f"kubeconfig {subject} must be a list")
    matches = [item for item in items if isinstance(item, dict) and item.get("name") == name]
    if len(matches) != 1:
        raise ValueError(f"kubeconfig {subject} must contain exactly one {name!r} entry")
    return matches[0]


def configure_kubeconfig(source, destination, service_host, service_port):
    source = Path(source)
    destination = Path(destination)
    host = str(ipaddress.ip_address(str(service_host).strip()))
    port = int(str(service_port).strip())
    if port < 1 or port > 65535:
        raise ValueError("Kubernetes service port is invalid")

    config = yaml.safe_load(source.read_text(encoding="utf-8"))
    if not isinstance(config, dict):
        raise ValueError("kubeconfig root must be an object")
    current_context = config.get("current-context")
    if not isinstance(current_context, str) or not current_context:
        raise ValueError("kubeconfig current-context is required")
    context_entry = _named_entry(config.get("contexts"), current_context, "contexts")
    context = context_entry.get("context")
    if not isinstance(context, dict):
        raise ValueError("kubeconfig current context is invalid")
    cluster_name = context.get("cluster")
    if not isinstance(cluster_name, str) or not cluster_name:
        raise ValueError("kubeconfig current context cluster is required")
    cluster_entry = _named_entry(config.get("clusters"), cluster_name, "clusters")
    cluster = cluster_entry.get("cluster")
    if not isinstance(cluster, dict):
        raise ValueError("kubeconfig current cluster is invalid")

    address = f"[{host}]" if ":" in host else host
    cluster["server"] = f"https://{address}:{port}"

    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".tmp")
    temporary.write_text(
        yaml.safe_dump(config, sort_keys=False),
        encoding="utf-8",
    )
    os.chmod(temporary, 0o600)
    os.replace(temporary, destination)


SERVICE_ACCOUNT_DIR = Path("/var/run/secrets/kubernetes.io/serviceaccount")


def configure_service_account_kubeconfig(
    destination, service_host, service_port, account_dir=SERVICE_ACCOUNT_DIR
):
    """Write a kubeconfig that authenticates as the Pod's own ServiceAccount."""
    account_dir = Path(account_dir)
    destination = Path(destination)
    host = str(ipaddress.ip_address(str(service_host).strip()))
    port = int(str(service_port).strip())
    if port < 1 or port > 65535:
        raise ValueError("Kubernetes service port is invalid")
    token_path = account_dir / "token"
    ca_path = account_dir / "ca.crt"
    if not token_path.is_file() or not ca_path.is_file():
        raise ValueError("ServiceAccount token and ca.crt are required")
    address = f"[{host}]" if ":" in host else host
    # tokenFile keeps the rotated projected token instead of copying a snapshot.
    config = {
        "apiVersion": "v1",
        "kind": "Config",
        "clusters": [{
            "name": "in-cluster",
            "cluster": {
                "server": f"https://{address}:{port}",
                "certificate-authority": str(ca_path),
            },
        }],
        "users": [{"name": "service-account", "user": {"tokenFile": str(token_path)}}],
        "contexts": [{
            "name": "in-cluster",
            "context": {"cluster": "in-cluster", "user": "service-account"},
        }],
        "current-context": "in-cluster",
    }
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".tmp")
    temporary.write_text(yaml.safe_dump(config, sort_keys=False), encoding="utf-8")
    os.chmod(temporary, 0o600)
    os.replace(temporary, destination)


def main():
    if len(sys.argv) == 5 and sys.argv[1] == "--service-account":
        configure_service_account_kubeconfig(*sys.argv[2:])
        return
    if len(sys.argv) != 5:
        raise SystemExit(
            "usage: configure_kubeconfig.py SOURCE DESTINATION SERVICE_HOST SERVICE_PORT\n"
            "       configure_kubeconfig.py --service-account DESTINATION SERVICE_HOST SERVICE_PORT"
        )
    configure_kubeconfig(*sys.argv[1:])


if __name__ == "__main__":
    main()
