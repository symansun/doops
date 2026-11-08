#!/usr/bin/env node

import { createHash } from "node:crypto";
import { lstat, readFile } from "node:fs/promises";
import path from "node:path";

const API = "https://api.github.com";
const BINARY_NAMES = ["doops-darwin-amd64", "doops-darwin-arm64", "doops-linux-amd64", "doops-linux-arm64", "doops-gateway-linux-amd64"];
const ASSET_NAMES = [...BINARY_NAMES, "checksums.txt"];
const usage = "usage: publish-cli-release.mjs --repo <owner/name> --tag <tag> --commit <sha> --dir <asset-dir> --image <image-ref>";
const values = {};
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i += 2) {
  const key = argv[i];
  if (!["--repo", "--tag", "--commit", "--dir", "--image"].includes(key) || !argv[i + 1] || values[key.slice(2)]) throw new Error(usage);
  values[key.slice(2)] = argv[i + 1];
}
if (Object.keys(values).length !== 5) throw new Error(usage);
const { repo, tag, commit, dir, image } = values;
if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo) || !/^v[0-9]+\.[0-9]+\.[0-9]+$/.test(tag) || !/^[0-9a-f]{40}$/.test(commit)) throw new Error("invalid repository, semantic release tag or full commit SHA");
if (!/^ghcr\.io\/symansun\/doops-agent@sha256:[0-9a-f]{64}$/.test(image)) throw new Error("Agent image must be a verified GHCR digest");
const token = process.env.GITHUB_TOKEN;
if (!token) throw new Error("GITHUB_TOKEN is required to publish GitHub Release assets");
const headers = (extra = {}) => ({ Accept: "application/vnd.github+json", Authorization: `Bearer ${token}`, "X-GitHub-Api-Version": "2022-11-28", ...extra });
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

async function github(route, method = "GET", body) {
  const response = await fetch(`${API}/repos/${repo}/${route}`, {
    method, headers: headers(body === undefined ? {} : { "Content-Type": "application/json" }),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(`GitHub ${method} ${route} failed: ${response.status} ${data.message || "request failed"}`);
  return data;
}

async function listAll(route) {
  const results = [];
  for (let page = 1; page <= 100; page++) {
    const items = await github(`${route}?per_page=100&page=${page}`);
    if (!Array.isArray(items)) throw new Error(`GitHub ${route} did not return a list`);
    results.push(...items);
    if (items.length < 100) return results;
  }
  throw new Error(`GitHub ${route} exceeds the safe pagination limit`);
}

async function verifySource() {
  let object = (await github(`git/ref/tags/${encodeURIComponent(tag)}`)).object;
  const seen = new Set();
  while (object.type === "tag") {
    if (seen.has(object.sha) || seen.size >= 10) throw new Error("invalid annotated tag chain");
    seen.add(object.sha);
    object = (await github(`git/tags/${object.sha}`)).object;
  }
  if (object.type !== "commit" || object.sha !== commit) throw new Error(`tag ${tag} does not resolve to release commit ${commit}`);
  const comparison = await github(`compare/${commit}...main`);
  if (comparison.merge_base_commit?.sha !== commit) throw new Error(`release commit ${commit} is not contained in GitHub main`);
}

async function loadAssets() {
  const assets = [];
  for (const name of ASSET_NAMES) {
    const file = path.join(dir, name);
    const metadata = await lstat(file);
    if (!metadata.isFile() || metadata.size === 0) throw new Error(`empty or non-regular release asset: ${name}`);
    const bytes = await readFile(file);
    assets.push({ name, bytes, size: bytes.length, sha256: sha256(bytes) });
  }
  const checksums = new Map();
  for (const line of assets.at(-1).bytes.toString("utf8").trim().split(/\r?\n/)) {
    const match = /^([0-9a-f]{64})[ \t]+\*?([A-Za-z0-9.-]+)$/.exec(line);
    if (!match || !BINARY_NAMES.includes(match[2]) || checksums.has(match[2])) throw new Error("invalid or duplicate release checksum entry");
    checksums.set(match[2], match[1]);
  }
  for (const asset of assets.slice(0, -1)) {
    if (checksums.get(asset.name) !== asset.sha256) throw new Error(`release checksum mismatch: ${asset.name}`);
    for (const marker of [tag, commit, ...(asset.name.startsWith("doops-gateway") ? [] : ["doops.sh/cli-contract/v3"])]) {
      if (!asset.bytes.includes(Buffer.from(marker))) throw new Error(`release identity marker missing: ${asset.name}`);
    }
  }
  return assets;
}

async function verifyAsset(remote, asset) {
  if (remote.size !== asset.size || remote.state !== "uploaded") throw new Error(`release asset metadata mismatch: ${asset.name}`);
  const response = await fetch(`${API}/repos/${repo}/releases/assets/${remote.id}`, { headers: headers({ Accept: "application/octet-stream" }) });
  if (!response.ok || sha256(Buffer.from(await response.arrayBuffer())) !== asset.sha256) throw new Error(`release asset digest mismatch: ${asset.name}; refusing to overwrite an immutable asset`);
}

// All local checks and source checks finish before creating or changing a release.
const assets = await loadAssets();
await verifySource();
let release = (await listAll("releases")).find((item) => item.tag_name === tag);
if (release && !release.draft) throw new Error(`published release ${tag} is immutable`);
if (!release) release = await github("releases", "POST", {
  tag_name: tag, target_commitish: commit, name: tag,
  body: `DoOps ${tag}\n\nSource: https://github.com/${repo}/commit/${commit}\nAgent image: ${image}`,
  draft: true, prerelease: false,
});
if (!release.id || !release.draft) throw new Error("release must remain a draft until verification completes");
let remoteAssets = await listAll(`releases/${release.id}/assets`);
if (remoteAssets.some((asset) => !ASSET_NAMES.includes(asset.name))) throw new Error("draft release contains unexpected assets");
for (const asset of assets) {
  let remote = remoteAssets.find((item) => item.name === asset.name);
  if (!remote) {
    const response = await fetch(`https://uploads.github.com/repos/${repo}/releases/${release.id}/assets?name=${encodeURIComponent(asset.name)}`, {
      method: "POST", headers: headers({ "Content-Type": "application/octet-stream" }), body: asset.bytes,
    });
    if (!response.ok) throw new Error(`asset upload failed: ${asset.name} (${response.status}); draft retained for verification and retry`);
    remote = await response.json();
  }
  // Retry reuses an identical verified asset. It never deletes or overwrites one.
  await verifyAsset(remote, asset);
}
remoteAssets = await listAll(`releases/${release.id}/assets`);
if (remoteAssets.length !== assets.length) throw new Error("release asset set is incomplete");
for (const asset of assets) await verifyAsset(remoteAssets.find((item) => item.name === asset.name), asset);
await verifySource();
await github(`releases/${release.id}`, "PATCH", { draft: false, make_latest: "true" });
console.log(`Published ${assets.length} verified GitHub Release assets for ${repo}@${tag} (${commit}).`);
