#!/usr/bin/env python3
"""Configure a local Harbor to replicate images from an upstream registry.

  plan    read-only: report what the run would create and what it would reuse.
  run     converge the registry endpoint, project, immutable tag rule and one
          replication policy per upstream repository, then optionally trigger
          the copy and compare digests with the upstream.

Run it inside the target cluster through the audited Gateway path:

  doops -session <s> exec --target <t> --cmd 'python3 /root/ws/harbor-replication.py run \
    --harbor-url https://core.harbor.svc:443 --harbor-credential <credential-id> \
    --upstream-url https://docker.cnb.cool --upstream-credential <credential-id> \
    --credential-namespace harbor-system --project agentcloud \
    --repository l8ai/agentcloud/backend --tag-pattern "git-*"'

The script is idempotent: a second run against the same cluster converges to the
same objects and reports each one as reused. It never deletes a registry
endpoint, project, policy or tag, and it never writes, revokes or rotates a
credential. The only credential inputs are the ids of DoOps managed credentials
whose values are read from their materialized Secret inside the cluster;
payloads never reach argv, a file on disk, or the output.
"""
import argparse
import base64
import json
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

MANAGED_BY = "doops"
CREDENTIAL_ID_LABEL = "doops.sh/credential-id"
MANIFEST_TYPES = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))


class HarborError(Exception):
    """A Harbor call failed. The message carries no credential payload."""


class UpstreamError(Exception):
    """The upstream registry could not be read. No payload values in the message."""


class CredentialError(Exception):
    """A managed credential could not be resolved or lacked a required key."""


def log(message):
    print(message, flush=True)


# ---------------------------------------------------------------- credentials


def resolve_managed_credential(identifier, namespace):
    """Return {key: value} for one DoOps managed credential in the cluster.

    The materialized Secret name is a digest of the credential and grant ids, so
    the credential is located by its `doops.sh/credential-id` label. The Secret
    name is accepted too, for a caller that already knows it. Only key names and
    credential ids are ever reported.
    """
    command = [
        "kubectl", "get", "secret", "-n", namespace,
        "-l", "app.kubernetes.io/managed-by=" + MANAGED_BY, "-o", "json",
    ]
    try:
        completed = subprocess.run(
            command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False
        )
    except FileNotFoundError:
        raise CredentialError("kubectl is required to read a managed credential")
    if completed.returncode != 0:
        raise CredentialError(
            "kubectl get secret -n " + namespace + " failed: "
            + completed.stderr.decode("utf-8", "replace").strip()[:200]
        )
    try:
        secrets = json.loads(completed.stdout.decode("utf-8")).get("items") or []
    except (json.JSONDecodeError, UnicodeDecodeError):
        raise CredentialError("cannot parse the managed credential listing")

    available = []
    for secret in secrets:
        metadata = secret.get("metadata") or {}
        credential_id = ((metadata.get("labels") or {}).get(CREDENTIAL_ID_LABEL) or "").strip()
        if not credential_id:
            continue
        available.append(credential_id)
        if identifier in (credential_id, metadata.get("name")):
            return _decode_secret(secret)
    if not available:
        raise CredentialError(
            "no DoOps managed credential is materialized in namespace " + namespace
        )
    raise CredentialError(
        "managed credential " + identifier + " is not materialized in " + namespace
        + "; materialized credential ids: " + ", ".join(sorted(available))
    )


def _decode_secret(secret):
    values = {}
    for key, encoded in (secret.get("data") or {}).items():
        try:
            values[key] = base64.b64decode(encoded).decode("utf-8", "replace")
        except (ValueError, TypeError):
            raise CredentialError("managed credential holds an unreadable value")
    if not values:
        raise CredentialError("managed credential holds no keys")
    return values


def require_key(values, key, label):
    if not values.get(key):
        raise CredentialError(
            label + " has no key " + key + "; available keys: " + ", ".join(sorted(values))
        )
    return values[key]


# ---------------------------------------------------------------- Harbor API


