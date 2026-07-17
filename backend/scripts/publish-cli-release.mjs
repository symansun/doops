#!/usr/bin/env node

import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

const API_BASE = "https://api.cnb.cool";
const ASSET_NAMES = [
  "doops-darwin-amd64",
  "doops-darwin-arm64",
  "doops-linux-amd64",
  "doops-linux-arm64",
  "checksums.txt",
];

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!["--repo", "--tag", "--commit", "--dir", "--image"].includes(key) || !value) {
      throw new Error(
        "usage: publish-cli-release.mjs --repo <repo> --tag <tag> --commit <sha> --dir <asset-dir> --image <image-ref>",
      );
    }
    values[key.slice(2)] = value;
  }
  if (Object.keys(values).length !== 5) {
    throw new Error(
      "usage: publish-cli-release.mjs --repo <repo> --tag <tag> --commit <sha> --dir <asset-dir> --image <image-ref>",
    );
  }
  return values;
}

const { repo, tag, commit, dir, image } = parseArgs(process.argv.slice(2));
const token = process.env.CNB_TOKEN;
if (!token) {
  throw new Error("CNB_TOKEN is required to publish CNB Release Assets");
}

async function request(url, { method = "GET", body } = {}) {
  const headers = {
    Authorization: `Bearer ${token}`,
    Accept: "application/json",
  };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }

  const response = await fetch(url, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    throw new Error(`CNB API ${method} ${url} returned non-JSON content`);
  }
  return { response, data };
}

async function api(pathname, options) {
  const result = await request(`${API_BASE}/${repo}/-/${pathname}`, options);
  if (!result.response.ok) {
    const message = result.data?.errmsg || result.data?.message || "request failed";
    throw new Error(
      `CNB API ${options?.method || "GET"} ${pathname} failed: ${result.response.status} ${message}`,
    );
  }
  return result.data;
}

async function getOrCreateRelease() {
  const existing = await request(
    `${API_BASE}/${repo}/-/releases/tags/${encodeURIComponent(tag)}`,
  );
  if (existing.response.ok) {
    return existing.data;
  }
  if (existing.response.status !== 404) {
    const message =
      existing.data?.errmsg || existing.data?.message || "request failed";
    throw new Error(
      `CNB API GET releases/tags/${tag} failed: ${existing.response.status} ${message}`,
    );
  }
  return api("releases", {
    method: "POST",
    body: {
      tag_name: tag,
      target_commitish: commit,
      name: tag,
      body: `Agent image: ${image}\n\nCLI assets for ${tag} (${commit})`,
      make_latest: "true",
    },
  });
}

async function uploadAsset(releaseId, assetName) {
  const assetPath = path.join(dir, assetName);
  const metadata = await stat(assetPath);
  const upload = await api(`releases/${releaseId}/asset-upload-url`, {
    method: "POST",
    body: {
      asset_name: assetName,
      size: metadata.size,
      overwrite: true,
    },
  });
  if (!upload?.upload_url || !upload?.verify_url) {
    throw new Error(`CNB did not return upload and verification URLs for ${assetName}`);
  }

  const bytes = await readFile(assetPath);
  const uploadResponse = await fetch(upload.upload_url, {
    method: "PUT",
    headers: { "Content-Type": "application/octet-stream" },
    body: bytes,
  });
  if (!uploadResponse.ok) {
    throw new Error(`asset upload failed for ${assetName}: ${uploadResponse.status}`);
  }

  const verifyUrl = new URL(upload.verify_url, API_BASE);
  verifyUrl.searchParams.set("ttl", "0");
  const verified = await request(verifyUrl, { method: "POST" });
  if (!verified.response.ok) {
    const message = verified.data?.errmsg || verified.data?.message || "request failed";
    throw new Error(
      `asset confirmation failed for ${assetName}: ${verified.response.status} ${message}`,
    );
  }
}

const release = await getOrCreateRelease();
if (!release?.id) {
  throw new Error("CNB release response did not include an id");
}

for (const assetName of ASSET_NAMES) {
  await uploadAsset(release.id, assetName);
}

console.log(`Published ${ASSET_NAMES.length} CLI assets to CNB release ${tag}.`);
