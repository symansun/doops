import { clone, equals, create, toBinary } from "@bufbuild/protobuf";
import { D as CommandReceiptState, B as CommandReceiptSchema, A as AgentErrorSchema, f as ArtifactDescriptorSchema, o as ArtifactRevisionSchema, b2 as TimelineItemSchema, b1 as TimelineItemContentSchema, bm as UnsupportedValueSchema, N as EventEnvelopeSchema, aw as SessionConfigurationSchema, aI as SupportCapabilitiesSchema, aS as TerminalLeaseSchema, az as SessionResourceSchema, ae as PermissionRequestState, ah as PermissionResolutionSchema, ad as PermissionRequestSchema, aD as SessionStatus, a8 as PermissionDecision, aA as SessionResourceStatus, p as ArtifactStatus, aC as SessionSnapshotSchema, y as CommandEnvelopeSchema } from "./session_pb-CU4zY4LI.js";
import "@bufbuild/protobuf/codegenv2";
class AgentSessionReductionError extends Error {
  code;
  constructor(code) {
    super(code);
    this.name = "AgentSessionReductionError";
    this.code = code;
  }
}
function isSnapshotCursorConflict(cause) {
  return cause instanceof AgentSessionReductionError && cause.code === "snapshot_cursor_conflict";
}
function batchIdentity(input) {
  return [
    input.streamEpoch,
    input.revision.toString(),
    input.firstSequence.toString(),
    input.lastSequence.toString()
  ].join(":");
}
const allowedTransitions = /* @__PURE__ */ new Map([
  [
    CommandReceiptState.RECEIVED,
    /* @__PURE__ */ new Set([
      CommandReceiptState.ACCEPTED,
      CommandReceiptState.REJECTED,
      CommandReceiptState.FAILED
    ])
  ],
  [
    CommandReceiptState.ACCEPTED,
    /* @__PURE__ */ new Set([
      CommandReceiptState.RUNNING,
      CommandReceiptState.SUCCEEDED,
      CommandReceiptState.FAILED,
      CommandReceiptState.CANCELLED
    ])
  ],
  [
    CommandReceiptState.RUNNING,
    /* @__PURE__ */ new Set([
      CommandReceiptState.SUCCEEDED,
      CommandReceiptState.FAILED,
      CommandReceiptState.CANCELLED
    ])
  ]
]);
function validateCommandReceipt(receipt, sessionId) {
  if (receipt.sessionId !== sessionId || !receipt.commandId || !receipt.payloadDigest) {
    throw new AgentSessionReductionError("receipt_invalid");
  }
  if (receipt.state === CommandReceiptState.UNSPECIFIED) {
    throw new AgentSessionReductionError("receipt_initial_state");
  }
}
function upsertCommandReceipt(receipts, nextReceipt, sessionId) {
  validateCommandReceipt(nextReceipt, sessionId);
  const index = receipts.findIndex((receipt) => receipt.commandId === nextReceipt.commandId);
  if (index < 0) {
    return [...receipts, clone(CommandReceiptSchema, nextReceipt)];
  }
  const current = receipts[index];
  if (current.payloadDigest !== nextReceipt.payloadDigest) {
    throw new AgentSessionReductionError("command_id_conflict");
  }
  if (isReceiptOlder(nextReceipt, current) && canReach(nextReceipt.state, current.state)) {
    return receipts.slice();
  }
  if (current.state === nextReceipt.state) {
    if (isTerminal(current.state)) {
      if (!equals(CommandReceiptSchema, current, nextReceipt)) {
        throw new AgentSessionReductionError("receipt_terminal");
      }
      return receipts.slice();
    }
    const updated2 = receipts.slice();
    updated2[index] = mergeSameStateReceipt(current, nextReceipt);
    return updated2;
  }
  if (!allowedTransitions.get(current.state)?.has(nextReceipt.state)) {
    if (isTerminal(current.state)) {
      throw new AgentSessionReductionError("receipt_terminal");
    }
    throw new AgentSessionReductionError("receipt_transition_invalid");
  }
  const updated = receipts.slice();
  updated[index] = clone(CommandReceiptSchema, nextReceipt);
  return updated;
}
function mergeSameStateReceipt(current, next) {
  if (isReceiptOlder(next, current)) {
    return clone(CommandReceiptSchema, current);
  }
  const merged = clone(CommandReceiptSchema, next);
  merged.resultingRevision = maximumRevision(
    current.resultingRevision,
    next.resultingRevision
  );
  merged.error ??= current.error ? clone(AgentErrorSchema, current.error) : void 0;
  merged.receivedAt ||= current.receivedAt;
  merged.updatedAt = laterTimestamp(current.updatedAt, next.updatedAt);
  return merged;
}
function isReceiptOlder(candidate, current) {
  if (candidate.resultingRevision !== void 0 && current.resultingRevision !== void 0) {
    if (candidate.resultingRevision !== current.resultingRevision) {
      return candidate.resultingRevision < current.resultingRevision;
    }
  }
  const candidateTime = timestampMillis(candidate.updatedAt);
  const currentTime = timestampMillis(current.updatedAt);
  return candidateTime !== void 0 && currentTime !== void 0 && candidateTime < currentTime;
}
function maximumRevision(left, right) {
  if (left === void 0) return right;
  if (right === void 0) return left;
  return left > right ? left : right;
}
function laterTimestamp(left, right) {
  const leftTime = timestampMillis(left);
  const rightTime = timestampMillis(right);
  if (leftTime === void 0) return right ?? left;
  if (rightTime === void 0) return left;
  return rightTime >= leftTime ? right : left;
}
function timestampMillis(value) {
  if (!value) return void 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : void 0;
}
function canReach(from, target, visited = /* @__PURE__ */ new Set()) {
  if (from === target) return true;
  if (visited.has(from)) return false;
  visited.add(from);
  for (const next of allowedTransitions.get(from) ?? []) {
    if (canReach(next, target, visited)) return true;
  }
  return false;
}
function isTerminal(state) {
  return state === CommandReceiptState.SUCCEEDED || state === CommandReceiptState.FAILED || state === CommandReceiptState.REJECTED || state === CommandReceiptState.CANCELLED;
}
function mergeArtifactDescriptor(current, value) {
  const next = clone(ArtifactDescriptorSchema, value);
  if (!current) return next;
  const revisions = /* @__PURE__ */ new Map();
  for (const revision of [...current.revisions, ...value.revisions]) {
    revisions.set(
      revision.revision.toString(),
      clone(ArtifactRevisionSchema, revision)
    );
  }
  next.revisions = [...revisions.values()].sort(
    (left, right) => left.revision < right.revision ? -1 : left.revision > right.revision ? 1 : 0
  );
  return next;
}
function applyAgentEvent(snapshot, event) {
  const envelope = event.envelope;
  if (!envelope) throw new AgentSessionReductionError("event_envelope_missing");
  applyAgentEventPayload(snapshot, event);
  const turnId = envelope.turnId?.trim();
  if (turnId) snapshot.activeTurnId = turnId;
}
function applyAgentEventPayload(snapshot, event) {
  const envelope = event.envelope;
  if (!envelope) throw new AgentSessionReductionError("event_envelope_missing");
  switch (event.event.case) {
    case "timelineItemAppended": {
      const content = event.event.value.content;
      if (!content) throw new AgentSessionReductionError("timeline_content_missing");
      if (snapshot.history.some((item) => item.envelope?.itemId === envelope.itemId)) {
        throw new AgentSessionReductionError("timeline_item_conflict");
      }
      snapshot.history.push(
        create(TimelineItemSchema, {
          envelope: clone(EventEnvelopeSchema, envelope),
          content: clone(TimelineItemContentSchema, content)
        })
      );
      recordHistoryAppend(snapshot);
      return;
    }
    case "timelineItemUpdated": {
      const content = event.event.value.content;
      const index = snapshot.history.findIndex(
        (item) => item.envelope?.itemId === envelope.itemId
      );
      if (!content) throw new AgentSessionReductionError("timeline_content_missing");
      if (index < 0) {
        if (snapshot.hasOlderHistory) return;
        throw new AgentSessionReductionError("timeline_item_missing");
      }
      snapshot.history[index] = create(TimelineItemSchema, {
        envelope: clone(EventEnvelopeSchema, envelope),
        content: clone(TimelineItemContentSchema, content)
      });
      return;
    }
    case "commandReceiptChanged": {
      const receipt = event.event.value.receipt;
      if (!receipt) throw new AgentSessionReductionError("receipt_missing");
      snapshot.commandReceipts = upsertCommandReceipt(
        snapshot.commandReceipts,
        receipt,
        snapshot.sessionId
      );
      return;
    }
    case "permissionRequested": {
      const request = event.event.value.request;
      if (!request) throw new AgentSessionReductionError("permission_request_missing");
      snapshot.permissionRequests = upsert(
        snapshot.permissionRequests,
        clone(PermissionRequestSchema, request),
        "permissionRequestId"
      );
      return;
    }
    case "permissionResolved": {
      const resolution = event.event.value.resolution;
      if (!resolution) throw new AgentSessionReductionError("permission_resolution_missing");
      const request = snapshot.permissionRequests.find(
        (item) => item.permissionRequestId === resolution.permissionRequestId
      );
      if (!request) throw new AgentSessionReductionError("permission_request_missing");
      request.state = PermissionRequestState.RESOLVED;
      request.resolution = clone(PermissionResolutionSchema, resolution);
      return;
    }
    case "resourceChanged": {
      const resource = event.event.value.resource;
      if (!resource) throw new AgentSessionReductionError("resource_missing");
      snapshot.resources = upsert(
        snapshot.resources,
        clone(SessionResourceSchema, resource),
        "resourceId"
      );
      return;
    }
    case "artifactChanged": {
      const artifact = event.event.value.artifact;
      if (!artifact) throw new AgentSessionReductionError("artifact_missing");
      const index = snapshot.artifacts.findIndex(
        (item) => item.artifactId === artifact.artifactId
      );
      const merged = mergeArtifactDescriptor(snapshot.artifacts[index], artifact);
      if (index < 0) snapshot.artifacts.push(merged);
      else snapshot.artifacts[index] = merged;
      return;
    }
    case "terminalLeaseChanged": {
      const { lease, resourceId } = event.event.value;
      const resource = snapshot.resources.find(
        (item) => item.resourceId === resourceId
      );
      if (!resource || resource.resource.case !== "terminal") {
        throw new AgentSessionReductionError("terminal_resource_missing");
      }
      resource.resource.value.lease = lease ? clone(TerminalLeaseSchema, lease) : void 0;
      return;
    }
    case "capabilitiesChanged":
      snapshot.capabilities = event.event.value.capabilities ? clone(SupportCapabilitiesSchema, event.event.value.capabilities) : void 0;
      return;
    case "sessionStatusChanged":
      snapshot.status = event.event.value.status;
      snapshot.error = event.event.value.error ? clone(AgentErrorSchema, event.event.value.error) : void 0;
      return;
    case "configurationChanged":
      snapshot.configuration = create(SessionConfigurationSchema, {
        model: event.event.value.configuration?.model ?? snapshot.configuration?.model,
        permissionMode: event.event.value.configuration?.permissionMode ?? snapshot.configuration?.permissionMode
      });
      return;
    case "unsupported":
      if (snapshot.history.some((item) => item.envelope?.itemId === envelope.itemId)) {
        throw new AgentSessionReductionError("timeline_item_conflict");
      }
      snapshot.history.push(
        create(TimelineItemSchema, {
          envelope: clone(EventEnvelopeSchema, envelope),
          content: create(TimelineItemContentSchema, {
            content: {
              case: "unsupported",
              value: clone(UnsupportedValueSchema, event.event.value)
            }
          })
        })
      );
      recordHistoryAppend(snapshot);
      return;
    default:
      throw new AgentSessionReductionError("event_payload_missing");
  }
}
function recordHistoryAppend(snapshot) {
  snapshot.historyTotalSize = snapshot.historyTotalSize > BigInt(0) ? snapshot.historyTotalSize + BigInt(1) : BigInt(snapshot.history.length);
  snapshot.historyWindowSize = snapshot.history.length;
}
function upsert(items, value, key) {
  const next = items.slice();
  const index = next.findIndex((item) => item[key] === value[key]);
  if (index < 0) next.push(value);
  else next[index] = value;
  return next;
}
function validateSessionSnapshot(snapshot) {
  if (!snapshot.sessionId || !snapshot.streamEpoch || snapshot.status === SessionStatus.UNSPECIFIED) {
    throw new AgentSessionReductionError("snapshot_identity_missing");
  }
  if (snapshot.revision > snapshot.latestSequence || snapshot.revision === BigInt(0) !== (snapshot.latestSequence === BigInt(0))) {
    throw new AgentSessionReductionError("snapshot_position_invalid");
  }
  validateHistory(snapshot);
  validateReceipts(snapshot);
  validateGrants(snapshot);
  validatePermissions(snapshot);
  validateResources(snapshot);
  validateArtifacts(snapshot);
}
function validateHistory(snapshot) {
  const windowed = snapshot.hasOlderHistory || snapshot.historyWindowSize > 0 || snapshot.historyTotalSize > BigInt(0);
  if (windowed && (snapshot.historyWindowSize !== snapshot.history.length || snapshot.historyTotalSize < BigInt(snapshot.history.length) || snapshot.hasOlderHistory !== snapshot.historyTotalSize > BigInt(snapshot.history.length))) throw new AgentSessionReductionError("snapshot_history_window_invalid");
  const itemIds = /* @__PURE__ */ new Set();
  const sequences = /* @__PURE__ */ new Set();
  for (const item of snapshot.history) {
    const envelope = item.envelope;
    if (!envelope || !item.content || item.content.content.case === void 0 || !envelope.itemId || !envelope.createdAt || envelope.sessionId !== snapshot.sessionId || envelope.streamEpoch !== snapshot.streamEpoch || envelope.revision > snapshot.revision || envelope.sequence > snapshot.latestSequence || itemIds.has(envelope.itemId) || sequences.has(envelope.sequence)) {
      throw new AgentSessionReductionError("snapshot_history_invalid");
    }
    itemIds.add(envelope.itemId);
    sequences.add(envelope.sequence);
  }
}
function validateReceipts(snapshot) {
  const commandIds = /* @__PURE__ */ new Set();
  for (const receipt of snapshot.commandReceipts) {
    if (receipt.sessionId !== snapshot.sessionId || !receipt.commandId || !receipt.payloadDigest || receipt.state === CommandReceiptState.UNSPECIFIED || commandIds.has(receipt.commandId) || receipt.resultingRevision !== void 0 && receipt.resultingRevision > snapshot.revision) {
      throw new AgentSessionReductionError("snapshot_receipt_invalid");
    }
    commandIds.add(receipt.commandId);
  }
}
function validateGrants(snapshot) {
  const grantIds = /* @__PURE__ */ new Set();
  for (const grant of snapshot.grants) {
    if (!grant.grantId || grant.sessionId !== snapshot.sessionId || grantIds.has(grant.grantId)) {
      throw new AgentSessionReductionError("snapshot_grant_invalid");
    }
    grantIds.add(grant.grantId);
  }
}
function validatePermissions(snapshot) {
  validateUniqueIds(
    snapshot.permissionRequests,
    "permissionRequestId",
    (request) => {
      if (request.state === PermissionRequestState.UNSPECIFIED) return false;
      if (request.state === PermissionRequestState.RESOLVED) {
        return request.resolution?.permissionRequestId === request.permissionRequestId && request.resolution.decision !== PermissionDecision.UNSPECIFIED;
      }
      if (request.state === PermissionRequestState.PENDING) {
        return request.request.case !== void 0 && request.resolution === void 0;
      }
      return request.resolution === void 0;
    },
    "snapshot_permission_invalid"
  );
}
function validateResources(snapshot) {
  validateUniqueIds(
    snapshot.resources,
    "resourceId",
    (resource) => resource.status !== SessionResourceStatus.UNSPECIFIED && resource.resource.case !== void 0,
    "snapshot_resource_invalid"
  );
}
function validateArtifacts(snapshot) {
  validateUniqueIds(
    snapshot.artifacts,
    "artifactId",
    (artifact) => artifact.status !== ArtifactStatus.UNSPECIFIED && artifact.revision > BigInt(0) && Boolean(artifact.filename && artifact.mediaType),
    "snapshot_artifact_invalid"
  );
}
function validateUniqueIds(values, key, isValid, errorCode) {
  const ids = /* @__PURE__ */ new Set();
  for (const value of values) {
    const id = value[key];
    if (!id || ids.has(id) || !isValid(value)) {
      throw new AgentSessionReductionError(errorCode);
    }
    ids.add(id);
  }
}
function invalidatesHistoryVisibility(snapshot, events) {
  if (!snapshot.hasOlderHistory) return false;
  const known = new Set(snapshot.history.map((item) => item.envelope?.itemId));
  for (const event of events) {
    const id = event.envelope?.itemId;
    if (event.event.case === "timelineItemAppended" || event.event.case === "unsupported") {
      known.add(id);
    } else if (event.event.case === "timelineItemUpdated" && !known.has(id)) {
      const content = event.event.value.content?.content;
      if (content?.case === "message") return true;
      if (content?.case === "unsupported" && content.value.identity?.namespace === "agentcloud.transcript" && content.value.identity.semanticKey === "message") return true;
    }
  }
  return false;
}
const MAX_APPLIED_BATCHES = 256;
function applySessionSnapshot(snapshot) {
  validateSessionSnapshot(snapshot);
  return {
    snapshot: clone(SessionSnapshotSchema, snapshot),
    status: "ready",
    appliedBatches: /* @__PURE__ */ new Map()
  };
}
function applyDeltaBatch(state, batch) {
  if (batch.sessionId !== state.snapshot.sessionId) {
    throw new AgentSessionReductionError("session_id_mismatch");
  }
  const identity = batchIdentity(batch);
  const appliedDigest = state.appliedBatches.get(identity);
  if (appliedDigest !== void 0) {
    if (appliedDigest !== batch.digest) {
      return requireResync(state, "digest_conflict");
    }
    return state;
  }
  if (state.status === "resync_required") return state;
  if (batch.streamEpoch !== state.snapshot.streamEpoch) {
    return requireResync(state, "stream_epoch_changed");
  }
  if (batch.baseRevision !== state.snapshot.revision) {
    return requireResync(state, "base_revision_mismatch");
  }
  if (batch.firstSequence !== state.snapshot.latestSequence + BigInt(1)) {
    return requireResync(state, "sequence_gap");
  }
  validateBatch(batch);
  if (invalidatesHistoryVisibility(state.snapshot, batch.events)) {
    return requireResync(state, "history_visibility_changed");
  }
  const snapshot = clone(SessionSnapshotSchema, state.snapshot);
  for (const event of batch.events) applyAgentEvent(snapshot, event);
  snapshot.revision = batch.revision;
  snapshot.latestSequence = batch.lastSequence;
  validateSessionSnapshot(snapshot);
  const appliedBatches = new Map(state.appliedBatches);
  appliedBatches.set(identity, batch.digest);
  while (appliedBatches.size > MAX_APPLIED_BATCHES) {
    const oldest = appliedBatches.keys().next().value;
    if (oldest === void 0) break;
    appliedBatches.delete(oldest);
  }
  return { snapshot, status: "ready", appliedBatches };
}
function validateBatch(batch) {
  if (!batch.digest || batch.events.length === 0 || batch.revision !== batch.baseRevision + BigInt(1)) {
    throw new AgentSessionReductionError("delta_invalid");
  }
  if (batch.lastSequence < batch.firstSequence) {
    throw new AgentSessionReductionError("delta_sequence_range_invalid");
  }
  const expectedCount = batch.lastSequence - batch.firstSequence + BigInt(1);
  if (expectedCount !== BigInt(batch.events.length)) {
    throw new AgentSessionReductionError("delta_event_count_invalid");
  }
  batch.events.forEach((event, index) => {
    const envelope = event.envelope;
    const sequence = batch.firstSequence + BigInt(index);
    if (!envelope || envelope.sessionId !== batch.sessionId || envelope.streamEpoch !== batch.streamEpoch || envelope.revision !== batch.revision || envelope.sequence !== sequence || !envelope.itemId) {
      throw new AgentSessionReductionError("delta_event_envelope_invalid");
    }
  });
}
function requireResync(state, resyncReason) {
  return { ...state, status: "resync_required", resyncReason };
}
function validateSnapshotAdvance(current, next) {
  if (next.sessionId !== current.sessionId) {
    throw new AgentSessionReductionError("snapshot_session_mismatch");
  }
  if (next.streamEpoch !== current.streamEpoch) return "advance";
  if (next.revision < current.revision || next.latestSequence < current.latestSequence) {
    throw new AgentSessionReductionError("snapshot_stale");
  }
  if (next.hasOlderHistory && next.history.length < current.history.length) {
    throw new AgentSessionReductionError("snapshot_history_window_regressed");
  }
  if (next.revision !== current.revision || next.latestSequence !== current.latestSequence) {
    return "advance";
  }
  if (equals(SessionSnapshotSchema, current, next)) return "identical";
  const currentContent = clone(SessionSnapshotSchema, current);
  const nextContent = clone(SessionSnapshotSchema, next);
  normalizeHistoryWindow(currentContent, nextContent);
  clearViewerMetadata(currentContent);
  clearViewerMetadata(nextContent);
  if (equals(SessionSnapshotSchema, currentContent, nextContent)) {
    return "metadata";
  }
  throw new AgentSessionReductionError("snapshot_cursor_conflict");
}
function normalizeHistoryWindow(current, next) {
  if (next.history.length !== current.history.length) {
    if (!current.hasOlderHistory || next.history.length < current.history.length) {
      throw new AgentSessionReductionError("snapshot_cursor_conflict");
    }
    next.history = next.history.slice(next.history.length - current.history.length);
  }
  const currentTotal = current.historyTotalSize || BigInt(current.history.length);
  const nextTotal = next.historyTotalSize || BigInt(next.history.length);
  if (currentTotal !== nextTotal) throw new AgentSessionReductionError("snapshot_cursor_conflict");
  for (const snapshot of [current, next]) {
    snapshot.hasOlderHistory = false;
    snapshot.historyWindowSize = 0;
    snapshot.historyTotalSize = BigInt(0);
  }
}
function clearViewerMetadata(snapshot) {
  snapshot.digest = void 0;
  snapshot.grants = [];
  snapshot.hiddenArtifactIds = [];
  for (const artifact of snapshot.artifacts) artifact.grants = [];
}
class AgentSessionHistoryLoader {
  flight;
  load(signal, transport, current, apply) {
    signal.throwIfAborted();
    if (this.flight && !this.flight.signal.aborted) return this.flight.promise;
    if (!current().hasOlderHistory) return Promise.resolve();
    if (!transport.loadOlder || !transport.getSnapshot) {
      return Promise.reject(new Error("agent_workbench_history_unavailable"));
    }
    const promise = this.expand(signal, transport, current, apply);
    const flight = { signal, promise };
    this.flight = flight;
    void promise.finally(() => {
      if (this.flight === flight) this.flight = void 0;
    }).catch(() => {
    });
    return promise;
  }
  async expand(signal, transport, current, apply) {
    const epoch = current().streamEpoch;
    let snapshot = await transport.loadOlder(signal, current().history.length);
    for (let attempt = 0; ; attempt += 1) {
      signal.throwIfAborted();
      if (current().streamEpoch !== epoch || snapshot.streamEpoch !== epoch) {
        throw new AgentSessionReductionError("snapshot_epoch_stale");
      }
      try {
        apply(snapshot);
        return;
      } catch (error) {
        if (!(error instanceof AgentSessionReductionError) || error.code !== "snapshot_stale" || attempt >= 2) throw error;
        snapshot = await transport.getSnapshot(signal, current().history.length);
      }
    }
  }
}
class AgentSessionStore {
  constructor(transport) {
    this.transport = transport;
  }
  transport;
  state = null;
  listeners = /* @__PURE__ */ new Set();
  seenEpochs = /* @__PURE__ */ new Set();
  historyLoader = new AgentSessionHistoryLoader();
  getState() {
    return this.state ? cloneSessionState(this.state) : null;
  }
  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  loadOlder(signal) {
    return this.historyLoader.load(
      signal,
      this.transport,
      () => this.requireReadyState().snapshot,
      (snapshot) => this.applySnapshot(snapshot)
    );
  }
  applySnapshot(snapshot) {
    const next = applySessionSnapshot(snapshot);
    if (this.state) {
      if (next.snapshot.streamEpoch !== this.state.snapshot.streamEpoch && this.seenEpochs.has(next.snapshot.streamEpoch)) {
        throw new AgentSessionReductionError("snapshot_epoch_stale");
      }
      const advance = validateSnapshotAdvance(
        this.state.snapshot,
        next.snapshot
      );
      if (advance === "identical" && this.state.status === "ready") return;
      if (advance === "metadata" && this.state.status === "ready") {
        next.appliedBatches = this.state.appliedBatches;
      }
    }
    this.rememberEpoch(next.snapshot.streamEpoch);
    this.commit(next);
  }
  replaceSnapshot(snapshot) {
    this.state = null;
    this.applySnapshot(snapshot);
  }
  applyDeltaBatch(batch) {
    const state = this.requireState();
    const next = applyDeltaBatch(state, batch);
    if (next === state) return;
    this.commit(next);
  }
  async execute(command) {
    const state = this.requireReadyState();
    if (command.sessionId !== state.snapshot.sessionId || command.streamEpoch !== state.snapshot.streamEpoch) {
      throw new Error("command_session_mismatch");
    }
    const receipt = await this.transport.execute(command);
    if (receipt.sessionId !== command.sessionId || receipt.commandId !== command.commandId || receipt.payloadDigest !== command.payloadDigest) {
      throw new AgentSessionReductionError("command_receipt_mismatch");
    }
    const current = this.requireState();
    if (command.sessionId !== current.snapshot.sessionId || command.streamEpoch !== current.snapshot.streamEpoch) {
      throw new Error("command_session_mismatch");
    }
    validateCommandReceipt(receipt, current.snapshot.sessionId);
    return receipt;
  }
  getCommandReceipt(commandId) {
    const receipt = this.state?.snapshot.commandReceipts.find(
      (receipt2) => receipt2.commandId === commandId
    );
    return receipt ? clone(CommandReceiptSchema, receipt) : void 0;
  }
  async requestResync(reason) {
    const state = this.requireState();
    if (state.status !== "resync_required" || state.resyncReason !== reason) {
      this.commit({ ...state, status: "resync_required", resyncReason: reason });
    }
    await this.transport.requestResync(state.snapshot.sessionId, reason);
  }
  requireState() {
    if (!this.state) throw new Error("session_snapshot_missing");
    return this.state;
  }
  requireReadyState() {
    const state = this.requireState();
    if (state.status !== "ready") throw new Error("session_resync_required");
    return state;
  }
  commit(state) {
    this.state = state;
    this.publish();
  }
  rememberEpoch(streamEpoch) {
    this.seenEpochs.add(streamEpoch);
  }
  publish() {
    for (const listener of this.listeners) {
      try {
        listener();
      } catch (error) {
        console.error("agent_session_listener_failed", error);
      }
    }
  }
}
function cloneSessionState(state) {
  return {
    ...state,
    snapshot: clone(SessionSnapshotSchema, state.snapshot),
    appliedBatches: new Map(state.appliedBatches)
  };
}
async function createAgentCommandEnvelope(input) {
  const command = create(CommandEnvelopeSchema, {
    sessionId: input.sessionId,
    streamEpoch: input.streamEpoch,
    commandId: input.commandId,
    payloadDigest: "",
    expectedRevision: input.expectedRevision,
    issuedAt: input.issuedAt,
    command: input.command
  });
  const bytes = toBinary(CommandEnvelopeSchema, command, {
    writeUnknownFields: false
  });
  const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes);
  command.payloadDigest = `sha256:${hex(new Uint8Array(digest))}`;
  return command;
}
function hex(bytes) {
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
    ""
  );
}
export {
  AgentSessionReductionError as A,
  AgentSessionStore as a,
  applyDeltaBatch as b,
  applySessionSnapshot as c,
  createAgentCommandEnvelope as d,
  isSnapshotCursorConflict as i
};