class HarborClient:
    """Minimal Harbor v2 client.

    The password is attached as a request header built in memory, so it never
    reaches a command line, a URL or a temporary file.
    """

    def __init__(self, base_url, username, password, insecure=False, timeout=60):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self._auth = "Basic " + base64.b64encode(
            (username + ":" + password).encode("utf-8")
        ).decode("ascii")
        context = ssl.create_default_context()
        if insecure:
            context.check_hostname = False
            context.verify_mode = ssl.CERT_NONE
        self._opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=context)
        )

    def _request(self, method, path, body=None, query=None):
        url = self.base_url + "/api/v2.0" + path
        if query:
            url = url + "?" + urllib.parse.urlencode(query)
        data = None
        headers = {"Accept": "application/json", "Authorization": self._auth}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with self._opener.open(request, timeout=self.timeout) as response:
                raw = response.read()
                location = response.headers.get("Location")
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", "replace").strip()[:300]
            raise HarborError(
                "harbor " + method + " " + path + " -> " + str(error.code) + " " + detail
            )
        except (urllib.error.URLError, OSError) as error:
            raise HarborError("harbor " + method + " " + path + " unreachable: " + str(error))
        if not raw:
            # Harbor answers a create with 201 and an empty body, and puts the
            # new object's id in the Location header. Drop it here so the
            # caller does not have to know that.
            if location:
                created = {"id": self._id_from_location(location)}
                return created
            return {}
        try:
            return json.loads(raw.decode("utf-8"))
        except (json.JSONDecodeError, UnicodeDecodeError):
            raise HarborError("harbor " + method + " " + path + " returned a non-JSON body")

    @staticmethod
    def _id_from_location(location):
        """Pull the object id out of a Location header such as
        /api/v2.0/replication/executions/1922."""
        tail = location.rstrip("/").rsplit("/", 1)[-1]
        try:
            return int(tail)
        except ValueError:
            return 0

    def get(self, path, query=None):
        return self._request("GET", path, query=query)

    def post(self, path, body):
        return self._request("POST", path, body=body)

    def put(self, path, body):
        return self._request("PUT", path, body=body)

    def system_info(self):
        return self.get("/systeminfo")

    def registries(self):
        found = self.get("/registries", query={"page": 1, "page_size": 100})
        return found if isinstance(found, list) else []

    def registry(self, registry_id):
        return self.get("/registries/" + str(registry_id))

    def projects(self):
        found = []
        page = 1
        while True:
            batch = self.get("/projects", query={"page": page, "page_size": 100})
            if not isinstance(batch, list) or not batch:
                break
            found.extend(batch)
            if len(batch) < 100:
                break
            page += 1
        return found

    def policies(self):
        found = self.get("/replication/policies", query={"page": 1, "page_size": 100})
        return found if isinstance(found, list) else []

    def policy(self, policy_id):
        return self.get("/replication/policies/" + str(policy_id))

    def execution(self, execution_id):
        return self.get("/replication/executions/" + str(execution_id))

    def immutable_rules(self, project):
        found = self.get(
            "/projects/" + urllib.parse.quote(project) + "/immutabletagrules",
            query={"page": 1, "page_size": 100},
        )
        return found if isinstance(found, list) else []

    def artifacts(self, project, repository):
        found = self.get(
            "/projects/" + urllib.parse.quote(project)
            + "/repositories/" + urllib.parse.quote(repository, safe="")
            + "/artifacts",
            query={"page": 1, "page_size": 100},
        )
        return found if isinstance(found, list) else []


# ---------------------------------------------------------- registry endpoint


def find_registry(client, name):
    for registry in client.registries():
        if registry.get("name") == name:
            return registry.get("id")
    return None


