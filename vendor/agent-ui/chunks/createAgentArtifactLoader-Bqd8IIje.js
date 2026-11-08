import { createClient, ConnectError, Code } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { serviceDesc, fileDesc } from "@bufbuild/protobuf/codegenv2";
import { A as AgentWorkbenchService, r as resolveSourceTool } from "./sourceToolCatalog-oO9OnLlo.js";
import { A as AgentSessionReductionError, a as AgentSessionStore, i as isSnapshotCursorConflict, d as createAgentCommandEnvelope } from "./createAgentCommandEnvelope-DISPrJTM.js";
import { create, equals } from "@bufbuild/protobuf";
import { ax as SessionCursorSchema, a8 as PermissionDecision, D as CommandReceiptState, a2 as MessageRole, y as CommandEnvelopeSchema, aD as SessionStatus, p as ArtifactStatus, bo as VideoStage, bk as UnsupportedReason, b3 as TimelineItemStatus, ae as PermissionRequestState, aA as SessionResourceStatus, bd as ToolPhase, ak as PlanStepStatus, aO as TerminalControlMode } from "./session_pb-CU4zY4LI.js";
function withPartnerIdentity(snapshot, partner) {
  if (!partner) return snapshot;
  const name = partner.name.trim();
  return {
    ...snapshot,
    partner,
    ...name ? { agentLabel: name, title: name } : {}
  };
}
function parseFormWorkflow(value) {
  if (!record(value) || !record(value.actions)) return void 0;
  const document = (raw) => record(raw) && identifier$1(raw.id) && text(raw.version, 100) && text(raw.title, 100) && Array.isArray(raw.paragraphs) && raw.paragraphs.length > 0 && raw.paragraphs.length <= 12 && raw.paragraphs.every((line) => text(line, 1e3));
  if (!document(value.notice) || !document(value.pledge)) return void 0;
  if (!["accept", "decline", "save", "sign", "cancel"].every((key) => identifier$1(value.actions[key]))) return void 0;
  return value;
}
function record(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}
function text(value, max) {
  return typeof value === "string" && !!value.trim() && value.length <= max;
}
function identifier$1(value) {
  return text(value, 100) && value.length >= 2 && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value);
}
function parseAgentFormSurface(content) {
  if (!content.valid || !isRecord(content.value) || content.value.kind !== "form") {
    return void 0;
  }
  const value = content.value;
  const appSlug = identifier(value.app_slug);
  const formId = identifier(value.form_id);
  const instanceId = identifier(value.instance_id);
  const dataRevision = value.data_revision;
  if (!appSlug || !formId || !instanceId || !isSafeRevision(dataRevision)) return void 0;
  if (!isRecord(value.data) || !isRecord(value.schema)) return void 0;
  if (value.ui_schema !== void 0 && !isRecord(value.ui_schema)) return void 0;
  if (!Array.isArray(value.actions) || value.actions.length > 12) return void 0;
  if (value.read_only !== void 0 && typeof value.read_only !== "boolean") return void 0;
  if (value.spec !== void 0 && !isRecord(value.spec)) return void 0;
  const actions = value.actions.flatMap(parseAction);
  if (actions.length !== value.actions.length || new Set(actions.map((action) => action.id)).size !== actions.length) return void 0;
  const errors = parseErrors(value.errors);
  if (value.errors !== void 0 && !errors) return void 0;
  const workflow = parseFormWorkflow(value.workflow);
  if (value.workflow !== void 0 && !workflow) return void 0;
  return {
    kind: "form",
    appSlug,
    formId,
    instanceId,
    dataRevision,
    data: value.data,
    schema: value.schema,
    uiSchema: value.ui_schema,
    readOnly: value.read_only === true,
    actions,
    errors,
    spec: value.spec,
    workflow
  };
}
function parseAction(value) {
  if (!isRecord(value)) return [];
  const id = identifier(value.id);
  if (!id || typeof value.label !== "string" || !value.label.trim() || value.label.length > 100) return [];
  if (value.behavior !== "validate" && value.behavior !== "send") return [];
  let confirmation;
  if (value.confirmation !== void 0) {
    if (value.behavior !== "send" || !isRecord(value.confirmation)) return [];
    const { title, description, confirmLabel } = value.confirmation;
    if (typeof title !== "string" || !title.trim() || title.length > 100 || typeof description !== "string" || !description.trim() || description.length > 500 || typeof confirmLabel !== "string" || !confirmLabel.trim() || confirmLabel.length > 100) return [];
    confirmation = { title, description, confirmLabel };
  }
  return [{ id, label: value.label, behavior: value.behavior, ...confirmation ? { confirmation } : {} }];
}
function parseErrors(value) {
  if (!isRecord(value) || !isSafeRevision(value.data_revision) || !Array.isArray(value.fields) || value.fields.length > 100) return void 0;
  const fields = value.fields.flatMap((item) => {
    if (!isRecord(item) || typeof item.field !== "string" || typeof item.description !== "string" || item.field.length > 400 || item.description.length > 500) return [];
    return [{ field: item.field, description: item.description }];
  });
  return fields.length === value.fields.length ? { dataRevision: value.data_revision, fields } : void 0;
}
function identifier(value) {
  return typeof value === "string" && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value) && value.length >= 2 && value.length <= 100 ? value : void 0;
}
function isSafeRevision(value) {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}
function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
class AgentWorkbenchConnectTransport {
  client;
  tenantId;
  replayLimit;
  sessionId;
  historyLimit = 50;
  expansionPending = false;
  constructor(options) {
    this.tenantId = required$1(options.tenantId, "agent_workbench_tenant_missing");
    this.sessionId = required$1(
      options.sessionId,
      "agent_workbench_session_missing"
    );
    this.replayLimit = options.replayLimit ?? 256;
    if (this.replayLimit < 1) {
      throw new Error("agent_workbench_replay_limit_invalid");
    }
    this.client = createClient(
      AgentWorkbenchService,
      createConnectTransport({
        baseUrl: required$1(options.baseUrl, "agent_workbench_base_url_missing"),
        useBinaryFormat: true,
        binaryOptions: { writeUnknownFields: false },
        fetch: options.fetch,
        interceptors: [bearerTokenInterceptor$1(options.getAccessToken)]
      })
    );
  }
  getSnapshot(signal, minimumHistorySize = 0) {
    this.historyLimit = Math.max(this.historyLimit, minimumHistorySize);
    return this.client.getSessionSnapshot(
      { tenantId: this.tenantId, sessionId: this.sessionId, historyLimit: this.historyLimit },
      { signal }
    );
  }
  async loadOlder(signal, minimumHistorySize = 0) {
    signal?.throwIfAborted();
    this.historyLimit = this.expansionPending ? Math.max(this.historyLimit, minimumHistorySize + 50) : Math.max(this.historyLimit, minimumHistorySize) + 50;
    this.expansionPending = true;
    const snapshot = await this.getSnapshot(signal);
    signal?.throwIfAborted();
    this.expansionPending = false;
    return snapshot;
  }
  streamDeltas(cursor, signal) {
    if (cursor.sessionId !== this.sessionId) {
      throw new Error("agent_workbench_cursor_session_mismatch");
    }
    return this.client.streamSessionDeltas(
      {
        tenantId: this.tenantId,
        cursor,
        replayLimit: this.replayLimit
      },
      { signal }
    );
  }
  execute(command, signal) {
    if (command.sessionId !== this.sessionId) {
      throw new Error("agent_workbench_command_session_mismatch");
    }
    return this.client.executeCommand(
      { tenantId: this.tenantId, command },
      { signal }
    );
  }
}
function isAgentWorkbenchCursorRejected(error) {
  return error instanceof ConnectError && (error.code === Code.FailedPrecondition || error.code === Code.OutOfRange);
}
function bearerTokenInterceptor$1(getAccessToken) {
  return (next) => async (request) => {
    const token = (await getAccessToken()).trim();
    if (!token) throw new Error("agent_workbench_access_token_missing");
    request.header.set("Authorization", `Bearer ${token}`);
    return next(request);
  };
}
function required$1(value, error) {
  const normalized = value.trim();
  if (!normalized) throw new Error(error);
  return normalized.replace(/\/+$/, "");
}
const file_events_v1_events = /* @__PURE__ */ fileDesc("ChZldmVudHMvdjEvZXZlbnRzLnByb3RvEg9wcm90by5ldmVudHMudjEiOgoQU3Vic2NyaWJlUmVxdWVzdBIRCgl0ZW5hbnRfaWQYASABKAkSEwoLZXZlbnRfdHlwZXMYAiADKAkikgIKBUV2ZW50EgwKBHR5cGUYASABKAkSEAoIY2F0ZWdvcnkYAiABKAkSFwoPb3JnYW5pemF0aW9uX2lkGAMgASgDEhsKDnRhcmdldF91c2VyX2lkGAQgASgDSACIAQESFwoPdGFyZ2V0X3VzZXJfaWRzGAUgAygDEhgKC2VudGl0eV90eXBlGAYgASgJSAGIAQESFgoJZW50aXR5X2lkGAcgASgJSAKIAQESEQoJZGF0YV9qc29uGAggASgJEhEKCXRpbWVzdGFtcBgJIAEoAxIRCglrZWVwYWxpdmUYCiABKAhCEQoPX3RhcmdldF91c2VyX2lkQg4KDF9lbnRpdHlfdHlwZUIMCgpfZW50aXR5X2lkMlkKDUV2ZW50c1NlcnZpY2USSAoJU3Vic2NyaWJlEiEucHJvdG8uZXZlbnRzLnYxLlN1YnNjcmliZVJlcXVlc3QaFi5wcm90by5ldmVudHMudjEuRXZlbnQwAUI/Wj1naXRodWIuY29tL2w4YWktY24vYWdlbnRjbG91ZC9wcm90by9nZW4vZ28vZXZlbnRzL3YxO2V2ZW50c3YxYgZwcm90bzM");
const EventsService = /* @__PURE__ */ serviceDesc(file_events_v1_events, 0);
class EventsConnectTransport {
  client;
  tenantId;
  constructor(options) {
    this.tenantId = required(options.tenantId, "events_tenant_missing");
    this.client = createClient(
      EventsService,
      createConnectTransport({
        baseUrl: required(options.baseUrl, "events_base_url_missing"),
        useBinaryFormat: true,
        fetch: options.fetch,
        interceptors: [bearerTokenInterceptor(options.getAccessToken)]
      })
    );
  }
  subscribe(signal) {
    return this.client.subscribe(
      { tenantId: this.tenantId, eventTypes: [] },
      { signal }
    );
  }
}
function bearerTokenInterceptor(getAccessToken) {
  return (next) => async (request) => {
    const token = (await getAccessToken()).trim();
    if (!token) throw new Error("events_access_token_missing");
    request.header.set("Authorization", `Bearer ${token}`);
    return next(request);
  };
}
function required(value, error) {
  const normalized = value.trim();
  if (!normalized) throw new Error(error);
  return normalized.replace(/\/+$/, "");
}
function createInitialConnection() {
  let resolve;
  let reject;
  const promise = new Promise((accept, decline) => {
    resolve = accept;
    reject = decline;
  });
  return { promise, reject, resolve, settled: false };
}
function resolveInitialConnection(initial) {
  if (!initial || initial.settled) return;
  initial.settled = true;
  initial.resolve();
}
function rejectInitialConnection(initial, error) {
  if (!initial || initial.settled) return;
  initial.settled = true;
  initial.reject(error);
}
function sessionCursor(snapshot) {
  return create(SessionCursorSchema, {
    sessionId: snapshot.sessionId,
    streamEpoch: snapshot.streamEpoch,
    revision: snapshot.revision,
    sequence: snapshot.latestSequence
  });
}
function cursorIdentity(snapshot) {
  return [
    snapshot.streamEpoch,
    snapshot.revision.toString(),
    snapshot.latestSequence.toString()
  ].join(":");
}
function asConnectionError(cause) {
  return cause instanceof Error ? cause : new Error(String(cause));
}
const permanentCodes = /* @__PURE__ */ new Set([
  Code.InvalidArgument,
  Code.NotFound,
  Code.PermissionDenied,
  Code.Unauthenticated,
  Code.Unimplemented,
  Code.DataLoss
]);
const permanentStatuses = /* @__PURE__ */ new Set([400, 401, 403, 404, 405, 410, 422]);
function isRetryableAgentSessionError(cause) {
  if (cause instanceof AgentSessionReductionError) {
    return cause.code === "snapshot_stale" || cause.code === "snapshot_epoch_stale";
  }
  if (cause instanceof ConnectError) return !permanentCodes.has(cause.code);
  if (cause && typeof cause === "object") {
    const http = cause;
    if (permanentStatuses.has(http.status ?? http.statusCode ?? 0)) return false;
  }
  return !(cause instanceof Error && /^agent_workbench_.*_(missing|invalid|mismatch)$/.test(cause.message));
}
function agentSessionRetryDelayMs(attempt, random = Math.random) {
  const ceiling = Math.min(250 * 2 ** Math.min(17, Math.max(0, attempt - 1)), 3e4);
  return Math.round(ceiling * (0.5 + random() * 0.5));
}
function defaultAgentSessionRetryDelay(attempt, signal) {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", finish);
      resolve();
    };
    const timer = setTimeout(finish, agentSessionRetryDelayMs(attempt));
    signal.addEventListener("abort", finish, { once: true });
  });
}
class AgentSessionConnection {
  constructor(transport, options = {}) {
    this.transport = transport;
    this.maxConsecutiveFailures = options.maxConsecutiveFailures ?? Infinity;
    if (this.maxConsecutiveFailures !== Infinity && (!Number.isInteger(this.maxConsecutiveFailures) || this.maxConsecutiveFailures < 0)) {
      throw new Error("agent_workbench_retry_limit_invalid");
    }
    this.retryDelay = options.retryDelay ?? defaultAgentSessionRetryDelay;
    this.store = new AgentSessionStore({
      execute: async (command) => {
        const signal = this.activeSignal();
        const receipt = await this.transport.execute(command, signal);
        if (signal.aborted) throw new Error("agent_workbench_connection_closed");
        return receipt;
      },
      requestResync: (sessionId) => this.requestResync(sessionId),
      getSnapshot: (signal, minimumHistorySize) => this.transport.getSnapshot(signal, minimumHistorySize),
      loadOlder: this.transport.loadOlder ? (signal, minimumHistorySize) => this.transport.loadOlder(signal, minimumHistorySize) : void 0
    });
  }
  transport;
  listeners = /* @__PURE__ */ new Set();
  maxConsecutiveFailures;
  retryDelay;
  store;
  error = null;
  status = "idle";
  lifecycle;
  open() {
    if (this.lifecycle && !this.lifecycle.controller.signal.aborted) {
      return this.lifecycle.initial.promise;
    }
    const lifecycle = {
      controller: new AbortController(),
      initial: createInitialConnection(),
      resyncRequested: false
    };
    this.lifecycle = lifecycle;
    this.setStatus("connecting", null);
    void this.run(lifecycle);
    return lifecycle.initial.promise;
  }
  close() {
    const lifecycle = this.lifecycle;
    this.lifecycle = void 0;
    lifecycle?.controller.abort();
    lifecycle?.iteration?.abort();
    rejectInitialConnection(lifecycle?.initial, new Error("agent_workbench_connection_closed"));
    if (!this.lifecycle) this.setStatus("disconnected");
  }
  getStore() {
    return this.store;
  }
  getStatus() {
    return this.status;
  }
  getError() {
    return this.error;
  }
  async loadOlder() {
    const signal = this.activeSignal();
    if (!this.store.getState()?.snapshot.hasOlderHistory) return;
    const lifecycle = this.lifecycle;
    if (lifecycle.historyLoad) return lifecycle.historyLoad;
    lifecycle.resyncRequested = true;
    lifecycle.iteration?.abort();
    this.setStatus("reconnecting");
    const loading = this.store.loadOlder(signal);
    lifecycle.historyLoad = loading;
    try {
      await loading;
    } finally {
      if (lifecycle.historyLoad === loading) lifecycle.historyLoad = void 0;
    }
  }
  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  activeSignal() {
    const signal = this.lifecycle?.controller.signal;
    if (!signal || signal.aborted) throw new Error("agent_workbench_connection_closed");
    return signal;
  }
  async requestResync(sessionId) {
    if (this.store.getState()?.snapshot.sessionId !== sessionId) {
      throw new Error("agent_workbench_resync_session_mismatch");
    }
    const lifecycle = this.lifecycle;
    if (!lifecycle || lifecycle.controller.signal.aborted) return;
    lifecycle.resyncRequested = true;
    this.setStatus("reconnecting");
    lifecycle.iteration?.abort();
  }
  async run(lifecycle) {
    const lifetime = lifecycle.controller.signal;
    let failures = 0;
    let lastCursor = "";
    while (!lifetime.aborted) {
      if (lifecycle.historyLoad) await lifecycle.historyLoad.catch(() => {
      });
      if (lifetime.aborted) return;
      lifecycle.iteration = new AbortController();
      const signal = lifecycle.iteration.signal;
      try {
        const snapshot = await this.transport.getSnapshot(signal, this.store.getState()?.snapshot.history.length);
        if (lifetime.aborted) return;
        if (signal.aborted) throw new Error("agent_workbench_resync_requested");
        try {
          this.store.applySnapshot(snapshot);
        } catch (cause) {
          if (!isSnapshotCursorConflict(cause)) throw cause;
          this.store.replaceSnapshot(snapshot);
        }
        if (lifetime.aborted) return;
        if (signal.aborted) throw new Error("agent_workbench_resync_requested");
        lifecycle.resyncRequested = false;
        const snapshotCursor = cursorIdentity(snapshot);
        if (snapshotCursor !== lastCursor) failures = 0;
        lastCursor = snapshotCursor;
        this.setStatus("connected", null);
        resolveInitialConnection(lifecycle.initial);
        if (lifetime.aborted) return;
        for await (const batch of this.transport.streamDeltas(sessionCursor(snapshot), signal)) {
          if (lifetime.aborted) return;
          if (signal.aborted) break;
          this.store.applyDeltaBatch(batch);
          const state = this.store.getState();
          if (state?.status === "resync_required") break;
          if (state) {
            lastCursor = cursorIdentity(state.snapshot);
            failures = 0;
          }
        }
        if (lifetime.aborted) return;
        if (this.store.getState()?.status === "resync_required") lifecycle.resyncRequested = true;
        throw new Error(lifecycle.resyncRequested ? "agent_workbench_resync_stalled" : "agent_workbench_stream_ended");
      } catch (cause) {
        if (lifetime.aborted) return;
        const resync = lifecycle.resyncRequested || isAgentWorkbenchCursorRejected(cause);
        lifecycle.resyncRequested = false;
        failures += 1;
        if (!resync && !isRetryableAgentSessionError(cause) || failures > this.maxConsecutiveFailures) {
          return this.fail(lifecycle, asConnectionError(cause));
        }
        this.setStatus("reconnecting", resync ? this.error : asConnectionError(cause));
      } finally {
        lifecycle.iteration.abort();
      }
      try {
        await this.retryDelay(failures, lifetime);
      } catch (cause) {
        if (!lifetime.aborted) this.fail(lifecycle, asConnectionError(cause));
        return;
      }
    }
  }
  fail(lifecycle, error) {
    lifecycle.controller.abort();
    lifecycle.iteration?.abort();
    rejectInitialConnection(lifecycle.initial, error);
    if (this.lifecycle === lifecycle) this.setStatus("failed", error);
  }
  setStatus(status, error = this.error) {
    if (this.status === status && this.error === error) return;
    this.status = status;
    this.error = error;
    for (const listener of this.listeners) listener();
  }
}
const PRIORITY = { now: 0, next: 1, later: 2 };
class AgentTaskQueue {
  items = /* @__PURE__ */ new Map();
  sequence = 0;
  order = /* @__PURE__ */ new Map();
  enqueue(input) {
    if (this.items.has(input.commandId)) throw new Error("agent_queue_command_exists");
    const text2 = input.text.trim();
    const attachments = [...input.attachments ?? []];
    if (!text2 && attachments.length === 0) throw new Error("agent_queue_message_empty");
    this.items.set(input.commandId, {
      commandId: input.commandId,
      text: text2,
      attachments,
      priority: input.priority ?? "next",
      status: "queued",
      createdAt: input.createdAt ?? (/* @__PURE__ */ new Date()).toISOString()
    });
    this.order.set(input.commandId, this.sequence++);
  }
  next() {
    return [...this.items.values()].filter((item) => item.status === "queued").sort(
      (left, right) => PRIORITY[left.priority] - PRIORITY[right.priority] || (this.order.get(left.commandId) ?? 0) - (this.order.get(right.commandId) ?? 0)
    )[0];
  }
  markDispatching(commandId) {
    this.patchItem(commandId, { status: "dispatching" });
  }
  fail(commandId, error) {
    this.patchItem(commandId, {
      status: "failed",
      error: error instanceof Error ? error.message : String(error)
    });
  }
  remove(commandId) {
    this.items.delete(commandId);
    this.order.delete(commandId);
  }
  update(commandId, patch) {
    const current = this.items.get(commandId);
    if (!current || current.status !== "queued") return false;
    const text2 = patch.text === void 0 ? current.text : patch.text.trim();
    const attachments = patch.attachments === void 0 ? current.attachments : [...patch.attachments];
    if (!text2 && attachments.length === 0) return false;
    this.items.set(commandId, {
      ...current,
      text: text2,
      attachments
    });
    return true;
  }
  retry(commandId) {
    const current = this.items.get(commandId);
    if (!current || current.status !== "failed") return false;
    this.items.set(commandId, { ...current, status: "queued", error: void 0 });
    return true;
  }
  has(commandId) {
    return this.items.has(commandId);
  }
  clear() {
    this.items.clear();
    this.order.clear();
  }
  cancel(commandId) {
    const item = this.items.get(commandId);
    if (!item || item.status !== "queued") return false;
    this.patchItem(commandId, { status: "cancelled" });
    return true;
  }
  project() {
    const items = [...this.items.values()].sort(
      (left, right) => PRIORITY[left.priority] - PRIORITY[right.priority] || (this.order.get(left.commandId) ?? 0) - (this.order.get(right.commandId) ?? 0)
    );
    return {
      items: items.map((item) => ({ ...item, attachments: [...item.attachments] })),
      pendingCount: items.filter((item) => item.status === "queued").length,
      dispatchingCount: items.filter((item) => item.status === "dispatching").length
    };
  }
  patchItem(commandId, patch) {
    const current = this.items.get(commandId);
    if (!current) throw new Error("agent_queue_command_missing");
    this.items.set(commandId, { ...current, ...patch });
  }
}
function sendPromptPayload(text2, attachments = []) {
  const normalized = text2.trim();
  if (!normalized && attachments.length === 0) {
    throw new Error("agent_workbench_prompt_missing");
  }
  return {
    case: "sendPrompt",
    value: {
      text: normalized,
      attachments: attachments.map((attachment) => ({
        contentId: attachment.id,
        identity: {
          namespace: "agentcloud.session-file",
          semanticKey: "attachment",
          schemaVersion: "1"
        },
        content: {
          case: "file",
          value: {
            artifactId: attachment.id,
            representationId: "source",
            revision: BigInt(1),
            mediaType: attachment.mediaType,
            role: "input",
            filename: attachment.name
          }
        }
      }))
    }
  };
}
function interruptPayload(turnId) {
  return {
    case: "interrupt",
    value: { turnId, reason: "user_requested" }
  };
}
function configurationPayload(patch) {
  const values = Object.entries(patch).map(([key, value]) => ({
    key,
    value: structuredJson(value)
  }));
  if (values.length === 0) {
    throw new Error("agent_workbench_configuration_empty");
  }
  return { case: "changeConfiguration", value: { values } };
}
function permissionPayload(permissionRequestId, resolution) {
  return {
    case: "resolvePermission",
    value: {
      permissionRequestId,
      decision: resolution.action === "accept" ? PermissionDecision.ACCEPT : PermissionDecision.DECLINE,
      response: resolution.action === "accept" ? structuredJson(resolution.content) : void 0
    }
  };
}
function artifactActionPayload(command) {
  if (!command.actionSchemaVersion.trim()) {
    throw new Error("agent_workbench_action_schema_missing");
  }
  return {
    case: "artifactAction",
    value: {
      artifactId: command.artifactId,
      representationId: command.representationId ?? "",
      baseRevision: command.baseRevision,
      clientActionId: command.commandId,
      actionType: command.actionType,
      payload: structuredJson(command.payload),
      actionSchemaVersion: command.actionSchemaVersion
    }
  };
}
function extensionPayload(command) {
  if (!command.namespace.trim() || !command.semanticType.trim() || !command.schemaVersion.trim()) {
    throw new Error("agent_workbench_extension_identity_missing");
  }
  return {
    case: "extension",
    value: {
      namespace: command.namespace,
      semanticType: command.semanticType,
      schemaVersion: command.schemaVersion,
      payload: structuredJson(command.payload)
    }
  };
}
function toolActionPayload(action) {
  if (!action.toolExecutionId.trim() || !action.actionId.trim() || !action.actionType.trim() || !action.schemaVersion.trim()) {
    throw new Error("agent_workbench_tool_action_identity_missing");
  }
  return {
    case: "toolAction",
    value: {
      actionId: action.actionId,
      actionType: action.actionType,
      schemaVersion: action.schemaVersion,
      toolExecutionId: action.toolExecutionId,
      payload: structuredJson(action.payload ?? {})
    }
  };
}
function structuredJson(value) {
  return {
    mediaType: "application/json",
    data: new TextEncoder().encode(
      JSON.stringify(
        value,
        (_, item) => typeof item === "bigint" ? item.toString() : item
      )
    )
  };
}
function sessionFormState(snapshot, appSlug, formId, instanceId) {
  for (const artifact of snapshot?.artifacts ?? []) {
    const extension = artifact.manifest?.manifest;
    if (artifact.provenance?.producerNamespace !== "agentcloud.backend" || extension?.case !== "extension") continue;
    const manifest = extension.value;
    if (manifest.namespace !== "agentcloud.ui" || manifest.semanticType !== "form-state" || manifest.schemaVersion !== "1" || !manifest.payload) continue;
    try {
      const raw = new TextDecoder().decode(manifest.payload.data);
      const value = JSON.parse(raw);
      const document = parseAgentFormSurface({ kind: "json", id: artifact.artifactId, mediaType: "application/json", valid: true, value, raw });
      if (!document || document.appSlug !== appSlug || document.formId !== formId || document.instanceId !== instanceId) continue;
      document.gates = value.gates;
      document.datePolicy = value.date_policy;
      const result = value.result;
      if (result?.status !== "saved" || typeof result.command_id !== "string" || typeof result.message !== "string" || !result.submitted_data || typeof result.submitted_data !== "object") continue;
      return { document, result: { commandId: result.command_id, actionId: result.action_id, message: result.message, submittedData: result.submitted_data, notificationStatus: result.notification_status } };
    } catch {
      continue;
    }
  }
}
function sessionFormBindings(connection, requireSession, execute) {
  return {
    executeExtension: async (sessionId, commandId, command) => {
      const receipt = await execute(sessionId, commandId, extensionPayload(command));
      return { status: receipt.state === CommandReceiptState.SUCCEEDED ? "succeeded" : "accepted" };
    },
    getFormState: (sessionId, appSlug, formId, instanceId) => {
      requireSession(sessionId);
      return sessionFormState(connection.getStore().getState()?.snapshot, appSlug, formId, instanceId);
    }
  };
}
class PendingPromptProjection {
  prompts = /* @__PURE__ */ new Map();
  add(commandId, prompt) {
    if (this.prompts.has(commandId)) throw new Error("agent_prompt_already_pending");
    this.prompts.set(commandId, { ...prompt, attachments: prompt.attachments?.map((file) => ({ ...file })) });
  }
  fail(commandId) {
    const prompt = this.prompts.get(commandId);
    if (prompt) prompt.failed = true;
  }
  remove(commandId) {
    this.prompts.delete(commandId);
  }
  clear() {
    this.prompts.clear();
  }
  project(view, source) {
    if (!source) return view;
    const receipts = new Map(source.commandReceipts.map((receipt) => [receipt.commandId, receipt]));
    const messageCommands = new Map(source.history.flatMap((item) => {
      const commandId = item.envelope?.causationCommandId;
      return commandId && item.content?.content.case === "message" && item.content.content.value.role === MessageRole.USER ? [[item.envelope.itemId, commandId]] : [];
    }));
    const acknowledged = new Set(messageCommands.values());
    const items = view.items.map((item) => {
      const commandId = messageCommands.get(item.id);
      return item.kind === "message" && item.role === "user" && commandId && failed(receipts.get(commandId)?.state) ? { ...item, status: "failed" } : item;
    });
    for (const [commandId, prompt] of this.prompts) {
      if (prompt.epoch !== source.streamEpoch || acknowledged.has(commandId)) {
        this.prompts.delete(commandId);
        continue;
      }
      const status = prompt.failed || failed(receipts.get(commandId)?.state) ? "failed" : "pending";
      items.push(...pendingItems(commandId, prompt, status));
    }
    return { ...view, items };
  }
}
function failed(state) {
  return state === CommandReceiptState.FAILED || state === CommandReceiptState.REJECTED || state === CommandReceiptState.CANCELLED;
}
function pendingItems(commandId, prompt, status) {
  const id = `message:${commandId}`;
  return [
    { id, kind: "message", role: "user", text: prompt.text, status },
    ...(prompt.attachments ?? []).map((file) => ({
      id: `${id}:${file.id}`,
      kind: "attachment",
      attachmentId: file.id,
      filename: file.name,
      mimeType: file.mediaType,
      representationId: "source"
    }))
  ];
}
class AgentInteractionStore {
  events = /* @__PURE__ */ new Map();
  clear() {
    this.events.clear();
  }
  get(id) {
    return this.events.get(id);
  }
  set(event) {
    this.events.set(event.id, event);
  }
  updateStatus(commandId, status, updatedAt) {
    const id = `interaction:${commandId}`;
    const current = this.events.get(id);
    if (!current) return void 0;
    const next = { ...current, status, updatedAt };
    this.events.set(id, next);
    return next;
  }
  values() {
    return [...this.events.values()];
  }
  merge(persisted) {
    const merged = new Map(persisted.map((item) => [item.id, item]));
    for (const item of this.events.values()) {
      const previous = merged.get(item.id);
      if (previous && isTerminalInteraction(previous.status) && !isTerminalInteraction(item.status)) continue;
      merged.set(item.id, item);
    }
    return [...merged.values()];
  }
}
function isTerminalInteraction(status) {
  return status === "succeeded" || status === "failed" || status === "rejected" || status === "cancelled";
}
class AgentInteractionExecutor {
  constructor(store, now, refresh) {
    this.store = store;
    this.now = now;
    this.refresh = refresh;
  }
  store;
  now;
  refresh;
  async execute(kind, commandId, sourceId, operation) {
    this.store.set({
      commandId,
      id: `interaction:${commandId}`,
      kind,
      ...sourceId ? { sourceId } : {},
      status: "pending",
      updatedAt: this.now()
    });
    this.store.updateStatus(commandId, "running", this.now());
    this.refresh();
    try {
      const result = await operation();
      this.store.updateStatus(commandId, interactionStatus(result), this.now());
      this.refresh();
      return result;
    } catch (cause) {
      const current = this.store.get(`interaction:${commandId}`);
      if (current) {
        this.store.set({
          ...current,
          error: cause instanceof Error ? cause.message : String(cause),
          status: "failed",
          updatedAt: this.now()
        });
      }
      this.refresh();
      throw cause;
    }
  }
}
function interactionStatus(result) {
  if (result && typeof result === "object" && "state" in result) {
    switch (result.state) {
      case CommandReceiptState.REJECTED:
        return "rejected";
      case CommandReceiptState.CANCELLED:
        return "cancelled";
      case CommandReceiptState.FAILED:
        return "failed";
      case CommandReceiptState.SUCCEEDED:
        return "succeeded";
      default:
        return "running";
    }
  }
  if (result && typeof result === "object" && "status" in result && result.status === "succeeded") {
    return "succeeded";
  }
  return "running";
}
const scheduleSessionFrame = (callback) => {
  if (typeof requestAnimationFrame === "function") {
    const handle2 = requestAnimationFrame(callback);
    return () => cancelAnimationFrame(handle2);
  }
  const handle = setTimeout(callback, 0);
  return () => clearTimeout(handle);
};
class SessionFrameNotifier {
  constructor(notify, schedule = scheduleSessionFrame) {
    this.notify = notify;
    this.schedule = schedule;
  }
  notify;
  schedule;
  scheduled;
  request() {
    if (this.scheduled) return;
    const task = {};
    this.scheduled = task;
    task.cancel = this.schedule(() => {
      if (this.scheduled !== task) return;
      this.scheduled = void 0;
      this.notify();
    });
  }
  flush() {
    this.cancel();
    this.notify();
  }
  cancel() {
    const task = this.scheduled;
    this.scheduled = void 0;
    task?.cancel?.();
  }
}
function bindSessionResources(options, requireSession) {
  const bindings = {};
  if (options.loadArtifact) bindings.loadArtifact = (sessionId, artifactId, representationId) => {
    requireSession(sessionId);
    return options.loadArtifact({ sessionId, artifactId, representationId });
  };
  if (options.loadAttachment) bindings.loadAttachment = (sessionId, attachmentId) => {
    requireSession(sessionId);
    return options.loadAttachment(sessionId, attachmentId);
  };
  if (options.loadPreviewUrl) bindings.loadPreviewUrl = (item, sessionId) => {
    requireSession(sessionId);
    return options.loadPreviewUrl(item, sessionId);
  };
  if (options.uploadAttachment) bindings.uploadAttachment = (sessionId, file) => {
    requireSession(sessionId);
    return options.uploadAttachment(sessionId, file);
  };
  return bindings;
}
class AgentCommandReceiptError extends Error {
  constructor(receipt) {
    super(receipt.error?.message || receiptFailureMessage(receipt.state));
    this.receipt = receipt;
    this.name = "AgentCommandReceiptError";
  }
  receipt;
}
function throwForFailedCommandReceipt(receipt) {
  if (!isFailedCommandReceipt(receipt.state)) return;
  throw new AgentCommandReceiptError(receipt);
}
function isFailedCommandReceipt(state) {
  return state === CommandReceiptState.FAILED || state === CommandReceiptState.REJECTED || state === CommandReceiptState.CANCELLED;
}
function receiptFailureMessage(state) {
  switch (state) {
    case CommandReceiptState.REJECTED:
      return "agent_workbench_command_rejected";
    case CommandReceiptState.CANCELLED:
      return "agent_workbench_command_cancelled";
    default:
      return "agent_workbench_command_failed";
  }
}
const commands = /* @__PURE__ */ new WeakMap();
function retrySessionCommand(connection, input) {
  let cache = commands.get(connection);
  if (!cache) {
    cache = /* @__PURE__ */ new Map();
    commands.set(connection, cache);
  }
  const key = `${input.sessionId}:${input.commandId}`;
  const existing = cache.get(key);
  if (existing) return existing.then((previous) => {
    if (previous.streamEpoch !== input.streamEpoch) throw new Error("agent_workbench_command_epoch_changed");
    const expected = create(CommandEnvelopeSchema, { command: input.command });
    const original = create(CommandEnvelopeSchema, { command: previous.command });
    if (!equals(CommandEnvelopeSchema, expected, original)) throw new Error("agent_workbench_command_id_reused");
    return previous;
  });
  const pending = createAgentCommandEnvelope(input);
  cache.set(key, pending);
  void pending.catch(() => {
    if (cache.get(key) === pending) cache.delete(key);
  });
  return pending;
}
function forgetRejectedSessionCommand(connection, envelope) {
  const cache = commands.get(connection);
  const key = `${envelope.sessionId}:${envelope.commandId}`;
  const pending = cache?.get(key);
  if (pending) void pending.then((stored) => {
    if (stored === envelope && cache?.get(key) === pending) cache.delete(key);
  });
}
async function executeSessionCommand(connection, sessionId, commandId, command, issuedAt, isCurrent) {
  const state = connection.getStore().getState();
  if (!state || state.status !== "ready") throw new Error("agent_workbench_session_not_ready");
  const envelope = await retrySessionCommand(connection, {
    sessionId,
    commandId,
    command,
    issuedAt,
    streamEpoch: state.snapshot.streamEpoch,
    expectedRevision: command?.case === "interrupt" ? void 0 : state.snapshot.revision
  });
  if (!isCurrent()) throw new Error("agent_workbench_connection_closed");
  try {
    const receipt = await connection.getStore().execute(envelope);
    throwForFailedCommandReceipt(receipt);
    return receipt;
  } catch (error) {
    if (error instanceof ConnectError && error.code === Code.FailedPrecondition) forgetRejectedSessionCommand(connection, envelope);
    throw error;
  }
}
async function sendAgentPrompt(options) {
  const payload = sendPromptPayload(options.input.text, options.input.attachments);
  const state = options.connection.getStore().getState();
  if (!state || state.status !== "ready") throw new Error("agent_workbench_session_not_ready");
  options.pending.add(options.commandId, {
    ...options.input,
    text: options.input.text.trim(),
    epoch: state.snapshot.streamEpoch
  });
  options.notifier.flush();
  try {
    await options.execute(options.sessionId, options.commandId, payload);
  } catch (error) {
    if (options.isCurrent(options.generation)) {
      if (error instanceof AgentCommandReceiptError) options.pending.fail(options.commandId);
      else options.pending.remove(options.commandId);
    }
    throw error;
  } finally {
    if (options.isCurrent(options.generation)) options.notifier.flush();
  }
}
function createAgentSessionRuntimeCommands(context) {
  const sendMessage = async (sessionId, commandId, input) => {
    context.requireSession(sessionId);
    await sendAgentPrompt({
      connection: context.connection,
      commandId,
      execute: context.execute,
      generation: context.getGeneration(),
      input,
      isCurrent: context.isCurrent,
      notifier: context.notifier,
      pending: context.pending,
      sessionId
    });
  };
  return {
    sendMessage,
    sendSlashCommand: (sessionId, commandId, input) => sendMessage(sessionId, commandId, {
      text: `/${input.name}${input.arguments ? ` ${input.arguments}` : ""}`
    }),
    interrupt: async (sessionId, commandId) => {
      const turnId = context.connection.getStore().getState()?.snapshot.activeTurnId;
      await context.execute(sessionId, commandId, interruptPayload(turnId));
    },
    resolvePermission: async (sessionId, commandId, permissionId, result) => {
      await context.interactionExecutor.execute("permission", commandId, permissionId, () => context.execute(sessionId, commandId, permissionPayload(permissionId, result)));
    },
    updateConfiguration: async (sessionId, commandId, patch) => {
      await context.execute(sessionId, commandId, configurationPayload(patch));
    },
    executeArtifactAction: async (sessionId, command) => {
      await context.interactionExecutor.execute("artifact_action", command.commandId, command.artifactId, () => context.execute(sessionId, command.commandId, artifactActionPayload(command)));
    }
  };
}
class AgentSessionQueueController {
  constructor(options) {
    this.options = options;
  }
  options;
  draining = false;
  awaitingCommandId;
  dispatchToken = 0;
  enqueue(input) {
    this.options.queue.enqueue(input);
    this.options.refresh();
    this.trigger();
  }
  cancel(commandId) {
    const cancelled = this.options.queue.cancel(commandId);
    if (cancelled) this.options.refresh();
    return cancelled;
  }
  update(commandId, input) {
    const updated = this.options.queue.update(commandId, input);
    if (updated) this.options.refresh();
    return updated;
  }
  retry(commandId) {
    const retried = this.options.queue.retry(commandId);
    if (retried) {
      this.options.refresh();
      this.trigger();
    }
    return retried;
  }
  remove(commandId) {
    const item = this.options.queue.project().items.find(
      (candidate) => candidate.commandId === commandId
    );
    if (!item || item.status === "dispatching") return false;
    this.options.queue.remove(commandId);
    this.options.refresh();
    return true;
  }
  clear() {
    this.dispatchToken++;
    this.options.queue.clear();
    this.awaitingCommandId = void 0;
  }
  trigger() {
    void this.drain();
  }
  async drain() {
    if (this.draining || this.options.isClosed()) return;
    if (!this.canDispatch()) return;
    const item = this.options.queue.next();
    if (!item) return;
    this.draining = true;
    this.awaitingCommandId = item.commandId;
    const dispatchToken = ++this.dispatchToken;
    this.options.queue.markDispatching(item.commandId);
    this.options.refresh();
    try {
      await sendAgentPrompt({
        connection: this.options.connection,
        commandId: item.commandId,
        execute: this.options.execute,
        generation: this.options.getGeneration(),
        input: { text: item.text, attachments: [...item.attachments] },
        isCurrent: this.options.isCurrent,
        notifier: this.options.notifier,
        pending: this.options.pending,
        sessionId: this.options.sessionId
      });
    } catch (cause) {
      if (dispatchToken === this.dispatchToken && !this.options.isClosed() && this.options.queue.has(item.commandId)) {
        this.options.queue.fail(item.commandId, cause);
      }
      if (dispatchToken === this.dispatchToken) this.awaitingCommandId = void 0;
    } finally {
      this.draining = false;
      this.options.refresh();
      this.trigger();
    }
  }
  canDispatch() {
    if (this.awaitingCommandId) {
      const progress = this.options.getCommandProgress(this.awaitingCommandId);
      if (progress.status === "failed") {
        this.options.queue.fail(this.awaitingCommandId, progress.error);
        this.awaitingCommandId = void 0;
      } else if (progress.status === "complete") {
        this.options.queue.remove(this.awaitingCommandId);
        this.awaitingCommandId = void 0;
      } else {
        return false;
      }
    }
    return this.options.canDispatchSession();
  }
}
function createAgentSessionQueueBinding(options) {
  const controller = new AgentSessionQueueController({
    connection: options.connection,
    notifier: options.notifier,
    pending: options.pending,
    queue: options.queue,
    sessionId: options.sessionId,
    execute: options.execute,
    getGeneration: options.getGeneration,
    isCurrent: options.isCurrent,
    isClosed: options.isClosed,
    getCommandProgress: (commandId) => getCommandProgress(options, commandId),
    canDispatchSession: () => canDispatchSession(options),
    refresh: options.refresh
  });
  const queueAction = (sessionId, action) => {
    options.requireSession(sessionId);
    return action();
  };
  const enqueue = (sessionId, commandId, input, priority) => {
    options.requireSession(sessionId);
    if (options.isClosed()) throw new Error("agent_workbench_session_not_ready");
    controller.enqueue({ commandId, ...input, priority });
  };
  return {
    controller,
    enqueueMessage: async (sessionId, commandId, input, priority = "next") => enqueue(sessionId, commandId, input, priority),
    enqueueSlashCommand: async (sessionId, commandId, input, priority = "next") => enqueue(
      sessionId,
      commandId,
      { text: `/${input.name}${input.arguments ? ` ${input.arguments}` : ""}` },
      priority
    ),
    cancelQueuedMessage: (sessionId, commandId) => queueAction(sessionId, () => controller.cancel(commandId)),
    updateQueuedMessage: (sessionId, commandId, input) => queueAction(sessionId, () => controller.update(commandId, input)),
    retryQueuedMessage: (sessionId, commandId) => queueAction(sessionId, () => controller.retry(commandId)),
    removeQueuedMessage: (sessionId, commandId) => queueAction(sessionId, () => controller.remove(commandId))
  };
}
function getCommandProgress(options, commandId) {
  const state = options.connection.getStore().getState();
  if (!state || state.status !== "ready") return { status: "pending" };
  const receipt = state.snapshot.commandReceipts.find(
    (candidate) => candidate.commandId === commandId
  );
  if (receipt?.state === CommandReceiptState.FAILED || receipt?.state === CommandReceiptState.REJECTED || receipt?.state === CommandReceiptState.CANCELLED) {
    return {
      status: "failed",
      error: receipt.error?.message || `agent_queue_command_${receipt.state}`
    };
  }
  const receivedHistory = state.snapshot.history.some(
    (item) => item.envelope?.causationCommandId === commandId
  );
  const sessionIdle = state.snapshot.status !== SessionStatus.RUNNING && state.snapshot.status !== SessionStatus.WAITING;
  return receivedHistory && sessionIdle ? { status: "complete" } : { status: "pending" };
}
function canDispatchSession(options) {
  const state = options.connection.getStore().getState();
  if (!state || state.status !== "ready") return false;
  if (state.snapshot.status === SessionStatus.RUNNING || state.snapshot.status === SessionStatus.WAITING) {
    return false;
  }
  return (options.projectSnapshot().execution?.runningCount ?? 0) === 0;
}
function projectRepresentations(representations) {
  return representations.map((representation) => ({
    representationId: representation.representationId,
    revision: representation.revision,
    mediaType: representation.mediaType,
    ...representation.role ? { role: representation.role } : {},
    ...representation.filename ? { filename: representation.filename } : {},
    status: representationStatus(representation.status),
    ...representation.byteSize !== void 0 ? { byteSize: representation.byteSize } : {},
    ...representation.dimensions ? { dimensions: { ...representation.dimensions } } : {},
    ...representation.durationMillis !== void 0 ? { durationMillis: representation.durationMillis } : {},
    ...representation.digest ? { digest: representation.digest } : {}
  }));
}
function projectGrants(grants) {
  return grants.map((grant) => ({
    grantId: grant.grantId,
    representationIds: [...grant.representationIds],
    actions: [...grant.actions],
    ...grant.issuer ? { issuer: grant.issuer } : {},
    ...grant.subject ? { subject: grant.subject } : {},
    ...grant.minimumRevision !== void 0 ? { minimumRevision: grant.minimumRevision } : {},
    ...grant.maximumRevision !== void 0 ? { maximumRevision: grant.maximumRevision } : {},
    ...grant.issuedAt ? { issuedAt: grant.issuedAt } : {},
    ...grant.expiresAt ? { expiresAt: grant.expiresAt } : {}
  }));
}
function projectGrantActions(grants, revision) {
  return [
    ...new Set(
      grants.filter((grant) => revisionApplies(grant, revision)).flatMap((grant) => grant.actions)
    )
  ];
}
function projectArtifactStatus(status) {
  if (status === ArtifactStatus.QUEUED) return "queued";
  if (status === ArtifactStatus.PROCESSING) return "processing";
  if (status === ArtifactStatus.READY) return "completed";
  return "failed";
}
function revisionApplies(grant, revision) {
  if (grant.minimumRevision !== void 0 && revision < grant.minimumRevision) {
    return false;
  }
  return grant.maximumRevision === void 0 || revision <= grant.maximumRevision;
}
function representationStatus(status) {
  if (status === ArtifactStatus.QUEUED) return "queued";
  if (status === ArtifactStatus.PROCESSING) return "processing";
  if (status === ArtifactStatus.READY) return "ready";
  if (status === ArtifactStatus.FAILED) return "failed";
  if (status === ArtifactStatus.DELETED) return "deleted";
  return "unknown";
}
function projectManifest(manifest) {
  const value = manifest?.manifest;
  if (!value?.case) return null;
  if (value.case === "imageEdit") {
    return {
      kind: "image_edit",
      sourceRepresentationId: value.value.sourceRepresentationId,
      ...value.value.resultRepresentationId ? { resultRepresentationId: value.value.resultRepresentationId } : {},
      candidateRepresentationIds: [...value.value.candidateRepresentationIds],
      ...value.value.maskRepresentationId ? { maskRepresentationId: value.value.maskRepresentationId } : {},
      sourceDimensions: {
        width: value.value.sourceWidth,
        height: value.value.sourceHeight
      },
      ...value.value.exifOrientation ? { exifOrientation: value.value.exifOrientation } : {},
      regions: value.value.regions.map((region) => ({ ...region })),
      annotations: value.value.annotations.map((annotation) => ({
        annotationId: annotation.annotationId,
        path: annotation.path.map((point) => ({ ...point })),
        ...annotation.label ? { label: annotation.label } : {},
        ...annotation.style ? { style: structuredPayload(annotation.style) } : {}
      }))
    };
  }
  if (value.case === "video") {
    return {
      kind: "video",
      stage: videoStage(value.value.stage),
      ...value.value.progressFraction !== void 0 ? { progressFraction: value.value.progressFraction } : {},
      ...value.value.durationMillis !== void 0 ? { durationMillis: value.value.durationMillis } : {},
      ...value.value.dimensions ? { dimensions: { ...value.value.dimensions } } : {},
      ...value.value.originalRepresentationId ? { originalRepresentationId: value.value.originalRepresentationId } : {},
      ...value.value.playableRepresentationId ? { playableRepresentationId: value.value.playableRepresentationId } : {},
      ...value.value.posterRepresentationId ? { posterRepresentationId: value.value.posterRepresentationId } : {},
      thumbnailRepresentationIds: [...value.value.thumbnailRepresentationIds],
      derivativeRepresentationIds: [...value.value.derivativeRepresentationIds]
    };
  }
  if (value.case === "presentation") {
    return {
      kind: "presentation",
      deckRevision: value.value.deckRevision,
      slides: value.value.slides.map((slide) => ({ ...slide })),
      versions: value.value.versions.map((version, index) => ({
        id: version.versionId,
        revision: version.revision,
        label: version.label || `Version ${index + 1}`
      })),
      ...value.value.selectedVersionId ? { selectedVersionId: value.value.selectedVersionId } : {}
    };
  }
  if (value.case === "extension") {
    return {
      kind: "extension",
      namespace: value.value.namespace,
      semanticType: value.value.semanticType,
      schemaVersion: value.value.schemaVersion,
      ...value.value.payload ? { payload: structuredPayload(value.value.payload) } : {}
    };
  }
  return {
    kind: "unsupported",
    reason: unsupportedReason$1(value.value.reason),
    ...value.value.identity ? { identity: { ...value.value.identity } } : {},
    ...value.value.payload ? { payload: structuredPayload(value.value.payload) } : {}
  };
}
function structuredPayload(payload) {
  return {
    mediaType: payload.mediaType,
    data: new Uint8Array(payload.data)
  };
}
function unsupportedReason$1(reason) {
  if (reason === UnsupportedReason.UNKNOWN) return "unknown";
  if (reason === UnsupportedReason.UNSUPPORTED) return "unsupported";
  if (reason === UnsupportedReason.INVALID) return "invalid";
  return "unspecified";
}
function videoStage(stage) {
  if (stage === VideoStage.QUEUED) return "queued";
  if (stage === VideoStage.RENDERING) return "rendering";
  if (stage === VideoStage.TRANSCODING) return "transcoding";
  if (stage === VideoStage.READY) return "ready";
  if (stage === VideoStage.FAILED) return "failed";
  return "unknown";
}
function selectArtifactRepresentation(descriptor, requestedId) {
  if (requestedId) {
    return descriptor.representations.find(
      (representation) => representation.representationId === requestedId
    );
  }
  const manifestId = manifestRepresentationId(descriptor);
  if (manifestId) {
    return descriptor.representations.find(
      (representation) => representation.representationId === manifestId
    );
  }
  return descriptor.representations.find(
    (representation) => representation.status === ArtifactStatus.READY && representation.representationId === "preview-pdf"
  ) ?? descriptor.representations.find(
    (representation) => representation.status === ArtifactStatus.READY && representation.role === "primary"
  ) ?? descriptor.representations.find(
    (representation) => representation.status === ArtifactStatus.READY
  ) ?? descriptor.representations[0];
}
function extensionSchemaVersion(descriptor) {
  const manifest = descriptor.manifest?.manifest;
  return manifest?.case === "extension" ? manifest.value.schemaVersion : void 0;
}
function manifestRepresentationId(descriptor) {
  const manifest = descriptor.manifest?.manifest;
  if (!manifest?.case) return void 0;
  if (manifest.case === "imageEdit") {
    return manifest.value.resultRepresentationId ?? manifest.value.candidateRepresentationIds[0] ?? manifest.value.sourceRepresentationId;
  }
  if (manifest.case === "video") {
    return manifest.value.playableRepresentationId ?? manifest.value.originalRepresentationId ?? manifest.value.posterRepresentationId;
  }
  if (manifest.case !== "presentation") return void 0;
  const selected = manifest.value.versions.find(
    (version) => version.versionId === manifest.value.selectedVersionId
  );
  const revision = descriptor.revisions.find(
    (candidate) => candidate.revision === (selected?.revision ?? manifest.value.deckRevision)
  );
  return revision?.representationIds[0] ?? manifest.value.slides.find((slide) => slide.pageRepresentationId)?.pageRepresentationId;
}
function createArtifactCatalog(artifacts) {
  return new Map(artifacts.map((artifact) => [artifact.artifactId, artifact]));
}
function projectArtifactDescriptor(descriptor, id, options = {}) {
  const representation = selectArtifactRepresentation(
    descriptor,
    options.representationId
  );
  const representations = projectRepresentations(descriptor.representations);
  const grants = projectGrants(descriptor.grants);
  const selectedRepresentationId = representation?.representationId ?? options.representationId ?? null;
  return {
    actions: projectGrantActions(grants, descriptor.revision),
    id,
    kind: "artifact",
    artifactId: descriptor.artifactId,
    filename: representation?.representationId === "preview-pdf" ? descriptor.filename : representation?.filename || descriptor.filename,
    grants,
    manifest: projectManifest(descriptor.manifest),
    mimeType: representation?.mediaType || descriptor.mediaType || null,
    provenance: {
      ...currentRevisionToolExecutionId(descriptor) ? {
        publicationToolExecutionId: currentRevisionToolExecutionId(descriptor)
      } : {},
      ...currentRevisionCommandId(descriptor) ? { commandId: currentRevisionCommandId(descriptor) } : {},
      ...descriptor.provenance?.producerId ? { producerId: descriptor.provenance.producerId } : {},
      ...descriptor.provenance?.producerNamespace ? { producerNamespace: descriptor.provenance.producerNamespace } : {},
      ...descriptor.provenance?.producerType ? { producerType: descriptor.provenance.producerType } : {}
    },
    representations,
    revision: descriptor.revision,
    role: options.role || descriptor.role || representation?.role || "artifact",
    schemaVersion: options.schemaVersion || extensionSchemaVersion(descriptor) || "1",
    selectedRepresentationId,
    status: projectArtifactStatus(representation?.status ?? descriptor.status),
    ...currentRevisionToolExecutionId(descriptor) ? {
      execution: {
        stepId: currentRevisionToolExecutionId(descriptor)
      }
    } : {}
  };
}
function currentRevisionToolExecutionId(descriptor) {
  return descriptor.revisions.find(
    (revision) => revision.revision === descriptor.revision
  )?.provenance?.toolExecutionId;
}
function currentRevisionCommandId(descriptor) {
  return descriptor.revisions.find(
    (revision) => revision.revision === descriptor.revision
  )?.provenance?.commandId;
}
function projectArtifactReference(reference, id, catalog, options = {}) {
  if (!reference) return missingArtifact(id, "artifact reference is missing");
  const descriptor = catalog.get(reference.artifactId);
  if (!descriptor) {
    return missingArtifact(
      id,
      `artifactId=${reference.artifactId}; representationId=${reference.representationId ?? "unspecified"}`
    );
  }
  if (reference.revision !== void 0 && descriptor.revision !== reference.revision) {
    return missingArtifact(
      id,
      `artifactId=${reference.artifactId}; revision=${reference.revision.toString()}; currentRevision=${descriptor.revision.toString()}`
    );
  }
  if (reference.representationId && !descriptor.representations.some(
    (representation) => representation.representationId === reference.representationId
  )) {
    return missingArtifact(
      id,
      `artifactId=${reference.artifactId}; representationId=${reference.representationId}`
    );
  }
  return projectArtifactDescriptor(descriptor, id, {
    representationId: reference.representationId,
    role: reference.role,
    schemaVersion: options.schemaVersion
  });
}
function missingArtifact(id, detail) {
  return {
    id,
    kind: "system",
    title: "Unsupported artifact",
    detail,
    status: "failed"
  };
}
const decoder = new TextDecoder();
function decodeStructuredPayload(payload) {
  if (!payload) return void 0;
  const text2 = decoder.decode(payload.data);
  if (!isJsonMediaType(payload.mediaType)) return { text: text2, value: text2 };
  try {
    const value = JSON.parse(text2);
    return { text: stringifyValue(value), value };
  } catch {
    return { text: text2, value: text2 };
  }
}
function formatStructuredPayload(payload) {
  return decodeStructuredPayload(payload)?.text;
}
function formatUnsupported(value) {
  const identity = formatContentIdentity(value.identity);
  const reason = unsupportedReason(value.reason);
  const payload = value.payload;
  if (!payload) return `${identity}; reason=${reason}; payload=missing`;
  const decoded = decodeStructuredPayload(payload)?.text ?? "";
  const bytes = Array.from(
    payload.data,
    (byte) => byte.toString(16).padStart(2, "0")
  ).join(" ");
  return [
    identity,
    `reason=${reason}`,
    `mediaType=${payload.mediaType || "unknown"}`,
    decoded ? `utf8=${decoded}` : void 0,
    `bytes=${bytes || "(empty)"}`
  ].filter(Boolean).join("; ");
}
function formatAgentError(error) {
  if (!error) return null;
  const detail = formatAgentErrorDetail(error);
  return `[${error.code || "agent_error"}] ${detail}`;
}
function formatAgentErrorDetail(error) {
  const details = formatStructuredPayload(error.details);
  const violations = error.fieldViolations.map((violation) => `${violation.field}: ${violation.description}`).join("\n");
  return [error.message, details, violations].filter(Boolean).join("\n");
}
function stringifyValue(value) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(
      value,
      (_, item) => typeof item === "bigint" ? item.toString() : item,
      2
    );
  } catch {
    return String(value);
  }
}
function formatContentIdentity(identity) {
  if (!identity) return "identity=missing";
  return [
    `namespace=${identity.namespace || "missing"}`,
    `semanticKey=${identity.semanticKey || "missing"}`,
    `schemaVersion=${identity.schemaVersion || "missing"}`,
    identity.sourceType ? `sourceType=${identity.sourceType}` : void 0
  ].filter(Boolean).join(", ");
}
function unsupportedReason(reason) {
  if (reason === UnsupportedReason.UNKNOWN) return "unknown";
  if (reason === UnsupportedReason.UNSUPPORTED) return "unsupported";
  if (reason === UnsupportedReason.INVALID) return "invalid";
  return "unspecified";
}
function isJsonMediaType(mediaType) {
  const normalized = mediaType.toLowerCase().split(";")[0]?.trim() ?? "";
  return normalized === "application/json" || normalized.endsWith("+json");
}
const nonChatModelId = /embed|kling|wan-|hailuo|veo|sora|video|speech|tts|music|voice|lyrics|audio|realtime|image|seedream|nano-banana|midjourney/i;
const blockedModelId = "codex-auto-review";
function isUserSelectableModel(modelId) {
  const normalized = modelId.trim().toLowerCase();
  return normalized !== "" && normalized !== blockedModelId;
}
function isChatModelOption(modelId) {
  const normalized = modelId.trim();
  return isUserSelectableModel(normalized) && normalized !== "" && !nonChatModelId.test(normalized);
}
function selectableChatModels(models, current) {
  const currentValue = current?.trim() ?? "";
  const seen = /* @__PURE__ */ new Set();
  const out = [];
  for (const model of models) {
    const id = model.trim();
    if (!id || seen.has(id)) continue;
    if (!isUserSelectableModel(id)) continue;
    if (id !== currentValue && !isChatModelOption(id)) continue;
    seen.add(id);
    out.push(id);
  }
  if (currentValue && !seen.has(currentValue) && isUserSelectableModel(currentValue)) {
    out.unshift(currentValue);
  }
  return out;
}
function projectConfiguration(snapshot) {
  const configuration = snapshot.configuration;
  const capabilities = snapshot.capabilities;
  if (!capabilities) return void 0;
  const controls = [];
  appendControl(
    controls,
    "model",
    "Model",
    configuration?.model,
    capabilities.models
  );
  appendControl(
    controls,
    "permission_mode",
    "Permissions",
    configuration?.permissionMode,
    capabilities.permissionModes
  );
  return controls.length > 0 ? controls : void 0;
}
function appendControl(controls, id, label, value, options) {
  const listed = id === "model" ? selectableChatModels(options, value) : [...options];
  if (listed.length === 0) return;
  controls.push({
    id,
    label,
    value: value && listed.includes(value) ? value : "",
    options: listed.map((option) => ({
      value: option,
      label: id === "model" ? modelOptionLabel(option) : option
    }))
  });
}
function modelOptionLabel(model) {
  const separator = model.lastIndexOf("/");
  if (separator <= 0 || separator === model.length - 1) return model;
  return model.slice(separator + 1);
}
function projectSessionStatus(status) {
  if (status === SessionStatus.LAUNCHING) return "launching";
  if (status === SessionStatus.RUNNING) return "running";
  if (status === SessionStatus.WAITING) return "waiting";
  if (status === SessionStatus.COMPLETED) return "completed";
  if (status === SessionStatus.FAILED || status === SessionStatus.RESYNC_REQUIRED) {
    return "failed";
  }
  return "idle";
}
function sessionStatusLabel(status) {
  if (status === SessionStatus.IDLE) return "idle";
  if (status === SessionStatus.LAUNCHING) return "launching";
  if (status === SessionStatus.RUNNING) return "running";
  if (status === SessionStatus.WAITING) return "waiting";
  if (status === SessionStatus.COMPLETED) return "completed";
  if (status === SessionStatus.FAILED) return "failed";
  if (status === SessionStatus.RESYNC_REQUIRED) return "resync required";
  return "unspecified";
}
function projectMessageStatus(status) {
  if (status === TimelineItemStatus.COMPLETED) return "completed";
  if (status === TimelineItemStatus.FAILED || status === TimelineItemStatus.CANCELLED || status === TimelineItemStatus.UNSPECIFIED) {
    return "failed";
  }
  return "streaming";
}
function projectActivityStatus(status) {
  if (status === TimelineItemStatus.PENDING) return "pending";
  if (status === TimelineItemStatus.STREAMING || status === TimelineItemStatus.RUNNING) {
    return "running";
  }
  if (status === TimelineItemStatus.COMPLETED) return "completed";
  return "failed";
}
function projectPlanStepStatus(status) {
  if (status === PlanStepStatus.RUNNING) return "running";
  if (status === PlanStepStatus.COMPLETED || status === PlanStepStatus.SKIPPED) {
    return "completed";
  }
  if (status === PlanStepStatus.FAILED) return "failed";
  return "pending";
}
function projectToolStatus(status) {
  if (status === ToolPhase.QUEUED) return "pending";
  if (status === ToolPhase.RUNNING) return "running";
  if (status === ToolPhase.WAITING_APPROVAL) return "waiting";
  if (status === ToolPhase.COMPLETED) return "completed";
  if (status === ToolPhase.CANCELLED) return "cancelled";
  return "failed";
}
function projectResourceStatus(status) {
  if (status === SessionResourceStatus.CONNECTING) return "connecting";
  if (status === SessionResourceStatus.READY) return "connected";
  if (status === SessionResourceStatus.DEGRADED) return "reconnecting";
  return "disconnected";
}
function projectPermissionActivityStatus(request) {
  if (request?.state === PermissionRequestState.PENDING) return "pending";
  if (request?.state === PermissionRequestState.RESOLVED && request.resolution?.decision === PermissionDecision.ACCEPT) {
    return "completed";
  }
  return "failed";
}
function canComposePrompt(input) {
  return input.connection === "connected" && input.hasSendPrompt;
}
const SEND_PROMPT = "send_prompt";
const INTERRUPT = "interrupt";
const CHANGE_CONFIGURATION = "change_configuration";
const RESOLVE_PERMISSION = "resolve_permission";
function projectPermissions(requests) {
  const permissions = [];
  const evidence = [];
  requests.forEach((request) => {
    if (request.state !== PermissionRequestState.PENDING) return;
    if (request.request.case === "approval") {
      permissions.push({
        id: request.permissionRequestId,
        kind: "approval",
        title: request.request.value.title,
        description: request.request.value.description ?? ""
      });
      return;
    }
    if (request.request.case === "questionnaire") {
      permissions.push({
        id: request.permissionRequestId,
        kind: "question",
        title: request.request.value.title,
        questions: request.request.value.questions.map((question) => ({
          id: question.questionId,
          prompt: question.prompt,
          header: question.header ?? "",
          options: question.options.map((option) => ({
            label: option.label,
            description: optionDescription(option)
          })),
          multiple: question.multiple,
          allowCustom: question.allowCustom,
          secret: question.secret
        }))
      });
      return;
    }
    evidence.push({
      id: `permission:${request.permissionRequestId}`,
      kind: "system",
      title: "Unsupported permission request",
      detail: request.request.case === "unsupported" ? formatUnsupported(request.request.value) : "missing permission request payload",
      status: "failed"
    });
  });
  return { permissions, evidence };
}
function projectTerminals(resources) {
  return resources.flatMap((resource) => {
    if (resource.resource.case !== "terminal") return [];
    const controlMode = resource.resource.value.controlMode;
    return [
      {
        id: resource.resourceId,
        label: resource.label,
        status: projectResourceStatus(resource.status),
        writable: resource.resource.value.writable,
        controlMode: controlMode === TerminalControlMode.SURFACE ? "surface" : controlMode === TerminalControlMode.HOST ? "host" : void 0
      }
    ];
  });
}
function projectCapabilities(snapshot, connection, terminals, interactionMode) {
  const interactive = connection === "connected";
  const capabilities = snapshot.capabilities;
  const hasToolActions = snapshot.history.some(
    (item) => item.content?.content?.case === "toolExecution" && item.content.content.value.actions.length > 0
  );
  return {
    sendMessage: canComposePrompt({
      connection,
      hasSendPrompt: supportsCommand(
        capabilities,
        SEND_PROMPT,
        snapshot.grants,
        snapshot
      )
    }),
    interrupt: interactive && supportsCommand(capabilities, INTERRUPT, snapshot.grants, snapshot),
    resolvePermission: interactive && supportsCommand(
      capabilities,
      RESOLVE_PERMISSION,
      snapshot.grants,
      snapshot
    ),
    updateConfiguration: interactive && supportsCommand(
      capabilities,
      CHANGE_CONFIGURATION,
      snapshot.grants,
      snapshot
    ),
    terminal: interactive && terminals.length > 0 && authorizedActions(
      capabilities?.terminalOperations ?? [],
      snapshot.grants,
      snapshot
    ),
    executeToolAction: interactive && hasToolActions,
    extensions: (capabilities?.extensions ?? []).map((extension) => ({
      actions: [...extension.actions],
      namespace: extension.namespace,
      schemaVersion: extension.schemaVersion,
      semanticType: extension.semanticKey
    }))
  };
}
function supportsCommand(capabilities, semanticKey, grants, snapshot) {
  const descriptor = capabilities?.commandSchemas.find(
    (candidate) => candidate.semanticKey === semanticKey
  );
  return Boolean(
    descriptor && authorizedActions(descriptor.actions, grants, snapshot)
  );
}
function authorizedActions(actions, grants, snapshot) {
  if (actions.length === 0) return false;
  return grants.some(
    (grant) => grantApplies(grant, snapshot) && actions.some((action) => grant.actions.includes(action))
  );
}
function grantApplies(grant, snapshot) {
  return grant.sessionId === snapshot.sessionId && (grant.minimumRevision === void 0 || snapshot.revision >= grant.minimumRevision) && (grant.maximumRevision === void 0 || snapshot.revision <= grant.maximumRevision);
}
function optionDescription(option) {
  const value = decodeStructuredPayload(option.value)?.text;
  return [option.description ?? "", value ? `Value: ${value}` : void 0].filter(Boolean).join("\n");
}
function contentBlockText(block) {
  const content = block.content;
  if (content.case === "text") return content.value.text;
  if (content.case === "markdown") return content.value.markdown;
  if (content.case === "code") {
    const label = [content.value.language, content.value.filename].filter(Boolean).join(" ");
    return `\`\`\`${label}
${content.value.code}
\`\`\``;
  }
  if (content.case === "json") {
    return formatStructuredPayload(content.value.value) ?? "JSON payload missing";
  }
  if (content.case === "table") return formatTable(content.value);
  if (content.case === "diff") {
    const heading = [
      content.value.path,
      content.value.language,
      content.value.truncated ? "truncated" : void 0
    ].filter(Boolean).join(" ");
    return `\`\`\`diff${heading ? ` ${heading}` : ""}
${content.value.patch}
\`\`\``;
  }
  if (content.case === "command") {
    return [
      content.value.cwd ? `cwd: ${content.value.cwd}` : void 0,
      `$ ${content.value.command}`,
      content.value.exitCode === void 0 ? void 0 : `exitCode: ${content.value.exitCode}`
    ].filter(Boolean).join("\n");
  }
  if (content.case === "log") {
    const fields = formatStructuredPayload(content.value.fields);
    return [
      `[${content.value.level || "log"}] ${content.value.message}`,
      content.value.createdAt,
      fields
    ].filter(Boolean).join("\n");
  }
  if (content.case === "progress") {
    const progress = content.value;
    const count = progress.current !== void 0 || progress.total !== void 0 ? `${progress.current?.toString() ?? "?"}/${progress.total?.toString() ?? "?"}${progress.unit ? ` ${progress.unit}` : ""}` : void 0;
    const fraction = progress.fraction === void 0 ? void 0 : `${Math.round(progress.fraction * 100)}%`;
    return [progress.stage, progress.message, count, fraction].filter(Boolean).join(" - ");
  }
  if (content.case === "error") {
    const details = formatStructuredPayload(content.value.details);
    return [
      `[${content.value.code || "content_error"}] ${content.value.message}`,
      details
    ].filter(Boolean).join("\n");
  }
  if (content.case === "html" && content.value.payload.case === "source") {
    return [
      `HTML security profile: ${content.value.securityProfile}`,
      `\`\`\`html
${content.value.payload.value}
\`\`\``
    ].join("\n");
  }
  if (content.case === "link") {
    const link = content.value.label ? `[${content.value.label}](${content.value.url})` : content.value.url;
    return [
      link,
      content.value.mediaType ? `Media type: ${content.value.mediaType}` : void 0
    ].filter(Boolean).join("\n");
  }
  if (content.case === "citation") {
    return citationBlockText(content.value);
  }
  if (content.case === "image" || content.case === "video" || content.case === "audio" || content.case === "pdf" || content.case === "presentation" || content.case === "spreadsheet" || content.case === "file") {
    return content.value.altText;
  }
  return void 0;
}
function contentBlockData(block) {
  const content = block.content;
  if (content.case === "json") {
    return decodeStructuredPayload(content.value.value)?.value;
  }
  if (content.case === "table") {
    return {
      columns: content.value.columns,
      rows: content.value.rows.map(
        (row) => row.cells.map((cell) => decodeStructuredPayload(cell)?.value)
      )
    };
  }
  if (content.case === "progress") return content.value;
  return stringifyValue(content.value);
}
function citationBlockText(citation) {
  const label = citation.url ? `[${citation.label}](${citation.url})` : citation.label;
  return [
    "## 回答引用来源",
    `- [${citation.citationId}] ${label}`.trim(),
    citation.excerpt
  ].filter(Boolean).join("\n");
}
function formatTable(table) {
  const labels = table.columns.map((column) => {
    const label = column.label || column.key;
    const metadata = [
      column.key !== label ? column.key : void 0,
      column.valueType
    ].filter(Boolean).join(": ");
    return metadata ? `${label} (${metadata})` : label;
  });
  if (labels.length === 0) return stringifyValue(table.rows);
  const header = `| ${labels.join(" | ")} |`;
  const separator = `| ${labels.map(() => "---").join(" | ")} |`;
  const rows = table.rows.map((row) => {
    const cells = row.cells.map(
      (cell) => formatStructuredPayload(cell)?.replaceAll("|", "\\|") ?? ""
    );
    return `| ${cells.join(" | ")} |`;
  });
  return [header, separator, ...rows].join("\n");
}
function isTranscriptMetadata(block) {
  return block.identity?.namespace === "agentcloud.transcript" && block.identity.semanticKey === "message-metadata";
}
function isHiddenTranscriptMessage(blocks) {
  return blocks.some((block) => isTranscriptMetadata(block) && block.content.case === "json" && hasMetaFlag(block.content.value.value));
}
function isHiddenPreservedTranscriptMessage(value) {
  return value.identity?.namespace === "agentcloud.transcript" && value.identity.semanticKey === "message" && hasMetaFlag(value.payload);
}
function hasMetaFlag(payload) {
  if (!payload) return false;
  const mime = payload.mediaType.split(";")[0].trim().toLowerCase();
  if (mime !== "application/json" && !/^application\/[\w.+-]+\+json$/.test(mime)) return false;
  try {
    const value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(payload.data));
    return value !== null && typeof value === "object" && !Array.isArray(value) && "is_meta" in value && value.is_meta === true;
  } catch {
    return false;
  }
}
function projectMessageStructuredContent(blocks) {
  if (isHiddenTranscriptMessage(blocks)) return void 0;
  const visible = blocks.filter((block) => !isTranscriptMetadata(block));
  if (!visible.some((block) => block.content.case === "json")) return void 0;
  return visible.flatMap((block) => {
    if (block.content.case !== "json") {
      const text2 = contentBlockText(block);
      return text2 === void 0 ? [] : [{ kind: "text", text: text2 }];
    }
    const payload = block.content.value.value;
    const mediaType = payload?.mediaType ?? "";
    const content = {
      kind: "json",
      id: block.contentId,
      mediaType,
      raw: new TextDecoder().decode(payload?.data),
      valid: false,
      ...block.identity ? { identity: {
        namespace: block.identity.namespace,
        semanticKey: block.identity.semanticKey,
        schemaVersion: block.identity.schemaVersion
      } } : {}
    };
    const mime = mediaType.split(";")[0].trim().toLowerCase();
    if (payload && (mime === "application/json" || /^application\/[\w.+-]+\+json$/.test(mime))) {
      try {
        content.value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(payload.data));
        content.valid = true;
        content.form = parseAgentFormSurface(content);
      } catch {
      }
    }
    return [content];
  });
}
function blockArtifactReferences(block) {
  const content = block.content;
  if (content.case === "image" || content.case === "video" || content.case === "audio" || content.case === "pdf" || content.case === "presentation" || content.case === "spreadsheet" || content.case === "file") {
    return [mediaReference(content.value)];
  }
  if (content.case === "imageComparison") {
    return [content.value.source, content.value.result].filter((value) => value !== void 0).map(mediaReference);
  }
  if (content.case === "annotation" && content.value.source) {
    return [mediaReference(content.value.source)];
  }
  if (content.case === "html" && content.value.payload.case === "artifact") {
    return [mediaReference(content.value.payload.value)];
  }
  if (content.case === "artifactRef") return [content.value];
  return [];
}
function mediaReference(media) {
  return {
    artifactId: media.artifactId,
    filename: media.filename,
    mediaType: media.mediaType,
    representationId: media.representationId,
    revision: media.revision,
    role: media.role
  };
}
function unsupportedContentEvidence(block, id) {
  const detail = unsupportedContentDetail(block);
  return detail ? {
    id,
    kind: "system",
    title: "Unsupported content",
    detail,
    status: "failed"
  } : void 0;
}
function toolArtifactMetadata(block) {
  const content = block.content;
  if (content.case === "imageComparison") {
    return {
      kind: content.case,
      defaultMode: content.value.defaultMode,
      source: mediaMetadata(content.value.source),
      result: mediaMetadata(content.value.result)
    };
  }
  if (content.case === "annotation") {
    return {
      kind: content.case,
      source: mediaMetadata(content.value.source),
      annotations: decodeStructuredPayload(content.value.annotations)?.value
    };
  }
  if (content.case === "html" && content.value.payload.case === "artifact") {
    return {
      kind: content.case,
      securityProfile: content.value.securityProfile,
      artifact: mediaMetadata(content.value.payload.value)
    };
  }
  if (content.case === "image" || content.case === "video" || content.case === "audio" || content.case === "pdf" || content.case === "presentation" || content.case === "spreadsheet" || content.case === "file") {
    return mediaMetadata(content.value);
  }
  return void 0;
}
function unsupportedContentDetail(block) {
  const content = block.content;
  if (content.case === "unsupported") return formatUnsupported(content.value);
  if (content.case === "livePreview") {
    return [
      "kind=livePreview",
      `resourceId=${content.value.resourceId}`,
      `securityProfile=${content.value.securityProfile}`,
      content.value.sessionUrl ? `sessionUrl=${content.value.sessionUrl}` : void 0
    ].filter(Boolean).join("; ");
  }
  if (content.case === "restrictedIframe") {
    return [
      "kind=restrictedIframe",
      `rendererId=${content.value.rendererId}`,
      `protocolVersion=${content.value.protocolVersion}`,
      `url=${content.value.url}`,
      `origin=${content.value.origin}`,
      `sandboxTokens=${content.value.sandboxTokens.join(",")}`,
      `permissions=${content.value.permissions.join(",")}`,
      `maxInboundBytes=${content.value.maxInboundBytes.toString()}`,
      `maxOutboundBytes=${content.value.maxOutboundBytes.toString()}`
    ].join("; ");
  }
  if (content.case === "imageComparison") {
    return `kind=imageComparison; defaultMode=${content.value.defaultMode ?? "unspecified"}`;
  }
  if (content.case === "annotation") {
    return `kind=annotation; annotations=${decodeStructuredPayload(content.value.annotations)?.text ?? "missing"}`;
  }
  if (content.case === "html" && content.value.payload.case === "artifact") {
    return `kind=html; securityProfile=${content.value.securityProfile}`;
  }
  if (content.case === void 0) return "missing content payload";
  return void 0;
}
function mediaMetadata(media) {
  if (!media) return void 0;
  return {
    artifactId: media.artifactId,
    representationId: media.representationId,
    revision: media.revision.toString(),
    mediaType: media.mediaType,
    role: media.role,
    filename: media.filename,
    altText: media.altText
  };
}
function projectTimelineContent(blocks, parentId, catalog) {
  const projection = {
    artifacts: [],
    attachments: [],
    evidence: [],
    text: []
  };
  blocks.forEach((block, index) => {
    if (isTranscriptMetadata(block)) return;
    const id = `${parentId}:${block.contentId || index}`;
    const text2 = contentBlockText(block);
    if (text2 !== void 0) projection.text.push(text2);
    projectBlockArtifacts(block, id, catalog, projection);
    const evidence = unsupportedContentEvidence(block, id);
    if (evidence) projection.evidence.push(evidence);
  });
  return projection;
}
function projectToolResultBlocks(blocks, resultId, catalog) {
  return blocks.flatMap(
    (block, index) => {
      const id = `${resultId}:${block.contentId || index}`;
      const artifactReferences = blockArtifactReferences(block);
      if (artifactReferences.length > 0) {
        const results = artifactReferences.map(
          (reference, referenceIndex) => {
            const projected = projectArtifactReference(
              reference,
              referenceIndex === 0 ? id : `${id}:${referenceIndex}`,
              catalog,
              { schemaVersion: block.identity?.schemaVersion }
            );
            if (projected.kind !== "artifact") {
              return {
                id: projected.id,
                kind: "data",
                value: projected.detail
              };
            }
            return {
              id: projected.id,
              kind: "artifact",
              artifactId: projected.artifactId,
              mediaType: projected.mimeType,
              representationId: projected.selectedRepresentationId,
              revision: projected.revision,
              role: projected.role,
              schemaVersion: projected.schemaVersion
            };
          }
        );
        const metadata = toolArtifactMetadata(block);
        if (metadata !== void 0) {
          results.push({ id: `${id}:metadata`, kind: "data", value: metadata });
        }
        return results;
      }
      if (block.content.case === "unsupported") {
        return [
          {
            id,
            kind: "data",
            value: formatUnsupported(block.content.value)
          }
        ];
      }
      if (block.content.case === "json") {
        const content = projectMessageStructuredContent([block])?.[0];
        return [{
          id,
          kind: "data",
          value: contentBlockData(block),
          ...content?.kind === "json" ? { content } : {}
        }];
      }
      if (block.content.case === "table" || block.content.case === "progress" || block.content.case === "livePreview" || block.content.case === "restrictedIframe" || block.content.case === "imageComparison" || block.content.case === "annotation") {
        return [{ id, kind: "data", value: contentBlockData(block) }];
      }
      const text2 = contentBlockText(block);
      return text2 === void 0 ? [{ id, kind: "data", value: "missing content payload" }] : [{ id, kind: "text", text: text2 }];
    }
  );
}
function projectBlockArtifacts(block, id, catalog, projection) {
  blockArtifactReferences(block).forEach((reference, index) => {
    if (reference.role === "input" && !catalog.has(reference.artifactId)) {
      projection.attachments.push({
        attachmentId: reference.artifactId,
        filename: reference.filename || reference.artifactId,
        id: index === 0 ? id : `${id}:${index}`,
        kind: "attachment",
        mimeType: reference.mediaType || null,
        ...reference.representationId ? { representationId: reference.representationId } : {}
      });
      return;
    }
    const projected = projectArtifactReference(
      reference,
      index === 0 ? id : `${id}:${index}`,
      catalog,
      { schemaVersion: block.identity?.schemaVersion }
    );
    if (projected.kind === "artifact") projection.artifacts.push(projected);
    else projection.evidence.push(projected);
  });
}
function conversationFacingSystemBlocks(blocks) {
  return blocks.filter((block) => {
    if (block.content.case !== "log") return true;
    const level = (block.content.value.level || "").toLowerCase();
    return level === "warn" || level === "warning" || level === "error";
  });
}
function projectPlanStep(step) {
  return {
    id: step.stepId,
    title: step.title,
    status: projectPlanStepStatus(step.status)
  };
}
function planDetail(steps) {
  return steps.map(
    (step) => `${step.title} [${projectPlanStepStatus(step.status)}]${step.detail ? `
${step.detail}` : ""}`
  ).join("\n");
}
function messageRole(role) {
  if (role === MessageRole.USER) return "user";
  if (role === MessageRole.ASSISTANT) return "assistant";
  return "system";
}
function statusActivity(status) {
  const projected = projectSessionStatus(status);
  if (projected === "launching" || projected === "running" || projected === "waiting") {
    return "running";
  }
  if (projected === "failed") return "failed";
  return "completed";
}
function permissionTitle(request) {
  if (request?.request.case === "approval") return request.request.value.title;
  if (request?.request.case === "questionnaire") return request.request.value.title;
  return "";
}
function unsupportedTimeline(id, detail) {
  return {
    id,
    kind: "system",
    title: "Unsupported timeline item",
    detail,
    status: "failed"
  };
}
function projectToolExecution(tool, itemId, catalog, execution) {
  const identity = tool.identity;
  if (!identity?.namespace || !identity.semanticKey || !identity.schemaVersion) {
    return [
      {
        id: itemId,
        kind: "system",
        title: "Unsupported tool execution",
        detail: `executionId=${tool.executionId || "missing"}; identity=missing`,
        status: "failed",
        ...execution ? { execution } : {}
      }
    ];
  }
  const input = decodeStructuredPayload(tool.input);
  const results = projectToolResults(tool, itemId, catalog);
  const textResults = results.filter(
    (result) => result.kind === "text"
  ).map((result) => result.text);
  return [
    {
      id: itemId,
      kind: "tool",
      identity: {
        namespace: identity.namespace,
        semanticKey: identity.semanticKey,
        schemaVersion: identity.schemaVersion
      },
      title: tool.title || identity.sourceToolName || identity.semanticKey,
      detail: toolDetail(tool),
      actions: tool.actions.map((action) => ({
        actionId: action.actionId,
        actionType: action.actionType,
        confirmationRequired: action.confirmationRequired,
        ...action.inputSchema ? { inputSchema: decodeStructuredPayload(action.inputSchema)?.value } : {},
        ...action.expiresAt ? { expiresAt: action.expiresAt } : {},
        ...action.label ? { label: action.label } : {},
        ...action.requiredGrantAction ? { requiredGrantAction: action.requiredGrantAction } : {},
        schemaVersion: action.schemaVersion
      })),
      ...tool.approval ? {
        approval: {
          permissionRequestId: tool.approval.permissionRequestId,
          ...tool.approval.approvalId ? { approvalId: tool.approval.approvalId } : {}
        }
      } : {},
      input: input?.text,
      inputValue: input?.value,
      output: textResults.length > 0 ? textResults.join("\n\n") : void 0,
      ...tool.progress ? {
        progress: {
          ...tool.progress.current !== void 0 ? { current: Number(tool.progress.current) } : {},
          ...tool.progress.fraction !== void 0 ? { fraction: tool.progress.fraction } : {},
          ...tool.progress.message ? { message: tool.progress.message } : {},
          ...tool.progress.stage ? { stage: tool.progress.stage } : {},
          ...tool.progress.total !== void 0 ? { total: Number(tool.progress.total) } : {},
          ...tool.progress.unit ? { unit: tool.progress.unit } : {}
        }
      } : {},
      ...tool.failure ? {
        failure: {
          ...tool.failure.code ? { code: tool.failure.code } : {},
          message: tool.failure.message,
          ...tool.failure.retryable ? { retryable: true } : {}
        }
      } : {},
      ...tool.timing ? {
        timing: {
          ...tool.timing.durationMillis !== void 0 ? { durationMillis: Number(tool.timing.durationMillis) } : {},
          ...tool.timing.finishedAt ? { finishedAt: tool.timing.finishedAt } : {},
          ...tool.timing.queuedAt ? { queuedAt: tool.timing.queuedAt } : {},
          ...tool.timing.startedAt ? { startedAt: tool.timing.startedAt } : {}
        }
      } : {},
      results,
      status: projectToolStatus(tool.phase),
      ...execution ? {
        execution: {
          ...execution,
          ...tool.retry ? { attempt: tool.retry.attempt } : {}
        }
      } : {},
      ...tool.executionId ? { executionId: tool.executionId } : {},
      ...tool.retry ? {
        retry: {
          attempt: tool.retry.attempt,
          ...tool.retry.maximumAttempts !== void 0 ? { maximumAttempts: tool.retry.maximumAttempts } : {},
          ...tool.retry.retryOfExecutionId ? { retryOfExecutionId: tool.retry.retryOfExecutionId } : {}
        }
      } : {}
    }
  ];
}
function projectToolResults(tool, itemId, catalog) {
  const results = tool.results.flatMap((result, resultIndex) => {
    const resultId = result.resultId || `${itemId}:result:${resultIndex}`;
    return [
      ...projectToolResultBlocks(result.blocks, resultId, catalog),
      ...result.artifacts.map(
        (artifact, artifactIndex) => artifactResult(artifact, `${resultId}:artifact:${artifactIndex}`, catalog)
      )
    ];
  });
  tool.artifacts.forEach((artifact, index) => {
    results.push(artifactResult(artifact, `${itemId}:artifact:${index}`, catalog));
  });
  return results;
}
function artifactResult(reference, id, catalog) {
  const projected = projectArtifactReference(reference, id, catalog);
  if (projected.kind !== "artifact") {
    return { id, kind: "data", value: projected.detail };
  }
  return {
    id,
    kind: "artifact",
    artifactId: projected.artifactId,
    mediaType: projected.mimeType,
    representationId: projected.selectedRepresentationId,
    revision: projected.revision,
    role: projected.role,
    schemaVersion: projected.schemaVersion
  };
}
function toolDetail(tool) {
  const failure = tool.failure ? [
    `[${tool.failure.code || "tool_failed"}] ${tool.failure.message}`,
    formatStructuredPayload(tool.failure.details)
  ].filter(Boolean).join("\n") : void 0;
  return [tool.detail, progressDetail(tool.progress), failure].filter(Boolean).join("\n");
}
function progressDetail(progress) {
  if (!progress) return void 0;
  const count = progress.current !== void 0 || progress.total !== void 0 ? `${progress.current?.toString() ?? "?"}/${progress.total?.toString() ?? "?"}${progress.unit ? ` ${progress.unit}` : ""}` : void 0;
  const fraction = progress.fraction === void 0 ? void 0 : `${Math.round(progress.fraction * 100)}%`;
  return [progress.stage, progress.message, count, fraction].filter(Boolean).join(" - ");
}
function projectUnsupportedTimeline(id, value) {
  const recovered = recoverUnknownTool(id, value);
  if (recovered) return [recovered];
  return [unsupportedTimeline(id, formatUnsupported(value))];
}
function recoverUnknownTool(id, value) {
  const semanticKey = value.identity?.semanticKey ?? "";
  if (semanticKey !== "tool.unknown" && semanticKey !== "tool.phase") {
    return null;
  }
  const decoded = decodeStructuredPayload(value.payload);
  if (!decoded) return null;
  const record2 = asRecord(decoded.value) ?? parseJsonRecord(decoded.text);
  if (!record2) return null;
  const toolName = typeof record2.toolName === "string" && record2.toolName.trim() ? record2.toolName.trim() : "Tool";
  const identity = resolveRecoveredToolIdentity(
    value.identity?.namespace,
    toolName,
    value.identity?.schemaVersion
  );
  const status = projectRecoveredToolStatus(record2);
  const detailParts = [
    typeof record2.resultText === "string" ? record2.resultText : "",
    typeof record2.errorMessage === "string" ? record2.errorMessage : ""
  ].filter(Boolean);
  const args = typeof record2.argumentsJson === "string" ? record2.argumentsJson.trim() : "";
  return {
    id,
    kind: "tool",
    identity,
    title: toolName,
    detail: detailParts.join("\n") || void 0,
    actions: [],
    input: args && args !== "{}" ? args : void 0,
    inputValue: args ? parseJSONValue(args) : void 0,
    results: [],
    status
  };
}
function resolveRecoveredToolIdentity(namespace, toolName, schemaVersion) {
  const protocol = namespace?.replace(/^agentcloud\./, "") || "acp";
  const baseName = toolName.split(/\s+/, 1)[0] || toolName;
  const resolved = resolveSourceTool(protocol, baseName) || resolveSourceTool("acp", baseName) || resolveSourceTool("claude", baseName) || resolveSourceTool("codex", baseName);
  if (resolved) {
    return {
      namespace: resolved.namespace || namespace || "agentcloud.acp",
      semanticKey: resolved.semanticKey,
      schemaVersion: resolved.schemaVersion || schemaVersion || "1"
    };
  }
  return {
    namespace: namespace || "agentcloud.acp",
    semanticKey: "tool.custom",
    schemaVersion: schemaVersion || "1"
  };
}
function asRecord(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value;
}
function parseJsonRecord(text2) {
  try {
    return asRecord(JSON.parse(text2));
  } catch {
    return null;
  }
}
function parseJSONValue(text2) {
  try {
    return JSON.parse(text2);
  } catch {
    return text2;
  }
}
function projectRecoveredToolStatus(record2) {
  if (record2.success === false) return "failed";
  if (record2.success === true) return "completed";
  const status = typeof record2.status === "string" ? record2.status : "";
  if (status === "failed" || status === "cancelled") return "failed";
  if (status === "completed") return "completed";
  if (status === "in_progress" || status === "running" || status === "pending") {
    return "running";
  }
  return "completed";
}
function executionReference(envelope, stepId) {
  if (!envelope) return stepId ? { stepId } : void 0;
  return {
    ...envelope.causationCommandId ? { causationCommandId: envelope.causationCommandId } : {},
    ...envelope.parentId ? { parentId: envelope.parentId } : {},
    ...envelope.sequence !== void 0 ? { sequence: envelope.sequence } : {},
    ...envelope.turnId ? { turnId: envelope.turnId } : {},
    ...envelope.itemId ? { sourceEventId: envelope.itemId } : {},
    ...stepId ? { stepId } : {}
  };
}
function decorateTimelineItems(items, history) {
  const sources = history.flatMap((timelineItem) => {
    const itemId = timelineItem.envelope?.itemId;
    if (!itemId) return [];
    return [{ itemId, execution: executionReference(timelineItem.envelope, itemId) }];
  });
  return items.map((item) => {
    const source = [...sources].reverse().find(
      ({ itemId }) => item.id === itemId || item.id.startsWith(`${itemId}:`)
    );
    return source?.execution ? { ...item, execution: { ...source.execution, ...item.execution } } : item;
  });
}
function projectTimeline(history, catalog, permissions) {
  const projection = { items: [], plan: [], hiddenArtifactIds: /* @__PURE__ */ new Set() };
  history.forEach((timelineItem, index) => {
    const id = timelineItem.envelope?.itemId || `timeline:${index}`;
    const content = timelineItem.content?.content;
    if (!content?.case) {
      projection.items.push(unsupportedTimeline(id, "missing timeline content"));
      return;
    }
    if (content.case === "message") {
      if (isHiddenTranscriptMessage(content.value.content)) {
        for (const block of content.value.content) {
          for (const reference of blockArtifactReferences(block)) {
            projection.hiddenArtifactIds.add(reference.artifactId);
          }
        }
        return;
      }
      if (content.value.role === MessageRole.USER && timelineItem.envelope?.causationCommandId) {
        projection.latestUserCommandId = timelineItem.envelope.causationCommandId;
      }
      const blocks = projectTimelineContent(content.value.content, id, catalog);
      if (blocks.text.length > 0) {
        const structured = projectMessageStructuredContent(content.value.content);
        projection.items.push({
          ...structured ? { content: structured } : {},
          id,
          kind: "message",
          role: messageRole(content.value.role),
          text: blocks.text.join("\n\n"),
          status: projectMessageStatus(content.value.status)
        });
      }
      projection.items.push(...blocks.attachments, ...blocks.artifacts, ...blocks.evidence);
      return;
    }
    if (content.case === "reasoning") {
      const blocks = projectTimelineContent(content.value.content, id, catalog);
      projection.items.push({
        id,
        kind: "reasoning",
        title: "Reasoning",
        detail: blocks.text.join("\n\n") || void 0,
        status: projectActivityStatus(content.value.status)
      });
      projection.items.push(...blocks.attachments, ...blocks.artifacts, ...blocks.evidence);
      return;
    }
    if (content.case === "toolExecution") {
      projection.items.push(...projectToolExecution(
        content.value,
        id,
        catalog,
        executionReference(timelineItem.envelope, content.value.executionId || id)
      ));
      return;
    }
    if (content.case === "plan") {
      projection.plan = content.value.steps.map((step) => ({
        ...projectPlanStep(step),
        execution: executionReference(timelineItem.envelope, id)
      }));
      projection.items.push({
        id,
        kind: "system",
        title: "Plan updated",
        detail: planDetail(content.value.steps),
        status: "completed"
      });
      return;
    }
    if (content.case === "artifactReference") {
      projection.items.push(
        projectArtifactReference(content.value.artifact, id, catalog)
      );
      return;
    }
    if (content.case === "approval") {
      const request = permissions.get(content.value.permissionRequestId);
      projection.items.push({
        id,
        kind: "system",
        title: "Permission requested",
        detail: [
          `permissionRequestId=${content.value.permissionRequestId}`,
          permissionTitle(request)
        ].filter(Boolean).join("\n"),
        status: projectPermissionActivityStatus(request)
      });
      return;
    }
    if (content.case === "status") {
      projection.items.push({
        id,
        kind: "system",
        title: `Session ${sessionStatusLabel(content.value.status)}`,
        detail: content.value.detail,
        status: statusActivity(content.value.status)
      });
      return;
    }
    if (content.case === "error") {
      const error = content.value.error;
      projection.items.push({
        id,
        kind: "error",
        title: error?.code || "agent_error",
        detail: error ? formatAgentErrorDetail(error) : "missing error payload",
        status: "failed"
      });
      return;
    }
    if (content.case === "system") {
      const facing = conversationFacingSystemBlocks(content.value.content);
      if (facing.length === 0 && content.value.content.length > 0) {
        return;
      }
      const blocks = projectTimelineContent(
        facing.length > 0 ? facing : content.value.content,
        id,
        catalog
      );
      projection.items.push({
        id,
        kind: "system",
        title: "System",
        detail: blocks.text.join("\n\n") || void 0,
        status: "completed"
      });
      projection.items.push(...blocks.attachments, ...blocks.artifacts, ...blocks.evidence);
      return;
    }
    if (content.case === "unsupported") {
      if (isHiddenPreservedTranscriptMessage(content.value)) return;
      projection.items.push(...projectUnsupportedTimeline(id, content.value));
    }
  });
  return {
    ...projection,
    items: decorateTimelineItems(projection.items, history)
  };
}
function projectSessionInteractions(receipts) {
  return receipts.flatMap((receipt) => {
    const kind = receipt.interactionKind;
    if (!isInteractionKind(kind)) return [];
    return [{
      commandId: receipt.commandId,
      ...receipt.error?.message ? { error: receipt.error.message } : {},
      id: `interaction:${receipt.commandId}`,
      kind,
      ...receipt.interactionSourceId ? { sourceId: receipt.interactionSourceId } : {},
      status: receiptStatus(receipt.state),
      updatedAt: receipt.updatedAt ?? receipt.receivedAt ?? ""
    }];
  });
}
function isInteractionKind(value) {
  return value === "artifact_action" || value === "extension" || value === "permission" || value === "tool_action";
}
function receiptStatus(state) {
  switch (state) {
    case CommandReceiptState.SUCCEEDED:
      return "succeeded";
    case CommandReceiptState.FAILED:
      return "failed";
    case CommandReceiptState.REJECTED:
      return "rejected";
    case CommandReceiptState.CANCELLED:
      return "cancelled";
    case CommandReceiptState.RUNNING:
      return "running";
    default:
      return "pending";
  }
}
function projectExecutionView(items) {
  const grouped = /* @__PURE__ */ new Map();
  const turnIds = /* @__PURE__ */ new Map();
  for (const item of items) {
    if (!item.execution) continue;
    const runId = item.execution.turnId ?? "run:unscoped";
    const steps = grouped.get(runId) ?? [];
    steps.push({
      id: item.id,
      title: itemTitle(item),
      kind: itemKind(item),
      status: stepStatus(item),
      ...executionStepDetails(item),
      ...item.execution.parentId ? { parentId: item.execution.parentId } : {},
      execution: item.execution
    });
    grouped.set(runId, steps);
    turnIds.set(runId, item.execution.turnId);
  }
  const runs = createRuns(grouped, turnIds);
  const activeRun = runs.find((run) => run.status === "running" || run.status === "waiting");
  return {
    runs,
    ...activeRun ? { activeRunId: activeRun.id } : {},
    runningCount: runs.filter((run) => run.status === "running" || run.status === "waiting").length,
    failedCount: runs.filter((run) => run.status === "failed").length
  };
}
function createRuns(grouped, turnIds) {
  const runs = [...grouped.entries()].map(
    ([id, steps]) => createRun(id, steps, turnIds.get(id))
  );
  const byParent = /* @__PURE__ */ new Map();
  for (const run of runs) {
    for (const step of run.steps) {
      const parentRunId = step.parentId ? runIdForParent(runs, step.parentId) : void 0;
      if (parentRunId && parentRunId !== run.id) {
        const children = byParent.get(parentRunId) ?? [];
        if (!children.includes(run.id)) children.push(run.id);
        byParent.set(parentRunId, children);
      }
    }
  }
  return runs.map((run) => ({ ...run, childRunIds: byParent.get(run.id) ?? [] }));
}
function runIdForParent(runs, parentId) {
  return runs.find(
    (run) => run.steps.some(
      (step) => step.id === parentId || step.execution?.stepId === parentId || step.executionId === parentId
    )
  )?.id;
}
function createRun(id, steps, turnId) {
  const status = aggregateStatus(steps.map((step) => step.status));
  return {
    id,
    title: turnId ? `Turn ${turnId}` : "Execution",
    status,
    ...turnId ? { turnId } : {},
    steps,
    childRunIds: []
  };
}
function aggregateStatus(statuses) {
  if (statuses.some((status) => status === "failed")) return "failed";
  if (statuses.some((status) => status === "running")) return "running";
  if (statuses.some((status) => status === "waiting")) return "waiting";
  if (statuses.some((status) => status === "queued")) return "queued";
  if (statuses.some((status) => status === "cancelled")) return "cancelled";
  return "completed";
}
function itemTitle(item) {
  if (item.kind === "tool") return item.title;
  if (item.kind === "message") return item.role === "user" ? "User message" : "Agent response";
  if (item.kind === "attachment" || item.kind === "artifact") return item.filename;
  return item.title;
}
function itemKind(item) {
  if (item.kind === "tool") return "tool";
  if (item.kind === "message") return "message";
  if (item.kind === "reasoning") return "reasoning";
  if (item.kind === "artifact") return "artifact";
  return "activity";
}
function stepStatus(item) {
  if (!("status" in item)) return "completed";
  if (item.status === "cancelled") return "cancelled";
  if (item.status === "waiting") return "waiting";
  if (item.status === "failed") return "failed";
  if (item.status === "running") return "running";
  if (item.status === "pending") return "queued";
  return "completed";
}
function executionStepDetails(item) {
  if (item.kind !== "tool") {
    return "detail" in item && item.detail ? { detail: item.detail } : {};
  }
  return {
    ...item.detail ? { detail: item.detail } : {},
    ...item.executionId ? { executionId: item.executionId } : {},
    ...item.failure ? { failure: item.failure } : {},
    ...item.progress ? { progress: item.progress } : {},
    ...item.retry ? { retry: item.retry } : {},
    ...item.timing ? { timing: item.timing } : {}
  };
}
function projectGeneratedSessionSnapshot(snapshot, options) {
  const catalog = createArtifactCatalog(snapshot.artifacts);
  const permissionMap = new Map(
    snapshot.permissionRequests.map((request) => [
      request.permissionRequestId,
      request
    ])
  );
  const timeline = projectTimeline(snapshot.history, catalog, permissionMap);
  const permissionProjection = projectPermissions(snapshot.permissionRequests);
  const representedArtifacts = new Set(
    timeline.items.flatMap(
      (item) => item.kind === "artifact" ? [item.artifactId] : []
    )
  );
  const hiddenArtifactIds = /* @__PURE__ */ new Set([
    ...snapshot.hiddenArtifactIds ?? [],
    ...timeline.hiddenArtifactIds
  ]);
  const catalogItems = snapshot.artifacts.filter((artifact) => !representedArtifacts.has(artifact.artifactId) && !hiddenArtifactIds.has(artifact.artifactId)).map(
    (artifact) => projectArtifactDescriptor(
      artifact,
      `artifact:${artifact.artifactId}`
    )
  );
  const terminals = projectTerminals(snapshot.resources);
  const configuration = projectConfiguration(snapshot);
  const error = formatAgentError(snapshot.error) ?? (snapshot.status === SessionStatus.RESYNC_REQUIRED ? "[resync_required] Session requires resynchronization." : null);
  return {
    sessionId: snapshot.sessionId,
    title: options.title,
    agentLabel: options.agentLabel,
    status: projectSessionStatus(snapshot.status),
    connection: options.connection,
    interactionMode: options.interactionMode,
    ...timeline.latestUserCommandId ? { latestUserCommandId: timeline.latestUserCommandId } : {},
    capabilities: projectCapabilities(
      snapshot,
      options.connection,
      terminals,
      options.interactionMode
    ),
    interactions: projectSessionInteractions(snapshot.commandReceipts),
    execution: projectExecutionView(timeline.items),
    ...configuration ? { configuration } : {},
    items: [
      ...timeline.items,
      ...catalogItems,
      ...permissionProjection.evidence
    ],
    plan: timeline.plan,
    permissions: permissionProjection.permissions,
    terminals,
    hasOlderItems: options.hasOlderItems,
    error
  };
}
function projectConnectedSession(connection, context, state = connection.getStore().getState()) {
  const connectionStatus = projectConnectionStatus(connection.getStatus());
  const transportError = connection.getError()?.message ?? null;
  if (!state) {
    return emptySession(context, connectionStatus, transportError);
  }
  const projected = projectGeneratedSessionSnapshot(state.snapshot, {
    title: context.title,
    agentLabel: context.agentLabel,
    connection: connectionStatus,
    interactionMode: context.interactionMode,
    hasOlderItems: state.snapshot.hasOlderHistory
  });
  return {
    ...projected,
    historyWindowSize: state.snapshot.history.length,
    ...transportError && !projected.error ? { error: transportError } : {}
  };
}
function emptySession(context, connection, error) {
  return {
    sessionId: context.sessionId,
    title: context.title,
    agentLabel: context.agentLabel,
    status: "launching",
    connection,
    interactionMode: context.interactionMode,
    capabilities: {
      sendMessage: canComposePrompt({
        connection,
        hasSendPrompt: false,
        interactionMode: context.interactionMode
      }),
      interrupt: false,
      resolvePermission: false,
      updateConfiguration: false,
      terminal: false,
      executeToolAction: false,
      extensions: []
    },
    items: [],
    interactions: [],
    plan: [],
    permissions: [],
    terminals: [],
    hasOlderItems: false,
    error
  };
}
function projectConnectionStatus(status) {
  if (status === "connected") return "connected";
  if (status === "reconnecting") return "reconnecting";
  if (status === "failed" || status === "disconnected") return "disconnected";
  return "connecting";
}
class AgentSessionRuntimeV2 {
  constructor(options) {
    this.options = options;
    this.connection = options.connection;
    this.context = {
      agentLabel: options.partner?.name.trim() || options.agentLabel,
      interactionMode: options.interactionMode,
      sessionId: options.sessionId,
      title: options.partner?.name.trim() || options.title
    };
    this.now = options.now ?? (() => (/* @__PURE__ */ new Date()).toISOString());
    this.interactionExecutor = new AgentInteractionExecutor(
      this.interactions,
      () => this.now(),
      () => this.refresh()
    );
    const forms = sessionFormBindings(this.connection, (id) => this.requireSession(id), (id, commandId, command) => this.execute(id, commandId, command));
    const executeExtension = forms.executeExtension;
    this.executeExtension = (sessionId, commandId, command) => this.interactionExecutor.execute(
      "extension",
      commandId,
      void 0,
      () => executeExtension(sessionId, commandId, command)
    ).then((result) => result);
    this.executeToolAction = (sessionId, action) => this.interactionExecutor.execute(
      "tool_action",
      action.commandId,
      action.toolExecutionId,
      () => this.execute(sessionId, action.commandId, toolActionPayload(action))
    ).then(() => void 0);
    this.getFormState = forms.getFormState;
    const resources = bindSessionResources(options, (id) => this.requireSession(id));
    this.loadArtifact = resources.loadArtifact;
    this.loadAttachment = resources.loadAttachment;
    this.loadPreviewUrl = resources.loadPreviewUrl;
    this.uploadAttachment = resources.uploadAttachment;
    this.notifier = new SessionFrameNotifier(() => this.refresh(), options.scheduleNotification);
    const commands2 = createAgentSessionRuntimeCommands({
      connection: this.connection,
      interactionExecutor: this.interactionExecutor,
      notifier: this.notifier,
      pending: this.pending,
      getGeneration: () => this.generation,
      isCurrent: (generation) => !this.closed && generation === this.generation,
      requireSession: (id) => this.requireSession(id),
      execute: (id, commandId, payload) => this.execute(id, commandId, payload)
    });
    this.sendMessage = commands2.sendMessage;
    this.sendSlashCommand = commands2.sendSlashCommand;
    this.interrupt = commands2.interrupt;
    this.resolvePermission = commands2.resolvePermission;
    this.updateConfiguration = commands2.updateConfiguration;
    this.executeArtifactAction = commands2.executeArtifactAction;
    const queueRuntime = createAgentSessionQueueBinding({
      connection: this.connection,
      notifier: this.notifier,
      pending: this.pending,
      queue: this.queue,
      sessionId: this.context.sessionId,
      execute: (id, nextCommandId, payload) => this.execute(id, nextCommandId, payload),
      getGeneration: () => this.generation,
      isCurrent: (generation) => !this.closed && generation === this.generation,
      isClosed: () => this.closed,
      requireSession: (id) => this.requireSession(id),
      projectSnapshot: () => this.project(),
      refresh: () => this.refresh()
    });
    this.queueController = queueRuntime.controller;
    this.enqueueMessage = queueRuntime.enqueueMessage;
    this.enqueueSlashCommand = queueRuntime.enqueueSlashCommand;
    this.cancelQueuedMessage = queueRuntime.cancelQueuedMessage;
    this.updateQueuedMessage = queueRuntime.updateQueuedMessage;
    this.retryQueuedMessage = queueRuntime.retryQueuedMessage;
    this.removeQueuedMessage = queueRuntime.removeQueuedMessage;
    this.snapshot = this.project();
    this.connection.subscribe(() => {
      this.scheduleRefresh();
      this.queueController.trigger();
    });
    this.connection.getStore().subscribe(() => {
      this.scheduleRefresh();
      this.queueController.trigger();
    });
  }
  options;
  executeExtension;
  executeToolAction;
  getFormState;
  loadArtifact;
  loadAttachment;
  loadPreviewUrl;
  uploadAttachment;
  enqueueMessage;
  enqueueSlashCommand;
  cancelQueuedMessage;
  updateQueuedMessage;
  retryQueuedMessage;
  removeQueuedMessage;
  sendMessage;
  sendSlashCommand;
  interrupt;
  resolvePermission;
  updateConfiguration;
  executeArtifactAction;
  connection;
  context;
  listeners = /* @__PURE__ */ new Set();
  now;
  snapshot;
  pending = new PendingPromptProjection();
  queue = new AgentTaskQueue();
  queueController;
  interactions = new AgentInteractionStore();
  interactionExecutor;
  notifier;
  closed = false;
  generation = 0;
  async open(sessionId) {
    this.requireSession(sessionId);
    this.closed = false;
    const generation = this.generation;
    await this.connection.open();
    if (!this.closed && generation === this.generation) this.notifier.flush();
  }
  close(sessionId) {
    this.requireSession(sessionId);
    this.closed = true;
    this.generation++;
    this.notifier.cancel();
    this.pending.clear();
    this.interactions.clear();
    this.queueController.clear();
    this.connection.close();
    this.refresh();
  }
  getSnapshot(sessionId) {
    this.requireSession(sessionId);
    return this.snapshot;
  }
  subscribe(sessionId, listener) {
    this.requireSession(sessionId);
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  async loadOlder(sessionId) {
    this.requireSession(sessionId);
    const generation = this.generation;
    await this.connection.loadOlder();
    if (!this.closed && generation === this.generation) this.notifier.flush();
  }
  async execute(sessionId, commandId, command) {
    this.requireSession(sessionId);
    if (this.closed) throw new Error("agent_workbench_session_not_ready");
    const generation = this.generation;
    return executeSessionCommand(
      this.connection,
      sessionId,
      commandId,
      command,
      this.now(),
      () => !this.closed && generation === this.generation
    );
  }
  scheduleRefresh() {
    if (!this.closed) this.notifier.request();
  }
  refresh() {
    this.snapshot = this.project();
    for (const listener of this.listeners) listener();
  }
  project() {
    const state = this.connection.getStore().getState();
    const snapshot = withPartnerIdentity(
      this.pending.project(projectConnectedSession(this.connection, this.context, state), state?.snapshot),
      this.options.partner
    );
    const projected = this.options.projectSnapshot?.(snapshot) ?? snapshot;
    return {
      ...projected,
      interactions: this.interactions.merge(projected.interactions),
      queue: this.queue.project()
    };
  }
  requireSession(sessionId) {
    if (sessionId !== this.context.sessionId) throw new Error("agent_workbench_runtime_session_mismatch");
  }
}
function createAgentWorkbenchRuntime({
  transport,
  live = true,
  connectionOptions,
  createArtifactLoader,
  ...options
}) {
  const connection = new AgentSessionConnection(
    live ? transport : {
      execute: (command, signal) => transport.execute(command, signal),
      getSnapshot: (signal, size) => transport.getSnapshot(signal, size),
      loadOlder: transport.loadOlder ? (signal, size) => transport.loadOlder(signal, size) : void 0,
      streamDeltas: (_cursor, signal) => idleDeltaStream(signal)
    },
    connectionOptions
  );
  return new AgentSessionRuntimeV2({
    ...options,
    connection,
    loadArtifact: createArtifactLoader?.(connection)
  });
}
async function* idleDeltaStream(signal) {
  await new Promise((resolve) => {
    if (signal.aborted) resolve();
    else signal.addEventListener("abort", () => resolve(), { once: true });
  });
}
function createAgentArtifactLoader(resources) {
  return async ({ artifactId, representationId, sessionId }) => {
    if (!representationId) {
      throw new Error("artifact_representation_missing");
    }
    const descriptor = resources.getArtifacts(sessionId).find((artifact) => artifact.artifactId === artifactId);
    if (!descriptor) throw new Error("artifact_descriptor_missing");
    const representation = descriptor.representations.find(
      (candidate) => candidate.representationId === representationId
    );
    if (!representation) {
      throw new Error("artifact_representation_missing");
    }
    if (!downloadGranted(descriptor, representationId)) {
      throw new Error("artifact_download_not_authorized");
    }
    return loadArtifactRepresentation(resources, {
      artifactId,
      descriptor,
      representation,
      representationId,
      sessionId
    });
  };
}
function downloadGranted(descriptor, representationId) {
  return descriptor.grants.some((grant) => {
    if (!grant.actions.includes("artifact.download")) return false;
    if (grant.representationIds.length > 0 && !grant.representationIds.includes(representationId)) {
      return false;
    }
    if (grant.minimumRevision !== void 0 && descriptor.revision < grant.minimumRevision) {
      return false;
    }
    if (grant.maximumRevision !== void 0 && descriptor.revision > grant.maximumRevision) {
      return false;
    }
    if (!grant.expiresAt) return true;
    const expiresAt = Date.parse(grant.expiresAt);
    return Number.isFinite(expiresAt) && expiresAt > Date.now();
  });
}
async function loadArtifactRepresentation(resources, context) {
  const { representation } = context;
  const transport = representation.transport?.transport;
  if (!transport?.case) throw new Error("artifact_transport_missing");
  if (transport.case === "inlineBytes") {
    return new Blob([transport.value.slice()], {
      type: representation.mediaType
    });
  }
  if (transport.case === "inlineText") {
    return new Blob([transport.value], { type: representation.mediaType });
  }
  if (transport.case === "resourceId") {
    return resources.loadResource(transport.value, context);
  }
  return resources.loadDownload(transport.value, context);
}
export {
  AgentSessionConnection as A,
  EventsConnectTransport as E,
  AgentSessionRuntimeV2 as a,
  AgentWorkbenchConnectTransport as b,
  artifactActionPayload as c,
  canComposePrompt as d,
  configurationPayload as e,
  createAgentArtifactLoader as f,
  createAgentWorkbenchRuntime as g,
  extensionPayload as h,
  interruptPayload as i,
  isAgentWorkbenchCursorRejected as j,
  projectGeneratedSessionSnapshot as k,
  AgentTaskQueue as l,
  parseAgentFormSurface as m,
  permissionPayload as p,
  sendPromptPayload as s,
  toolActionPayload as t,
  withPartnerIdentity as w
};
