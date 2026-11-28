import { spawn } from "node:child_process";
import { createHash } from "node:crypto";

const PSQL_ARGS = ["--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--no-align", "--tuples-only"];

export function createPostgresPsqlMetadataAdapter(input = {}) {
  const psqlPath = input.psqlPath ? String(input.psqlPath) : "psql";
  const connectionUriEnv = requireText(input.connectionUriEnv, "connectionUriEnv");
  const baseEnv = input.env ?? process.env;
  const spawnFile = input.spawnFile ?? runProcessWithInput;
  return {
    async executeSqlTransaction(request) {
      const sqlText = requireNonEmptyString(request.sqlText, "request.sqlText");
      const sourceCommitFingerprint = requireText(
        request.sourceCommitFingerprint,
        "request.sourceCommitFingerprint",
      );
      const connectionUri = requireText(baseEnv[connectionUriEnv], connectionUriEnv);
      const processResult = await spawnFile({
        command: psqlPath,
        args: PSQL_ARGS,
        stdin: sqlText,
        env: {
          ...baseEnv,
          PGDATABASE: connectionUri,
        },
      });
      const status = Number(processResult.status);
      const stdout = String(processResult.stdout ?? "");
      const stderr = String(processResult.stderr ?? "");
      if (status !== 0) {
        throw new Error(`psql exited with status ${status}: ${stderr || stdout}`.trim());
      }
      return parsePsqlMetadataExecutionOutput({
        output: `${stdout}\n${stderr}`,
        sourceCommitFingerprint,
        schema: request.schema,
      });
    },
    async executeSqlArtifact(request) {
      const sqlText = requireNonEmptyString(request.sqlText, "request.sqlText");
      const sourceArtifactFingerprint = requireText(
        request.sourceArtifactFingerprint,
        "request.sourceArtifactFingerprint",
      );
      const artifactRef = requireText(request.artifactRef, "request.artifactRef");
      const connectionUri = requireText(baseEnv[connectionUriEnv], connectionUriEnv);
      const processResult = await spawnFile({
        command: psqlPath,
        args: PSQL_ARGS,
        stdin: sqlText,
        env: {
          ...baseEnv,
          PGDATABASE: connectionUri,
        },
      });
      const status = Number(processResult.status);
      const stdout = String(processResult.stdout ?? "");
      const stderr = String(processResult.stderr ?? "");
      if (status !== 0) {
        throw new Error(`psql exited with status ${status}: ${stderr || stdout}`.trim());
      }
      return parsePsqlSqlArtifactOutput({
        output: `${stdout}\n${stderr}`,
        artifactRef,
        sourceArtifactFingerprint,
      });
    },
    async executeScalarCheck(request) {
      const sqlText = requireNonEmptyString(request.sqlText, "request.sqlText");
      const checkName = requireText(request.checkName, "request.checkName");
      const connectionUri = requireText(baseEnv[connectionUriEnv], connectionUriEnv);
      const processResult = await spawnFile({
        command: psqlPath,
        args: PSQL_ARGS,
        stdin: sqlText,
        env: {
          ...baseEnv,
          PGDATABASE: connectionUri,
        },
      });
      const status = Number(processResult.status);
      const stdout = String(processResult.stdout ?? "");
      const stderr = String(processResult.stderr ?? "");
      if (status !== 0) {
        throw new Error(`psql exited with status ${status}: ${stderr || stdout}`.trim());
      }
      return parsePsqlScalarCheckOutput({
        checkName,
        sqlText,
        output: `${stdout}\n${stderr}`,
      });
    },
    async executeTimepointStateQuery(request) {
      const sqlText = requireNonEmptyString(request.sqlText, "request.sqlText");
      const sourceQueryFingerprint = requireText(
        request.sourceQueryFingerprint,
        "request.sourceQueryFingerprint",
      );
      const connectionUri = requireText(baseEnv[connectionUriEnv], connectionUriEnv);
      const processResult = await spawnFile({
        command: psqlPath,
        args: PSQL_ARGS,
        stdin: sqlText,
        env: {
          ...baseEnv,
          PGDATABASE: connectionUri,
        },
      });
      const status = Number(processResult.status);
      const stdout = String(processResult.stdout ?? "");
      const stderr = String(processResult.stderr ?? "");
      if (status !== 0) {
        throw new Error(`psql exited with status ${status}: ${stderr || stdout}`.trim());
      }
      return parsePsqlTimepointStateQueryOutput({
        sqlText,
        output: `${stdout}\n${stderr}`,
        sourceQueryFingerprint,
      });
    },
  };
}

