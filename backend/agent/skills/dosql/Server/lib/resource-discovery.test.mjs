import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
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
  const displayResourcesPath = join(root, "resources.json");
  const result = await discoverAndPersistResources({
    rootDir: root,
    resourcesPath,
    displayResourcesPath,
    projectId: "zhiyong",
    environmentId: "zyj",
    target: { name: "gw-zyj", cluster: "doops-zyj", instance: "control-plane-1" },
    hints: [{ namespace: "agents", serviceName: "postgres", host: "postgres.agents.svc.cluster.local", port: 5432, databaseName: "agentcloud" }],
  });
  const display = JSON.parse(await readFile(displayResourcesPath, "utf8"));
  assert.equal(result.inventory.assets[0].source.kind, "target-discovery");
  assert.match(result.inventory.assets[0].connectionRef, /#db=agentcloud$/);
  assert.equal(display.schema, "dosuite.skill-resources.v1");
  assert.equal(display.product, "DoSql");
  assert.equal(display.resources.length, 1);
  assert.equal(display.resources[0].id, "db_zhiyong_zyj_agents_postgres_agentcloud");
  assert.equal(display.resources[0].kind, "PostgreSQL");
  assert.equal(display.resources[0].status, "discovered");
  assert.match(display.resources[0].logicalEndpoint, /^postgresql:\/\/postgres\.agents\.svc\.cluster\.local:5432\/agentcloud$/);
  assert.equal(JSON.stringify(display).includes("password"), false);
  assert.equal(JSON.stringify(display).includes("connection_uri"), false);
});

test("discover-resources parses Helm/Kubernetes manifests, connection references, and deduplicates assets", async () => {
  const root = await mkdtemp(join(tmpdir(), "dosql-resource-manifest-"));
  const source = join(root, "deploy", "values.yaml");
  const resourcesPath = join(root, "assets.json");
  await mkdir(join(root, "deploy"), { recursive: true });
  await writeFile(source, `
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: agents
spec:
  ports:
    - port: 5432
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agentcloud
  namespace: agents
spec:
  template:
    spec:
      containers:
        - name: backend
          image: postgres:16
          env:
            - name: POSTGRES_DB
              value: agentcloud
            - name: DATABASE_URL
              value: postgresql://user:password@postgres.agents.svc.cluster.local:5432/agentcloud
---
apiVersion: v1
kind: Service
metadata:
  name: billing-mysql
  namespace: data
spec:
  ports:
    - port: 3306
---
apiVersion: v1
kind: Service
metadata:
  name: events-mongodb
  namespace: data
spec:
  ports:
    - port: 27017
`, "utf8");

  const result = await discoverAndPersistResources({
    rootDir: root,
    resourcesPath,
    projectId: "zhiyong",
    environmentId: "zyj",
    target: { name: "gw-zyj", cluster: "doops-zyj", instance: "control-plane-1" },
  });
  const postgres = result.inventory.assets.filter((asset) => asset.engine === "postgres");
  const mysql = result.inventory.assets.filter((asset) => asset.engine === "mysql");
  const mongodb = result.inventory.assets.filter((asset) => asset.engine === "mongodb");
  assert.equal(postgres.length, 1);
  assert.equal(postgres[0].databaseAssetId, "db_zhiyong_zyj_agents_postgres_agentcloud");
  assert.equal(postgres[0].source.kind, "helm");
  assert.equal(mysql.length, 1);
  assert.equal(mysql[0].databaseAssetId, "db_zhiyong_zyj_data_billing_mysql_unknown");
  assert.equal(mongodb.length, 1);
  assert.equal(mongodb[0].port, 27017);
  assert.equal(JSON.stringify(result.inventory).includes("user:password"), false);
  assert.equal(new Set(result.inventory.assets.map((asset) => asset.databaseAssetId)).size, result.inventory.assets.length);
});

test("target discovery adopts the Gateway asset id over a prior generated id", async () => {
  const root = await mkdtemp(join(tmpdir(), "dosql-resource-canonical-id-"));
  const resourcesPath = join(root, "assets.json");
  await writeFile(resourcesPath, `${JSON.stringify({
    schema: "dosql.database-asset-inventory.v1",
    projectId: "zhiyong",
    assets: [{
      databaseAssetId: "db_zhiyong_zyj_agents_postgres_agentcloud",
      projectId: "zhiyong",
      environmentId: "zyj",
      engine: "postgres",
      namespace: "agents",
      serviceName: "postgres",
      host: "postgres.agents.svc.cluster.local",
      port: 5432,
      databaseName: "agentcloud",
      source: { kind: "code", ref: "values.yaml" },
    }],
  }, null, 2)}\n`, "utf8");
  const result = await discoverAndPersistResources({
    rootDir: root,
    resourcesPath,
    projectId: "zhiyong",
    environmentId: "zyj",
    target: { name: "gw-zyj", cluster: "doops-zyj", instance: "control-plane-1" },
    hints: [{
      databaseAssetId: "dbscan_doops-zyj_control-plane-1_agents_postgres_postgres",
      namespace: "agents",
      serviceName: "postgres",
      host: "postgres.agents.svc.cluster.local",
      port: 5432,
      databaseName: "agentcloud",
      sourceRef: "mcp_dosql_read:discover",
    }],
  });
  assert.equal(result.inventory.assets.length, 1);
  assert.equal(result.inventory.assets[0].databaseAssetId, "dbscan_doops-zyj_control-plane-1_agents_postgres_postgres");
  assert.equal(result.inventory.assets[0].source.kind, "target-discovery");
});
