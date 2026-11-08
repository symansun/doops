"""Run the release publisher against a strict, stateful GitHub API boundary."""

import base64
import hashlib
import json
import os
import subprocess
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parents[1]
PUBLISHER = ROOT / "scripts" / "publish-cli-release.mjs"
COMMIT = "a" * 40
TAG = "v1.2.3"
IMAGE = "ghcr.io/symansun/doops-agent@sha256:" + "a" * 64
BINARY_NAMES = (
    "doops-darwin-amd64",
    "doops-darwin-arm64",
    "doops-linux-amd64",
    "doops-linux-arm64",
    "doops-gateway-linux-amd64",
)
ASSET_NAMES = (*BINARY_NAMES, "checksums.txt")


FETCH_BOUNDARY = r"""
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

const config = JSON.parse(readFileSync(process.env.PUBLISHER_TEST_CONFIG, "utf8"));
const calls = [];
let release = config.release ?? null;
let compareCalls = 0;
let nextAssetId = Math.max(999, ...(config.assets ?? []).map((asset) => asset.id)) + 1;
const assets = (config.assets ?? []).map((asset) => ({
  ...asset,
  bytes: Buffer.from(asset.content, "base64"),
}));
const metadata = ({ bytes, content, ...asset }) => ({
  ...asset,
  size: asset.size ?? bytes.length,
  state: asset.state ?? "uploaded",
});
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});
const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

process.on("exit", () => {
  writeFileSync(process.env.PUBLISHER_TEST_TRACE, JSON.stringify({
    calls,
    release,
    assets: assets.map((asset) => ({
      ...metadata(asset),
      content: asset.bytes.toString("base64"),
    })),
  }));
});

globalThis.fetch = async (input, options = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url);
  const method = options.method ?? "GET";
  const headers = new Headers(options.headers);
  const body = options.body === undefined || url.hostname === "uploads.github.com"
    ? null : JSON.parse(options.body);
  const call = {
    method,
    url: url.href,
    path: url.pathname,
    accept: headers.get("Accept"),
    authenticated: headers.get("Authorization") === "Bearer synthetic-publisher-token",
    body,
  };
  calls.push(call);
  if (!call.authenticated) throw new Error("GitHub request must carry publisher authentication");
  if (!['api.github.com', 'uploads.github.com'].includes(url.hostname)) {
    throw new Error(`Unexpected network destination: ${url.hostname}`);
  }
  const prefix = "/repos/symansun/doops/";
  if (!url.pathname.startsWith(prefix)) throw new Error(`Unexpected repository path: ${url.pathname}`);
  const route = url.pathname.slice(prefix.length);

  if (url.hostname === "uploads.github.com") {
    if (method !== "POST" || route !== `releases/${release?.id}/assets`) {
      throw new Error(`Unexpected upload operation: ${method} ${route}`);
    }
    const name = url.searchParams.get("name");
    const bytes = Buffer.from(options.body);
    call.asset = name;
    call.sha256 = digest(bytes);
    if (config.fail_upload === name) return json({ message: "synthetic upload outage" }, 503);
    if (assets.some((asset) => asset.name === name)) {
      throw new Error(`Publisher attempted to overwrite existing asset ${name}`);
    }
    const asset = { id: nextAssetId++, name, bytes, state: "uploaded" };
    assets.push(asset);
    return json(metadata(asset), 201);
  }

  if (method === "GET" && route === `git/ref/tags/${config.tag}`) {
    return json({ object: config.annotated
      ? { type: "tag", sha: "1".repeat(40) }
      : { type: "commit", sha: config.commit } });
  }
  if (method === "GET" && route === `git/tags/${"1".repeat(40)}` && config.annotated) {
    return json({ object: { type: "commit", sha: config.commit } });
  }
  if (method === "GET" && route === `compare/${config.commit}...main`) {
    compareCalls++;
    const contained = config.contained !== false && !(config.main_changes_after_first_check && compareCalls > 1);
    return json({
      status: "ahead",
      merge_base_commit: { sha: contained ? config.commit : "b".repeat(40) },
    });
  }
  if (method === "GET" && route === "releases") {
    if (url.searchParams.get("per_page") !== "100" || url.searchParams.get("page") !== "1") {
      throw new Error(`Unexpected releases pagination: ${url.search}`);
    }
    return json(release ? [release] : []);
  }
  if (method === "POST" && route === "releases") {
    if (release) throw new Error("Publisher attempted to create a duplicate release");
    release = { ...body, id: 17 };
    return json(release, 201);
  }
  if (method === "GET" && route === `releases/${release?.id}/assets`) {
    if (url.searchParams.get("per_page") !== "100" || url.searchParams.get("page") !== "1") {
      throw new Error(`Unexpected assets pagination: ${url.search}`);
    }
    return json(assets.map(metadata));
  }
  if (method === "GET" && route.startsWith("releases/assets/")) {
    if (call.accept !== "application/octet-stream") throw new Error("Asset verification must download its bytes");
    const id = Number(route.slice("releases/assets/".length));
    const asset = assets.find((item) => item.id === id);
    if (!asset) return json({ message: "asset not found" }, 404);
    return new Response(asset.bytes, { status: 200, headers: { "Content-Type": "application/octet-stream" } });
  }
  if (method === "PATCH" && route === `releases/${release?.id}`) {
    release = { ...release, ...body };
    return json(release);
  }
  throw new Error(`Unexpected GitHub operation: ${method} ${route}`);
};
"""