def ensure_registry_endpoint(client, name, url, access_key, access_secret, insecure):
    """Create the upstream endpoint, or reuse it and re-check its credential.

    Harbor answers a PUT whose credential it did not store with 200, so the
    endpoint is always read back and the credential presence is asserted.
    """
    registry_id = find_registry(client, name)
    if registry_id is not None:
        current = client.registry(registry_id)
        credential = current.get("credential") or {}
        if not credential.get("access_key") or not credential.get("access_secret"):
            log("registry " + name + " exists without a credential; writing it")
            client.put("/registries/" + str(registry_id), {
                "name": name,
                "type": current.get("type") or "docker-registry",
                "url": current.get("url") or url,
                "insecure": bool(current.get("insecure", insecure)),
                "credential_type": "basic",
                "access_key": access_key,
                "access_secret": access_secret,
            })
            current = client.registry(registry_id)
            credential = current.get("credential") or {}
            if not credential.get("access_key") or not credential.get("access_secret"):
                raise HarborError(
                    "registry " + name + " did not store the credential; refusing to continue"
                )
            log("registry " + name + " credential written")
        else:
            log("registry " + name + " reused (id=" + str(registry_id) + ")")
        status = current.get("status")
        if status and status != "healthy":
            raise HarborError("registry " + name + " is " + str(status) + ", not healthy")
        return registry_id, False

    created = client.post("/registries", {
        "name": name,
        "description": "Upstream registry configured by harbor-replication.py",
        "type": "docker-registry",
        "url": url,
        "insecure": bool(insecure),
        "credential_type": "basic",
        "access_key": access_key,
        "access_secret": access_secret,
    })
    registry_id = created.get("id")
    if not isinstance(registry_id, int) or registry_id <= 0:
        # Harbor returns an empty body on some versions; resolve by name.
        registry_id = find_registry(client, name)
    if not isinstance(registry_id, int) or registry_id <= 0:
        raise HarborError("registry " + name + " was created but cannot be found by name")
    current = client.registry(registry_id)
    credential = current.get("credential") or {}
    if not credential.get("access_key") or not credential.get("access_secret"):
        raise HarborError("registry " + name + " was created without a credential")
    status = current.get("status")
    if status and status != "healthy":
        raise HarborError("registry " + name + " is " + str(status) + ", not healthy")
    log("registry " + name + " created (id=" + str(registry_id) + ")")
    return registry_id, True


# ------------------------------------------------------------------- project


def find_project(client, name):
    for project in client.projects():
        if project.get("name") == name:
            return project.get("project_id")
    return None


def ensure_project(client, name, public=False):
    project_id = find_project(client, name)
    if project_id is not None:
        log("project " + name + " reused (id=" + str(project_id) + ")")
        return project_id, False
    # registry_id must be absent: 0 is rejected and any real id makes a proxy cache.
    client.post("/projects", {
        "project_name": name,
        "public": bool(public),
        "metadata": {"public": "true" if public else "false"},
        "storage_limit": -1,
    })
    project_id = find_project(client, name)
    if not isinstance(project_id, int) or project_id <= 0:
        raise HarborError("project " + name + " was created but cannot be found by name")
    log("project " + name + " created (id=" + str(project_id) + ")")
    return project_id, True


# --------------------------------------------------------- replication policy


def policy_name_for(prefix, project, repository):
    """Name one policy after the project and the repository it copies.

    The name must be stable across runs, because reuse is decided by name: a
    second run that derived a different name would create a duplicate policy
    for a repository that already has one. The upstream path is irrelevant to
    the name, since the copy is flattened to its last segment anyway, and a
    leading project name in that segment is dropped so that
    `aiedulab/zhiyong/education/zhiyong-frontend` under project `zhiyong`
    reads `cnb-zhiyong-frontend` rather than `cnb-zhiyong-zhiyong-frontend`.
    """
    base = repository.strip("/").split("/")[-1]
    if base.startswith(project + "-"):
        base = base[len(project) + 1:]
    return "-".join(part for part in (prefix, project, base) if part)


def policy_body(name, registry_id, project, repository, tag_pattern):
    # Harbor rejects a body that fills in both registry sides. This is a pull
    # policy, so only the source endpoint is named; the destination is a
    # namespace, flattened across every path level by replace_count -1.
    return {
        "name": name,
        "description": "Pull " + repository + " " + tag_pattern,
        "enabled": True,
        "src_registry": {"id": registry_id},
        "dest_namespace": project,
        "dest_namespace_replace_count": -1,
        "filters": [
            {"type": "name", "value": repository},
            {"type": "tag", "value": tag_pattern},
        ],
        "override": False,
        "deletion": False,
        "trigger": {"type": "manual", "trigger_settings": {"cron": ""}},
    }


