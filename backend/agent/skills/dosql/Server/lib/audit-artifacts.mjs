import { createHash, randomUUID } from "node:crypto";
import { O_CREAT, O_EXCL, O_NOFOLLOW, O_RDONLY, O_WRONLY } from "node:constants";
import { link, lstat, mkdir, open, realpath, unlink } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

export async function writeAuditArtifact(path, value) {
  await mkdir(dirname(path), { recursive: true });
  const temporaryPath = `${path}.${randomUUID()}.tmp`;
  const handle = await open(temporaryPath, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600);
  try {
    try {
      await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, "utf8");
      await handle.sync();
    } finally {
      await handle.close();
    }
    await link(temporaryPath, path);
    const directory = await open(dirname(path), O_RDONLY);
    try {
      await directory.sync();
    } finally {
      await directory.close();
    }
  } finally {
    await unlink(temporaryPath);
  }
}

export async function reserveOperationId({ artifactRoot, operationId, claim }) {
  const root = await realpath(resolve(artifactRoot));
  const directory = join(root, "operation-claims");
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const metadata = await lstat(directory);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
    throw new Error("operation-claims must be a real directory inside the audit root");
  }
  const id = String(operationId).trim();
  const fingerprint = createHash("sha256").update(id).digest("hex");
  const path = join(directory, `${fingerprint}.json`);
  try {
    await writeAuditArtifact(path, { ...claim, operationId: id });
  } catch (error) {
    if (error.code !== "EEXIST") throw error;
    // A partially completed explicit import may resume only for the exact same
    // source bytes. Ordinary execution claims are never reusable.
    if (claim.schema === "dosql.legacy-operation-claim.v1") {
      const handle = await open(path, O_RDONLY | O_NOFOLLOW);
      try {
        if (!(await handle.stat()).isFile()) throw new Error("operation claim must be a regular file");
        const existing = JSON.parse(await handle.readFile("utf8"));
        if (existing.schema === claim.schema && existing.sourceHash === claim.sourceHash &&
            existing.operationId === id) return;
      } finally {
        await handle.close();
      }
    }
    throw Object.assign(new Error(
      "operationId has already been reserved; inspect the existing operation before submitting any new SQL",
    ), { code: "DOSQL_OPERATION_ALREADY_EXISTS", executionOutcome: "unknown" });
  }
}