@pytest.fixture
def release_assets(tmp_path):
    directory = tmp_path / "assets"
    directory.mkdir()
    contents = {}
    for name in BINARY_NAMES:
        payload = f"{name}\nrelease={TAG} commit={COMMIT}\n".encode()
        if not name.startswith("doops-gateway"):
            payload += b"doops.sh/cli-contract/v3\n"
        contents[name] = payload
        (directory / name).write_bytes(payload)
    contents["checksums.txt"] = "".join(
        f"{hashlib.sha256(contents[name]).hexdigest()}  {name}\n"
        for name in BINARY_NAMES
    ).encode()
    (directory / "checksums.txt").write_bytes(contents["checksums.txt"])
    return directory, contents


def run_publisher(tmp_path, release_assets, scenario=None, attempt="attempt"):
    directory, _contents = release_assets
    workspace = tmp_path / attempt
    workspace.mkdir()
    config = workspace / "config.json"
    trace = workspace / "trace.json"
    boundary = workspace / "fetch-boundary.mjs"
    config.write_text(json.dumps({"tag": TAG, "commit": COMMIT, **(scenario or {})}))
    boundary.write_text(FETCH_BOUNDARY)
    result = subprocess.run(
        [
            "node", "--import", str(boundary), str(PUBLISHER),
            "--repo", "symansun/doops",
            "--tag", TAG,
            "--commit", COMMIT,
            "--dir", str(directory),
            "--image", IMAGE,
        ],
        env={
            **os.environ,
            "GITHUB_TOKEN": "synthetic-publisher-token",
            "PUBLISHER_TEST_CONFIG": str(config),
            "PUBLISHER_TEST_TRACE": str(trace),
        },
        text=True,
        capture_output=True,
        check=False,
        timeout=30,
    )
    assert trace.is_file(), result.stdout + result.stderr
    return result, json.loads(trace.read_text())


def mutations(trace):
    return [call for call in trace["calls"] if call["method"] != "GET"]


def draft_release():
    return {"id": 17, "tag_name": TAG, "target_commitish": COMMIT, "draft": True}


def test_publisher_opens_draft_and_publishes_only_after_all_asset_downloads(tmp_path, release_assets):
    result, trace = run_publisher(tmp_path, release_assets)

    assert result.returncode == 0, result.stderr
    writes = mutations(trace)
    assert writes[0]["path"] == "/repos/symansun/doops/releases"
    assert writes[0]["body"]["draft"] is True
    assert writes[0]["body"]["target_commitish"] == COMMIT
    assert [call["asset"] for call in writes[1:-1]] == list(ASSET_NAMES)
    assert writes[-1]["method"] == "PATCH"
    assert writes[-1]["body"] == {"draft": False, "make_latest": "true"}
    assert trace["calls"][-1] == writes[-1]
    for name in ASSET_NAMES:
        asset = next(item for item in trace["assets"] if item["name"] == name)
        downloads = [
            call for call in trace["calls"]
            if call["method"] == "GET" and call["path"].endswith(f"/releases/assets/{asset['id']}")
        ]
        assert len(downloads) >= 2, f"{name} must be verified after upload and again before publication"
    assert trace["release"]["draft"] is False


def test_publisher_refuses_commit_outside_main_before_any_write(tmp_path, release_assets):
    result, trace = run_publisher(tmp_path, release_assets, {"contained": False})

    assert result.returncode != 0
    assert "not contained in GitHub main" in result.stderr
    assert mutations(trace) == []
    assert trace["release"] is None