export function parsePsqlMetadataExecutionOutput(input) {
  const output = String(input.output ?? "");
  const sourceCommitFingerprint = requireText(
    input.sourceCommitFingerprint,
    "sourceCommitFingerprint",
  );
  const schema = requireText(input.schema, "schema");
  if (schema === "dosql.timeline-artifacts-metadata-commit.v1") {
    return parsePsqlTimelineArtifactsMetadataOutput({ output, sourceCommitFingerprint });
  }
  if (schema === "dosql.restore-plan-metadata-commit.v1") {
    return parsePsqlRestorePlanMetadataOutput({ output, sourceCommitFingerprint });
  }
  if (schema === "dosql.restore-evidence-metadata-commit.v1") {
    return parsePsqlRestoreEvidenceMetadataOutput({ output, sourceCommitFingerprint });
  }
  if (schema === "dosql.forward-evidence-metadata-commit.v1") {
    return parsePsqlForwardEvidenceMetadataOutput({ output, sourceCommitFingerprint });
  }
  if (schema !== "dosql.change-metadata-commit.v1") {
    throw new Error(`Unsupported metadata commit schema: ${schema}`);
  }
  return parsePsqlChangeMetadataOutput({ output, sourceCommitFingerprint });
}

function parsePsqlChangeMetadataOutput({ output, sourceCommitFingerprint }) {
  const insertCounts = [...output.matchAll(/^INSERT\s+\d+\s+(\d+)\s*$/gim)].map((match) =>
    Number(match[1]),
  );
  if (insertCounts.length < 2) {
    throw new Error("psql output must include timeline and baseline INSERT command tags");
  }
  const guardPassed = output
    .split(/\r?\n/g)
    .map((line) => line.trim())
    .includes("1");
  if (!guardPassed) {
    throw new Error("psql output did not prove the current-head guard passed");
  }
  return {
    status: "succeeded",
    transactionId: `psqltx_${sha256(`${sourceCommitFingerprint}\u001f${output}`).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    timelineNodeInsertCount: insertCounts[0],
    currentHeadUpdateCount: 1,
    currentHeadGuardPassed: true,
    baselineRecordInsertCount: insertCounts[insertCounts.length - 1],
  };
}

function parsePsqlTimelineArtifactsMetadataOutput({ output, sourceCommitFingerprint }) {
  const insertCounts = [...output.matchAll(/^INSERT\s+\d+\s+(\d+)\s*$/gim)].map((match) =>
    Number(match[1]),
  );
  if (insertCounts.length < 1) {
    throw new Error("psql output must include timeline artifacts INSERT command tag");
  }
  return {
    status: "succeeded",
    transactionId: `psqltx_${sha256(`${sourceCommitFingerprint}\u001f${output}`).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    timelineArtifactInsertCount: insertCounts[0],
  };
}

function parsePsqlRestorePlanMetadataOutput({ output, sourceCommitFingerprint }) {
  const insertCounts = [...output.matchAll(/^INSERT\s+\d+\s+(\d+)\s*$/gim)].map((match) =>
    Number(match[1]),
  );
  if (insertCounts.length < 1) {
    throw new Error("psql output must include restore plan INSERT command tag");
  }
  return {
    status: "succeeded",
    transactionId: `psqltx_${sha256(`${sourceCommitFingerprint}\u001f${output}`).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    restorePlanInsertCount: insertCounts[0],
  };
}

function parsePsqlRestoreEvidenceMetadataOutput({ output, sourceCommitFingerprint }) {
  const insertCounts = [...output.matchAll(/^INSERT\s+\d+\s+(\d+)\s*$/gim)].map((match) =>
    Number(match[1]),
  );
  if (insertCounts.length < 3) {
    throw new Error("psql output must include rollback, restore-check and restore-verification INSERT command tags");
  }
  return {
    status: "succeeded",
    transactionId: `psqltx_${sha256(`${sourceCommitFingerprint}\u001f${output}`).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    rollbackExecutionInsertCount: insertCounts[0],
    restoreCheckExecutionInsertCount: insertCounts[1],
    restoreVerificationInsertCount: insertCounts[2],
  };
}

function parsePsqlForwardEvidenceMetadataOutput({ output, sourceCommitFingerprint }) {
  const insertCounts = [...output.matchAll(/^INSERT\s+\d+\s+(\d+)\s*$/gim)].map((match) =>
    Number(match[1]),
  );
  if (insertCounts.length < 1) {
    throw new Error("psql output must include forward execution INSERT command tag");
  }
  return {
    status: "succeeded",
    transactionId: `psqltx_${sha256(`${sourceCommitFingerprint}\u001f${output}`).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    forwardExecutionInsertCount: insertCounts[0],
  };
}

export function parsePsqlSqlArtifactOutput(input) {
  const output = String(input.output ?? "");
  const artifactRef = requireText(input.artifactRef, "artifactRef");
  const sourceArtifactFingerprint = requireText(
    input.sourceArtifactFingerprint,
    "sourceArtifactFingerprint",
  );
  const affectedRows = [...output.matchAll(/^(?:INSERT|UPDATE|DELETE)\s+(?:\d+\s+)?(\d+)\s*$/gim)]
    .map((match) => Number(match[1]))
    .reduce((sum, value) => sum + value, 0);
  return {
    status: "succeeded",
    transactionId: `psqlart_${sha256(
      `${artifactRef}\u001f${sourceArtifactFingerprint}\u001f${output}`,
    ).slice(0, 16)}`,
    statementCount: countStatementTags(output),
    affectedRows,
  };
}

export function parsePsqlScalarCheckOutput(input) {
  const output = String(input.output ?? "");
  const checkName = requireText(input.checkName, "checkName");
  const sqlText = requireNonEmptyString(input.sqlText, "sqlText");
  const actual = output
    .split(/\r?\n/g)
    .map((line) => line.trim())
    .find((line) => line !== "");
  if (!actual) {
    throw new Error("psql restore check output must include a scalar value");
  }
  return {
    status: "succeeded",
    checkName,
    actual,
    transactionId: `psqlchk_${sha256(`${checkName}\u001f${sqlText}\u001f${output}`).slice(0, 16)}`,
    statementCount: countSqlStatements(sqlText),
  };
}

export function parsePsqlTimepointStateQueryOutput(input) {
  const output = String(input.output ?? "");
  const sqlText = requireNonEmptyString(input.sqlText, "sqlText");
  const sourceQueryFingerprint = requireText(
    input.sourceQueryFingerprint,
    "sourceQueryFingerprint",
  );
  const jsonLine = output
    .split(/\r?\n/g)
    .map((line) => line.trim())
    .find((line) => line !== "");
  if (!jsonLine) {
    throw new Error("psql timepoint state query output must include one JSON row");
  }
  let timepointState;
  try {
    timepointState = JSON.parse(jsonLine);
  } catch (error) {
    throw new Error(`psql timepoint state query output must be JSON: ${error.message}`);
  }
  if (!timepointState || typeof timepointState !== "object" || Array.isArray(timepointState)) {
    throw new Error("psql timepoint state query output must be a JSON object");
  }
  return {
    status: "succeeded",
    timepointState,
    transactionId: `psqlqry_${sha256(
      `${sourceQueryFingerprint}\u001f${sqlText}\u001f${jsonLine}`,
    ).slice(0, 16)}`,
    statementCount: countSqlStatements(sqlText),
  };
}

async function runProcessWithInput({ command, args, stdin, env }) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (status) => {
      resolve({ status, stdout, stderr });
    });
    child.stdin.end(stdin);
  });
}

function countStatementTags(output) {
  return output
    .split(/\r?\n/g)
    .map((line) => line.trim().toUpperCase())
    .filter(
      (line) =>
        line === "BEGIN" ||
        line === "COMMIT" ||
        line === "ALTER TABLE" ||
        /^INSERT\s+\d+\s+\d+$/.test(line) ||
        /^UPDATE\s+\d+$/.test(line) ||
        /^DELETE\s+\d+$/.test(line),
    )
    .length;
}

function countSqlStatements(sqlText) {
  const count = String(sqlText)
    .split(";")
    .map((statement) => statement.trim())
    .filter(Boolean).length;
  return count || 1;
}

function requireText(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value).trim();
}

function requireNonEmptyString(value, fieldName) {
  if (value === undefined || value === null || String(value).trim() === "") {
    throw new Error(`${fieldName} is required`);
  }
  return String(value);
}

function sha256(value) {
  return createHash("sha256").update(String(value)).digest("hex");
}
