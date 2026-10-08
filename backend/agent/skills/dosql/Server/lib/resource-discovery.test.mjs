import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { discoverAndPersistResources } from "./resource-discovery.mjs";

test("discover-resources scans code, strips credentials, and persists assets", async () => {
  const root = await mkdtemp(join(tmpdir(), "dosql-resource-discovery-"));
  const source = join(root, "deploy.yaml");
  const resourcesPath = join(root, "config", "assets.json");
  const displayResourcesPath = join(root, "config", "resources.json");
  await writeFile(source, `DATABASE_URL: postgresql://db-user:db-secret@postgres.agents.svc.cluster.local:5432/agentcloud\n`, "utf8");

  const result = await discoverAndPersistResources({
    rootDir: root,
    resourcesPath,
    displayResourcesPath,
    projectId: "zhiyong",
    environmentId: "zyj",
    target: { name: "gw-zyj", cluster: "doops-zyj", instance: "control-plane-1" },
  });
  const saved = JSON.parse(await readFile(resourcesPath, "utf8"));
  const display = JSON.parse(await readFile(displayResourcesPath, "utf8"));
  assert.equal(result.assets.length, 1);
  assert.equal(saved.assets[0].databaseAssetId, "db_zhiyong_zyj_agents_postgres_agentcloud");
  assert.equal(saved.assets[0].host, "postgres.agents.svc.cluster.local");
  assert.equal(saved.assets[0].databaseName, "agentcloud");
  assert.equal(saved.assets[0].source.kind, "code");
  assert.equal(display.resources[0].id, "db_zhiyong_zyj_agents_postgres_agentcloud");
  assert.equal(JSON.stringify(saved).includes("db-secret"), false);
  assert.equal(JSON.stringify(saved).includes("postgresql://db-user"), false);
});

test("discover-resources persists an explicit target discovery hint", async () => {
  const root = await mkdtemp(join(tmpdir(), "dosql-resource-hint-"));
  const resourcesPath = join(root, "assets.json");
  const result = await discoverAndPersistResources({
    rootDir: root,
    resourcesPath,
    projectId: "zhiyong",
    environmentId: "zyj",
    target: { name: "gw-zyj", cluster: "doops-zyj", instance: "control-plane-1" },
    hints: [{ namespace: "agents", serviceName: "postgres", host: "postgres.agents.svc.cluster.local", port: 5432, databaseName: "agentcloud" }],
  });
  assert.equal(result.inventory.assets[0].source.kind, "target-discovery");
  assert.match(result.inventory.assets[0].connectionRef, /#db=agentcloud$/);
});
