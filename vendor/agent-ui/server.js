import { createHash } from "node:crypto";
import { clone, toBinary, create } from "@bufbuild/protobuf";
import { y as CommandEnvelopeSchema, aC as SessionSnapshotSchema, a as AgentEventSchema, ay as SessionDeltaBatchSchema, ax as SessionCursorSchema, B as CommandReceiptSchema } from "./chunks/session_pb-CU4zY4LI.js";
import { A as AgentSessionReductionError, a as AgentSessionStore, b as applyDeltaBatch, c as applySessionSnapshot, d as createAgentCommandEnvelope } from "./chunks/createAgentCommandEnvelope-DISPrJTM.js";
import "@bufbuild/protobuf/codegenv2";
class AgentWorkbenchSessionSource {
  store;
  replayLimit;
  batches = [];
  listeners = /* @__PURE__ */ new Set();
  commands = /* @__PURE__ */ new Map();
  publications = Promise.resolve();
  constructor(options) {
    this.replayLimit = options.replayLimit ?? 256;
    if (!Number.isSafeInteger(this.replayLimit) || this.replayLimit < 1) {
      throw new AgentSessionReductionError("session_source_replay_limit_invalid");
    }
    this.store = new AgentSessionStore({
      execute: (command) => {
        const pending = this.commands.get(command.commandId);
        if (!pending) throw new AgentSessionReductionError("session_source_command_missing");
        return pending.handler(clone(CommandEnvelopeSchema, command));
      },
      requestResync: async (_sessionId, reason) => {
        throw new AgentSessionReductionError(reason);
      }
    });
    this.store.applySnapshot(options.snapshot);
  }
  getSnapshot() {
    const snapshot = this.store.getState().snapshot;
    snapshot.digest = void 0;
    snapshot.digest = digest(toBinary(SessionSnapshotSchema, snapshot, { writeUnknownFields: false }));
    return snapshot;
  }
  async emit(events, streamEpoch) {
    if (this.store.getState().snapshot.streamEpoch !== streamEpoch) {
      throw new AgentSessionReductionError("session_source_epoch_stale");
    }
    const createdAt = (/* @__PURE__ */ new Date()).toISOString();
    const inputs = events.map((input) => clone(AgentEventSchema, create(AgentEventSchema, {
      envelope: { ...input, createdAt: input.createdAt ?? createdAt },
      event: input.event
    })));
    return this.enqueue(() => {
      const current = this.store.getState();
      const snapshot = current.snapshot;
      if (snapshot.streamEpoch !== streamEpoch) throw new AgentSessionReductionError("session_source_epoch_stale");
      if (inputs.length === 0 || inputs.some((event) => !event.envelope?.itemId || !event.envelope.createdAt || !Number.isFinite(Date.parse(event.envelope.createdAt)))) {
        throw new AgentSessionReductionError("session_source_event_identity_missing");
      }
      for (const input of inputs) {
        if (input.event.case !== "commandReceiptChanged") continue;
        const receipt = input.event.value.receipt;
        const pending = receipt && this.commands.get(receipt.commandId);
        if (pending && pending.digest !== receipt.payloadDigest) {
          throw new AgentSessionReductionError("command_id_conflict");
        }
      }
      const batch = create(SessionDeltaBatchSchema, {
        sessionId: snapshot.sessionId,
        streamEpoch: snapshot.streamEpoch,
        baseRevision: snapshot.revision,
        revision: snapshot.revision + 1n,
        firstSequence: snapshot.latestSequence + 1n,
        lastSequence: snapshot.latestSequence + BigInt(inputs.length),
        events: inputs.map((event, index) => create(AgentEventSchema, {
          event: event.event,
          envelope: {
            ...event.envelope,
            sessionId: snapshot.sessionId,
            streamEpoch: snapshot.streamEpoch,
            revision: snapshot.revision + 1n,
            sequence: snapshot.latestSequence + BigInt(index) + 1n
          }
        }))
      });
      if (batch.revision > 0xffffffffffffffffn || batch.lastSequence > 0xffffffffffffffffn) {
        throw new AgentSessionReductionError("session_source_position_overflow");
      }
      batch.digest = digest(toBinary(SessionDeltaBatchSchema, batch, { writeUnknownFields: false }));
      const next = applyDeltaBatch(current, batch);
      if (next.status !== "ready") throw new AgentSessionReductionError(next.resyncReason);
      this.store.applyDeltaBatch(batch);
      this.batches.push(batch);
      if (this.batches.length > this.replayLimit) this.batches.shift();
      this.wake();
      return clone(SessionDeltaBatchSchema, batch);
    });
  }
  async resetEpoch(snapshot) {
    const next = applySessionSnapshot(snapshot).snapshot;
    return this.enqueue(() => {
      const current = this.store.getState().snapshot;
      if (next.sessionId !== current.sessionId || next.streamEpoch === current.streamEpoch) {
        throw new AgentSessionReductionError("session_source_epoch_reset_required");
      }
      this.store.applySnapshot(next);
      this.batches.length = 0;
      this.commands.clear();
      this.wake();
    });
  }
  replay(cursor) {
    const snapshot = this.store.getState().snapshot;
    if (cursor.sessionId !== snapshot.sessionId) throw new AgentSessionReductionError("cursor_session_mismatch");
    if (cursor.streamEpoch !== snapshot.streamEpoch) throw new AgentSessionReductionError("cursor_epoch_mismatch");
    if (cursor.revision < 0n || cursor.sequence < 0n || cursor.revision > snapshot.revision || cursor.sequence > snapshot.latestSequence) {
      throw new AgentSessionReductionError("cursor_position_invalid");
    }
    if (cursor.revision === snapshot.revision && cursor.sequence === snapshot.latestSequence) return [];
    const oldest = this.batches[0];
    if (!oldest || cursor.revision < oldest.baseRevision || cursor.sequence < oldest.firstSequence - 1n) {
      throw new AgentSessionReductionError("cursor_stale");
    }
    const index = this.batches.findIndex((batch) => batch.baseRevision === cursor.revision && batch.firstSequence === cursor.sequence + 1n);
    if (index < 0) throw new AgentSessionReductionError("cursor_gap");
    return this.batches.slice(index).map((batch) => clone(SessionDeltaBatchSchema, batch));
  }
  async *subscribe(cursor, signal) {
    const position = clone(SessionCursorSchema, cursor);
    let wake;
    const abort = () => wake?.();
    signal.addEventListener("abort", abort, { once: true });
    try {
      while (!signal.aborted) {
        const ready = new Promise((resolve) => {
          wake = resolve;
        });
        const listener = wake;
        this.listeners.add(listener);
        try {
          for (const batch of this.replay(position)) {
            if (signal.aborted) return;
            if (this.store.getState().snapshot.streamEpoch !== position.streamEpoch) {
              throw new AgentSessionReductionError("cursor_epoch_mismatch");
            }
            position.revision = batch.revision;
            position.sequence = batch.lastSequence;
            yield batch;
          }
          if (!signal.aborted) await ready;
        } finally {
          this.listeners.delete(listener);
        }
      }
    } finally {
      signal.removeEventListener("abort", abort);
    }
  }
  async execute(command, handler) {
    const owned = clone(CommandEnvelopeSchema, command);
    if (!owned.commandId || !owned.command.case || !Number.isFinite(Date.parse(owned.issuedAt))) {
      throw new AgentSessionReductionError("session_source_command_invalid");
    }
    const canonical = await createAgentCommandEnvelope({ ...owned, command: owned.command });
    if (canonical.payloadDigest !== owned.payloadDigest) {
      throw new AgentSessionReductionError("command_payload_digest_invalid");
    }
    const snapshot = this.store.getState().snapshot;
    if (owned.sessionId !== snapshot.sessionId || owned.streamEpoch !== snapshot.streamEpoch) {
      throw new AgentSessionReductionError("command_session_mismatch");
    }
    const receipt = this.store.getCommandReceipt(owned.commandId);
    if (receipt) {
      if (receipt.payloadDigest !== owned.payloadDigest) throw new AgentSessionReductionError("command_id_conflict");
      return receipt;
    }
    const previous = this.commands.get(owned.commandId);
    if (previous) {
      if (previous.digest !== owned.payloadDigest) throw new AgentSessionReductionError("command_id_conflict");
      return clone(CommandReceiptSchema, await previous.result);
    }
    if (owned.expectedRevision !== void 0 && owned.expectedRevision !== snapshot.revision) {
      throw new AgentSessionReductionError("command_position_mismatch");
    }
    const pending = {
      digest: owned.payloadDigest,
      handler,
      result: Promise.resolve().then(() => this.store.execute(owned)).then((result) => clone(CommandReceiptSchema, result))
    };
    this.commands.set(owned.commandId, pending);
    return clone(CommandReceiptSchema, await pending.result);
  }
  enqueue(operation) {
    const result = this.publications.then(operation);
    this.publications = result.then(() => void 0, () => void 0);
    return result;
  }
  wake() {
    for (const listener of this.listeners) listener();
  }
}
function digest(bytes) {
  return `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
}
export {
  AgentSessionReductionError,
  AgentWorkbenchSessionSource
};