def policy_matches(policy, project, repository, tag_pattern):
    filters = {f.get("type"): f.get("value") for f in (policy.get("filters") or [])}
    return (
        filters.get("name") == repository
        and filters.get("tag") == tag_pattern
        and policy.get("dest_namespace") == project
        and policy.get("enabled") is True
    )


def find_policy(client, name):
    for policy in client.policies():
        if policy.get("name") == name:
            return policy
    return None


def ensure_policy(client, name, registry_id, project, repository, tag_pattern):
    body = policy_body(name, registry_id, project, repository, tag_pattern)
    existing = find_policy(client, name)
    if existing is not None:
        policy_id = existing["id"]
        current = client.policy(policy_id)
        if policy_matches(current, project, repository, tag_pattern):
            log("policy " + name + " reused (id=" + str(policy_id) + ")")
            return policy_id, False
        log("policy " + name + " has different filters; updating it")
        client.put("/replication/policies/" + str(policy_id), body)
        updated = client.policy(policy_id)
        if not policy_matches(updated, project, repository, tag_pattern):
            raise HarborError("policy " + name + " did not accept the requested filters")
        log("policy " + name + " updated (id=" + str(policy_id) + ")")
        return policy_id, False

    # A 201 does not prove the policy is usable; resolve its real id by name.
    client.post("/replication/policies", body)
    created = find_policy(client, name)
    if created is None or not isinstance(created.get("id"), int) or created["id"] <= 0:
        raise HarborError("policy " + name + " was created but cannot be found by name")
    log("policy " + name + " created (id=" + str(created["id"]) + ")")
    return created["id"], True


# ------------------------------------------------------------ immutable rule


def ensure_immutable_rule(client, project, tag_pattern):
    for rule in client.immutable_rules(project):
        patterns = [s.get("pattern") for s in (rule.get("tag_selectors") or [])]
        if tag_pattern in patterns:
            log("immutable rule " + project + " " + tag_pattern + " reused (id="
                + str(rule.get("id")) + ")")
            return rule.get("id"), False
    client.post("/projects/" + urllib.parse.quote(project) + "/immutabletagrules", {
        "disabled": False,
        "action": "immutable",
        "template": "immutable_template",
        "tag_selectors": [
            {"kind": "doublestar", "decoration": "matches", "pattern": tag_pattern}
        ],
        "scope_selectors": {
            "repository": [
                {"kind": "doublestar", "decoration": "repoMatches", "pattern": "**"}
            ]
        },
    })
    for rule in client.immutable_rules(project):
        patterns = [s.get("pattern") for s in (rule.get("tag_selectors") or [])]
        if tag_pattern in patterns:
            log("immutable rule " + project + " " + tag_pattern + " created (id="
                + str(rule.get("id")) + ")")
            return rule.get("id"), True
    raise HarborError(
        "immutable rule " + tag_pattern + " was created but cannot be found in " + project
    )


# ------------------------------------------------------- digest verification