def test_publisher_refuses_incorrect_local_checksums_before_any_write(tmp_path, release_assets):
    directory, contents = release_assets
    checksums = contents["checksums.txt"].decode()
    checksum = hashlib.sha256(contents[BINARY_NAMES[0]]).hexdigest()
    (directory / "checksums.txt").write_text(checksums.replace(checksum, "0" * 64, 1))

    result, trace = run_publisher(tmp_path, release_assets)

    assert result.returncode != 0
    assert f"release checksum mismatch: {BINARY_NAMES[0]}" in result.stderr
    assert trace["calls"] == []
    assert mutations(trace) == []


def test_publisher_never_modifies_a_published_release(tmp_path, release_assets):
    result, trace = run_publisher(
        tmp_path, release_assets, {"release": {**draft_release(), "draft": False}},
    )

    assert result.returncode != 0
    assert f"published release {TAG} is immutable" in result.stderr
    assert mutations(trace) == []
    assert trace["release"]["draft"] is False


def test_upload_failure_leaves_the_existing_assets_in_a_draft(tmp_path, release_assets):
    result, trace = run_publisher(tmp_path, release_assets, {"fail_upload": BINARY_NAMES[2]})

    assert result.returncode != 0
    assert f"asset upload failed: {BINARY_NAMES[2]}" in result.stderr
    assert "draft retained" in result.stderr
    assert trace["release"]["draft"] is True
    assert [asset["name"] for asset in trace["assets"]] == list(BINARY_NAMES[:2])
    assert not any(call["method"] in ("PATCH", "DELETE") for call in trace["calls"])


def test_retry_reuses_identical_assets_without_replacing_them(tmp_path, release_assets):
    failed, partial = run_publisher(
        tmp_path, release_assets, {"fail_upload": BINARY_NAMES[2]}, attempt="first",
    )
    assert failed.returncode != 0

    result, trace = run_publisher(
        tmp_path, release_assets,
        {"release": partial["release"], "assets": partial["assets"]},
        attempt="retry",
    )

    assert result.returncode == 0, result.stderr
    uploads = [call for call in trace["calls"] if "asset" in call]
    assert [call["asset"] for call in uploads] == list(ASSET_NAMES[2:])
    assert not any(call["method"] == "DELETE" for call in trace["calls"])
    assert not any(call["method"] == "POST" and call["path"].endswith("/releases") for call in trace["calls"])
    for before in partial["assets"]:
        after = next(asset for asset in trace["assets"] if asset["name"] == before["name"])
        assert after == before
    assert trace["release"]["draft"] is False


def test_retry_refuses_same_size_asset_with_different_bytes(tmp_path, release_assets):
    _directory, contents = release_assets
    name = BINARY_NAMES[0]
    wrong_bytes = b"!" + contents[name][1:]
    result, trace = run_publisher(tmp_path, release_assets, {
        "release": draft_release(),
        "assets": [{
            "id": 51,
            "name": name,
            "content": base64.b64encode(wrong_bytes).decode(),
            "state": "uploaded",
        }],
    })

    assert result.returncode != 0
    assert f"release asset digest mismatch: {name}" in result.stderr
    assert mutations(trace) == []
    assert trace["release"]["draft"] is True
    assert base64.b64decode(trace["assets"][0]["content"]) == wrong_bytes


def test_publisher_resolves_annotated_tag_before_publishing(tmp_path, release_assets):
    result, trace = run_publisher(tmp_path, release_assets, {"annotated": True})

    assert result.returncode == 0, result.stderr
    tag_reads = [call for call in trace["calls"] if "/git/tags/" in call["path"]]
    assert len(tag_reads) == 2
    assert all(call["method"] == "GET" and call["path"].endswith("1" * 40) for call in tag_reads)
    assert trace["release"]["draft"] is False


def test_publisher_rechecks_main_containment_before_publication(tmp_path, release_assets):
    result, trace = run_publisher(tmp_path, release_assets, {"main_changes_after_first_check": True})

    assert result.returncode != 0
    assert "not contained in GitHub main" in result.stderr
    assert {asset["name"] for asset in trace["assets"]} == set(ASSET_NAMES)
    assert trace["release"]["draft"] is True
    assert not any(call["method"] in ("PATCH", "DELETE") for call in trace["calls"])
