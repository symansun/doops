#!/usr/bin/env node

import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

const API_BASE = "https://api.github.com";
const ASSET_NAMES = [
  "doops-darwin-amd64",
  "doops-darwin-arm64",
  "doops-linux-amd64",
  "doops-linux-arm64",
  "doops-gateway-linux-amd64",
  "checksums.txt",
];

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!["--repo", "--tag", "--commit", "--dir", "--image"].includes(key) || !value) {
      throw new Error(
        "usage: publish-cli-release.mjs --repo <owner/repo> --tag <tag> --commit <sha> --dir <asset-dir> --image <image-ref>",
      );
    }
    values[key.slice(2)] = value;
  }
  if (Object.keys(values).length !== 5) {
    throw new Error(
      "usage: publish-cli-release.mjs --repo <owner/repo> --tag <tag> --commit <sha> --dir <asset-dir> --image <image-ref>",
    );
  }
  if (!/^[^/]+\/[^/]+$/.test(values.repo)) {
    throw new Error(`Invalid GitHub repository: ${values.repo}`);
  }
  if (!/^[0-9a-f]{40}$/.test(values.commit)) {
    throw new Error(`Invalid commit SHA: ${values.commit}`);
  }
  return values;
}

const { repo, tag, commit, dir, image } = parseArgs(process.argv.slice(2));
const token = process.env.GITHUB_TOKEN;
if (!token) {
  throw new Error("GITHUB_TOKEN is required to publish GitHub Release assets");
}

async function request(url, { method = "GET", body, headers = {} } = {}) {
  const response = await fetch(url, {
    method,
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    throw new Error(`GitHub API ${method} ${url} returned non-JSON content`);
  }
  if (!response.ok) {
    const message = data?.message || "request failed";
    throw new Error(`GitHub API ${method} ${url} failed: ${response.status} ${message}`);
  }
  return data;
}

const repoBase = `${API_BASE}/repos/${repo}`;

async function getOrCreateRelease() {
  const encodedTag = encodeURIComponent(tag);
  const existing = await fetch(`${repoBase}/releases/tags/${encodedTag}`, {
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
    },
  });
  if (existing.ok) {
    return existing.json();
  }
  if (existing.status !== 404) {
    const text = await existing.text();
    throw new Error(`GitHub release lookup failed: ${existing.status} ${text}`);
  }
  return request(`${repoBase}/releases`, {
    method: "POST",
    body: {
      tag_name: tag,
      target_commitish: commit,
      name: tag,
      body: `Agent image: ${image}\n\nCLI assets for ${tag} (${commit})`,
      draft: false,
      prerelease: false,
      make_latest: true,
    },
  });
}

async function deleteExistingAsset(releaseId, assetName) {
  const assets = await request(`${repoBase}/releases/${releaseId}/assets?per_page=100`);
  const existing = assets.find((asset) => asset.name === assetName);
  if (existing) {
    await request(`${API_BASE}/repos/${repo}/releases/assets/${existing.id}`, { method: "DELETE" });
  }
}

async function uploadAsset(releaseId, assetName) {
  const assetPath = path.join(dir, assetName);
  const metadata = await stat(assetPath);
  const bytes = await readFile(assetPath);
  await deleteExistingAsset(releaseId, assetName);
  const uploadUrl = `https://uploads.github.com/repos/${repo}/releases/${releaseId}/assets?name=${encodeURIComponent(assetName)}`;
  const response = await fetch(uploadUrl, {
    method: "POST",
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
      "Content-Type": "application/octet-stream",
      "Content-Length": String(metadata.size),
    },
    body: bytes,
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(`GitHub asset upload failed for ${assetName}: ${response.status} ${text}`);
  }
}

const release = await getOrCreateRelease();
if (!release?.id) {
  throw new Error("GitHub release response did not include an id");
}

for (const assetName of ASSET_NAMES) {
  await uploadAsset(release.id, assetName);
}

console.log(`Published ${ASSET_NAMES.length} CLI assets to GitHub Release ${tag}.`);