class UpstreamRegistry:
    """Read-only client for the upstream registry.

    The registry answers an unauthenticated request with a Bearer challenge; the
    token is exchanged once per repository and carried in memory. The user name
    and token never reach a command line, a URL or the output.
    """

    def __init__(self, url, username, token, timeout=60):
        self.url = url.rstrip("/")
        self.username = username
        self.token = token
        self.timeout = timeout
        self._basic = "Basic " + base64.b64encode(
            (username + ":" + token).encode("utf-8")
        ).decode("ascii")
        self._opener = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ssl.create_default_context())
        )
        self._realm = None
        self._service = ""

    def _challenge(self):
        """Read the registry's own Bearer realm and service, once.

        The realm is taken from the registry instead of being hardcoded, so the
        script works against any v2 registry that uses token auth.
        """
        if self._realm is not None:
            return self._realm, self._service
        try:
            request = urllib.request.Request(self.url + "/v2/")
            with self._opener.open(request, timeout=self.timeout):
                pass
            raise UpstreamError(self.url + " did not ask for authentication")
        except urllib.error.HTTPError as error:
            if error.code != 401:
                raise UpstreamError(
                    self.url + "/v2/ answered " + str(error.code) + ", not a token challenge"
                )
            header = error.headers.get("WWW-Authenticate", "")
        except (urllib.error.URLError, OSError) as error:
            raise UpstreamError(self.url + " is unreachable: " + str(error))
        fields = {}
        for item in header.replace("Bearer ", "", 1).split(","):
            key, _, value = item.partition("=")
            if value:
                fields[key.strip()] = value.strip().strip('"')
        realm = fields.get("realm")
        if not realm:
            raise UpstreamError(self.url + " returned no token realm")
        self._realm = realm
        self._service = fields.get("service", "")
        return self._realm, self._service

    def bearer_for(self, repository):
        """Exchange the credentials for a pull token scoped to one repository."""
        realm, service = self._challenge()
        query = {
            "service": service,
            "scope": "repository:" + repository.strip("/") + ":pull",
        }
        request = urllib.request.Request(realm + "?" + urllib.parse.urlencode(query))
        request.add_header("Authorization", self._basic)
        try:
            with self._opener.open(request, timeout=self.timeout) as response:
                payload = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            raise UpstreamError(
                "the upstream rejected the credential for " + repository
                + " (" + str(error.code) + ")"
            )
        except (urllib.error.URLError, OSError, json.JSONDecodeError) as error:
            raise UpstreamError("cannot obtain an upstream token: " + str(error))
        bearer = payload.get("token") or payload.get("access_token")
        if not bearer:
            raise UpstreamError("the upstream returned no token for " + repository)
        return bearer

    def tags(self, repository, bearer):
        """Return the tag list the upstream currently serves."""
        request = urllib.request.Request(
            self.url + "/v2/" + repository.strip("/") + "/tags/list"
        )
        request.add_header("Authorization", "Bearer " + bearer)
        with self._opener.open(request, timeout=self.timeout) as response:
            payload = json.loads(response.read().decode("utf-8"))
        return list(payload.get("tags") or [])

    def digest(self, repository, tag, bearer):
        """Return the upstream content digest for one tag, or None."""
        request = urllib.request.Request(
            self.url + "/v2/" + repository.strip("/") + "/manifests/" + tag, method="HEAD"
        )
        request.add_header("Authorization", "Bearer " + bearer)
        request.add_header("Accept", MANIFEST_TYPES)
        try:
            with self._opener.open(request, timeout=self.timeout) as response:
                digest = response.headers.get("Docker-Content-Digest")
                return digest.strip() if digest else None
        except (urllib.error.HTTPError, urllib.error.URLError, OSError):
            return None


def harbor_digests(client, project, repository):
    """Return {tag: digest} for one Harbor repository.

    Harbor v2.14 has no repository-level tag endpoint; tags come from each
    artifact's own tag list.
    """
    digests = {}
    for artifact in client.artifacts(project, repository):
        digest = artifact.get("digest") or ""
        for tag in artifact.get("tags") or []:
            if tag.get("name"):
                digests[tag["name"]] = digest
    return digests


def tag_matches(tag, pattern):
    if pattern.endswith("*"):
        return tag.startswith(pattern[:-1])
    return tag == pattern


def verify_repository(client, project, harbor_repository, upstream, upstream_repository,
                      tag_pattern):
    """Compare the upstream's matching tags with the Harbor copies.

    The upstream tag list drives the comparison, not the Harbor one. Walking
    Harbor instead would miss the failure that actually matters: a tag the
    upstream publishes and the replication never copied. Both directions are
    reported, because each means something different:

      missing   the upstream serves the tag and Harbor does not. The copy has
                not happened yet, so the caller fails.
      mismatch  both serve the tag under different digests. Either side was
                overwritten, so the caller fails.
      extra     Harbor keeps a tag the upstream no longer serves. The upstream
                runs its own tag retention, so a kept copy is not a fault and
                is only reported.
    """
    local = harbor_digests(client, project, harbor_repository)
    bearer = upstream.bearer_for(upstream_repository)
    try:
        remote_tags = [t for t in upstream.tags(upstream_repository, bearer)
                       if tag_matches(t, tag_pattern)]
    except (urllib.error.HTTPError, urllib.error.URLError, OSError,
            json.JSONDecodeError) as error:
        raise UpstreamError(
            "cannot list upstream tags for " + upstream_repository + ": " + str(error)
        )
    matched = []
    mismatched = []
    missing = []
    for tag in sorted(remote_tags):
        remote = upstream.digest(upstream_repository, tag, bearer)
        if tag not in local:
            missing.append(tag)
        elif remote and remote == local[tag]:
            matched.append(tag)
        elif remote:
            mismatched.append({"tag": tag, "harbor": local[tag], "upstream": remote})
    extra = sorted(tag for tag in local
                   if tag_matches(tag, tag_pattern) and tag not in set(remote_tags))
    return {
        "repository": upstream_repository,
        "harbor_repository": harbor_repository,
        "upstream_tags": len(remote_tags),
        "harbor_tags": len(local),
        "matched": len(matched),
        "mismatched": mismatched,
        "missing": missing,
        "extra": extra,
        "ok": not mismatched and not missing,
    }


# -------------------------------------------------------------- trigger/wait


def trigger_replication(client, policy_id):
    # POST /replication/policies/{id}/executions returns no id on v2.14.
    created = client.post("/replication/executions", {"policy_id": policy_id})
    execution_id = created.get("id")
    if not isinstance(execution_id, int) or execution_id <= 0:
        raise HarborError("triggering policy " + str(policy_id) + " returned no execution id")
    log("execution " + str(execution_id) + " triggered for policy " + str(policy_id))
    return execution_id


def wait_for_execution(client, execution_id, timeout_seconds, poll_seconds=15):
    """Poll one execution until it leaves InProgress.

    A slow copy is not a failed copy: the upstream link runs at tens of KB/s, so
    a timeout here reports an unfinished execution rather than declaring failure.
    """
    deadline = time.time() + timeout_seconds
    while True:
        state = client.execution(execution_id)
        if state.get("status") != "InProgress":
            return state
        if time.time() >= deadline:
            raise HarborError(
                "execution " + str(execution_id) + " is still running after "
                + str(timeout_seconds) + "s; the copy is slow, not failed. Check "
                "GET /replication/executions/" + str(execution_id) + "/tasks before retrying."
            )
        time.sleep(poll_seconds)


# ------------------------------------------------------------------- command


def harbor_repository_for(upstream_repository):
    # dest_namespace_replace_count -1 flattens every path level, so the copy
    # lands under the project as the last segment of the upstream path.
    return upstream_repository.strip("/").split("/")[-1]


def plan(client, args, registry_id):
    rows = []
    rows.append(("registry", args.registry_name,
                 "reuse" if registry_id is not None else "create"))
    rows.append(("project", args.project,
                 "reuse" if find_project(client, args.project) is not None else "create"))
    immutable_pattern = args.immutable_tag_pattern or args.tag_pattern
    patterns = []
    if find_project(client, args.project) is not None:
        for rule in client.immutable_rules(args.project):
            patterns.extend(s.get("pattern") for s in (rule.get("tag_selectors") or []))
    rows.append(("immutable", immutable_pattern,
                 "reuse" if immutable_pattern in patterns else "create"))
    for repository in args.repository:
        name = policy_name_for(args.policy_prefix, args.project, repository)
        existing = find_policy(client, name)
        action = "create"
        if existing is not None:
            current = client.policy(existing["id"])
            action = "reuse" if policy_matches(
                current, args.project, repository, args.tag_pattern
            ) else "update"
        rows.append(("policy", name, action))
    for kind, name, action in rows:
        log(action + " " + kind + " " + name)
    return [{"kind": k, "name": n, "action": a} for k, n, a in rows]


def run(client, args, upstream_user, upstream_token):
    registry_id, registry_created = ensure_registry_endpoint(
        client, args.registry_name, args.upstream_url,
        upstream_user, upstream_token, args.insecure,
    )
    project_id, project_created = ensure_project(client, args.project, public=args.project_public)
    immutable_id, immutable_created = ensure_immutable_rule(
        client, args.project, args.immutable_tag_pattern or args.tag_pattern
    )

    policies = []
    for repository in args.repository:
        name = policy_name_for(args.policy_prefix, args.project, repository)
        policy_id, created = ensure_policy(
            client, name, registry_id, args.project, repository, args.tag_pattern
        )
        policies.append({"repository": repository, "policy": name,
                         "policy_id": policy_id, "created": created})

    executions = []
    if args.trigger:
        for entry in policies:
            execution_id = trigger_replication(client, entry["policy_id"])
            state = wait_for_execution(client, execution_id, args.timeout)
            status = state.get("status")
            log("execution " + str(execution_id) + " " + entry["repository"] + " -> "
                + str(status) + " ok=" + str(state.get("succeed"))
                + " fail=" + str(state.get("failed")))
            executions.append({"repository": entry["repository"], "execution_id": execution_id,
                               "status": status, "succeeded": state.get("succeed"),
                               "failed": state.get("failed")})
            if status != "Succeed":
                raise HarborError(
                    "replication of " + entry["repository"] + " ended with " + str(status)
                )

    verifications = []
    if args.verify:
        upstream = UpstreamRegistry(args.upstream_url, upstream_user, upstream_token)
        for entry in policies:
            report = verify_repository(
                client, args.project, harbor_repository_for(entry["repository"]),
                upstream, entry["repository"], args.tag_pattern,
            )
            log("verify " + report["repository"]
                + ": upstream=" + str(report["upstream_tags"])
                + " matched=" + str(report["matched"])
                + " mismatched=" + str(len(report["mismatched"]))
                + " missing=" + str(len(report["missing"]))
                + " extra=" + str(len(report["extra"])))
            for item in report["mismatched"]:
                log("  MISMATCH " + item["tag"] + " harbor=" + item["harbor"]
                    + " upstream=" + item["upstream"])
            for tag in report["missing"]:
                log("  MISSING " + tag + " (upstream serves it, Harbor does not)")
            for tag in report["extra"]:
                log("  kept " + tag + " (upstream no longer serves it)")
            verifications.append(report)

    return {
        "registry": {"name": args.registry_name, "id": registry_id, "created": registry_created},
        "project": {"name": args.project, "id": project_id, "created": project_created},
        "immutable_rule": {"id": immutable_id, "created": immutable_created},
        "policies": policies,
        "executions": executions,
        "verifications": verifications,
    }


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description="Configure a local Harbor to replicate images from an upstream registry.",
    )
    parser.add_argument("command", choices=("plan", "run"))
    parser.add_argument("--harbor-url", required=True,
                        help="Local Harbor base URL, for example https://core.harbor.svc:443")
    parser.add_argument("--harbor-credential", required=True,
                        help="Managed credential id holding the local Harbor login")
    parser.add_argument("--harbor-username-key", default="HARBOR_ADMIN_USERNAME",
                        help="Credential key holding the Harbor user name (default: %(default)s)")
    parser.add_argument("--harbor-password-key", default="HARBOR_ADMIN_PASSWORD",
                        help="Credential key holding the Harbor password (default: %(default)s)")
    parser.add_argument("--upstream-url", required=True,
                        help="Upstream registry URL, for example https://docker.cnb.cool")
    parser.add_argument("--upstream-credential", required=True,
                        help="Managed credential id holding the upstream registry login")
    parser.add_argument("--upstream-username-key", default="CNB_USERNAME",
                        help="Credential key holding the upstream user name (default: %(default)s)")
    parser.add_argument("--upstream-token-key", default="CNB_TOKEN",
                        help="Credential key holding the upstream token (default: %(default)s)")
    parser.add_argument("--credential-namespace", required=True,
                        help="Namespace where the managed credentials are materialized")
    parser.add_argument("--registry-name", default="cnb-upstream",
                        help="Harbor registry endpoint name (default: %(default)s)")
    parser.add_argument("--policy-prefix", default="cnb",
                        help="Prefix for every policy name, which decides reuse "
                             "(default: %(default)s)")
    parser.add_argument("--project", required=True,
                        help="Harbor project that receives the copies")
    parser.add_argument("--project-public", action="store_true",
                        help="Create the project public instead of private")
    parser.add_argument("--repository", action="append", default=[], metavar="PATH",
                        help="Upstream repository path to replicate; repeatable")
    parser.add_argument("--tag-pattern", default="git-*",
                        help="Tag filter for every policy (default: %(default)s)")
    parser.add_argument("--immutable-tag-pattern",
                        help="Tag pattern to make immutable; defaults to --tag-pattern")
    parser.add_argument("--trigger", action="store_true",
                        help="Trigger each policy and wait for its execution")
    parser.add_argument("--verify", action="store_true",
                        help="Compare each Harbor tag digest with the upstream digest")
    parser.add_argument("--timeout", type=int, default=1800, metavar="SECONDS",
                        help="Wait per replication execution (default: %(default)s)")
    parser.add_argument("--insecure", action="store_true",
                        help="Skip TLS verification against the local Harbor")
    parser.add_argument("--json", action="store_true", help="Print the result as JSON")
    args = parser.parse_args(argv)
    if not args.repository:
        parser.error("at least one --repository is required")
    return args


def main(argv):
    args = parse_args(argv)

    harbor_values = resolve_managed_credential(args.harbor_credential, args.credential_namespace)
    upstream_values = resolve_managed_credential(
        args.upstream_credential, args.credential_namespace
    )
    harbor_user = harbor_values.get(args.harbor_username_key) or "admin"
    harbor_password = require_key(harbor_values, args.harbor_password_key, "Harbor credential")
    upstream_user = require_key(upstream_values, args.upstream_username_key, "upstream credential")
    upstream_token = require_key(upstream_values, args.upstream_token_key, "upstream credential")

    client = HarborClient(args.harbor_url, harbor_user, harbor_password, insecure=args.insecure)
    info = client.system_info()
    log("harbor " + str(info.get("harbor_version", "unknown")) + " reachable at " + args.harbor_url)

    if args.command == "plan":
        rows = plan(client, args, find_registry(client, args.registry_name))
        if args.json:
            print(json.dumps({"plan": rows}, indent=2, sort_keys=True))
        return 0

    result = run(client, args, upstream_user, upstream_token)
    mismatched = sum(len(report["mismatched"]) for report in result["verifications"])
    missing = sum(len(report["missing"]) for report in result["verifications"])
    failed = mismatched + missing

    if args.json:
        print(json.dumps(result, indent=2, sort_keys=True))
    else:
        print("harbor_replication=" + ("failed" if failed else "complete")
              + " registry=" + str(result["registry"]["id"])
              + " project=" + str(result["project"]["id"]))
        for entry in result["policies"]:
            print("  policy " + entry["policy"] + " id=" + str(entry["policy_id"])
                  + (" created" if entry["created"] else " reused"))
        for entry in result["executions"]:
            print("  execution " + str(entry["execution_id"]) + " " + str(entry["status"]))
        for report in result["verifications"]:
            summary = " ok " + str(report["matched"]) + "/" + str(report["upstream_tags"])
            if not report["ok"]:
                summary = (" FAILED mismatched=" + str(len(report["mismatched"]))
                           + " missing=" + str(len(report["missing"])))
            print("  verify " + report["repository"] + summary
                  + (" kept=" + str(len(report["extra"])) if report["extra"] else ""))
    if failed:
        raise HarborError(
            str(mismatched) + " tag(s) differ from the upstream digest and "
            + str(missing) + " upstream tag(s) are not in Harbor; nothing was deleted"
        )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except (HarborError, CredentialError, UpstreamError) as failure:
        print("harbor_replication=failed " + str(failure), file=sys.stderr)
        sys.exit(1)
