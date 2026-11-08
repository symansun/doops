"use client";
import { B as useAgentWorkspaceText, D as focusAdjacentTab, E as WorkspaceFullscreenButton, F as useElementFullscreen, G as AgentWorkspaceLocaleProvider, H as SessionDeckHeaderContext, I as AgentWorkspace, a as WorkerConversation, T as ToolRendererRegistry, J as toolRendererKeyId, K as contentRendererKeyId, L as userVideoExecutionSteps, U as UserTaskStatus, M as UserVideoExecutionTrace, N as artifactActionAllowed, O as ArtifactError, P as isSafeArtifactImage, Q as useArtifactBlobUrl, R as ContentRendererRegistry, V as GenericArtifactCard } from "./chunks/WorkerConversation-ZnlZqmaU.js";
import { X, Y, Z, _, $, a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, aa, S, ab, ac, W, b, c, ad, ae, d, e, f, g, h, i, j, af, k, ag, ah, ai, p, m, r, n, o, q, aj, s, t, ak, al, u, am, v, w, x, y, an, ao, z, A } from "./chunks/WorkerConversation-ZnlZqmaU.js";
import { jsxs, jsx, Fragment } from "react/jsx-runtime";
import { useCallback, useSyncExternalStore, useId, useState, useEffect, useRef, createContext, useMemo, useContext } from "react";
import { X as X2, Plus, Globe, Terminal, ListChecks, Image, Plug, MessageCircleQuestion, FilePlus2, Search, FileText, FilePenLine, FileDiff, MousePointerClick, Code, PackageCheck, ZoomOutIcon, ZoomInIcon, XIcon, ImageIcon, LoaderCircle, Images, Columns2, SlidersHorizontal, Send, RefreshCw, Replace, ArrowUp, ArrowDown, Download, Scan, ZoomIn, Maximize2, StickyNote, Minimize2, CircleAlert, Clock3 } from "lucide-react";
import { A as A2, a, l, b as b2, E, c as c2, d as d2, e as e2, f as f2, g as g2, i as i2, j as j2, m as m2, p as p2, k as k2, s as s2, w as w2 } from "./chunks/createAgentArtifactLoader-Bqd8IIje.js";
import { createPortal } from "react-dom";
import "@bufbuild/protobuf";
import "@bufbuild/protobuf/codegenv2";
import { d as d3 } from "./chunks/createAgentCommandEnvelope-DISPrJTM.js";
function AgentSessionDeckTab({
  active,
  entry,
  id,
  onActivate,
  onClose,
  panelId
}) {
  const text = useAgentWorkspaceText();
  const subscribe = useCallback(
    (listener) => {
      try {
        return entry.runtime?.subscribe(entry.sessionId, listener) ?? (() => void 0);
      } catch {
        return () => void 0;
      }
    },
    [entry]
  );
  const readSnapshot = useCallback(() => readSessionSnapshot(entry), [entry]);
  const snapshot = useSyncExternalStore(subscribe, readSnapshot, readSnapshot);
  const title = snapshot?.title.trim() || entry.fallbackTitle || entry.sessionId;
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-controls": panelId,
      "aria-label": title,
      "aria-selected": active,
      className: `flex h-8 max-w-48 shrink-0 items-center gap-1.5 rounded-md pl-2.5 text-xs ${onClose ? "pr-1" : "pr-2.5"} ${active ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/60"}`,
      id,
      onClick: onActivate,
      onKeyDown: (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onActivate();
        }
      },
      role: "tab",
      tabIndex: active ? 0 : -1,
      title,
      children: [
        /* @__PURE__ */ jsx(
          "span",
          {
            "aria-hidden": "true",
            className: `size-1.5 shrink-0 rounded-full ${sessionToneClass(snapshot)}`
          }
        ),
        /* @__PURE__ */ jsx("span", { className: "truncate", children: title }),
        onClose && /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": text.closeSession(title),
            className: "flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background/60 hover:text-foreground",
            onClick: (event) => {
              event.stopPropagation();
              onClose();
            },
            type: "button",
            children: /* @__PURE__ */ jsx(X2, { className: "size-3" })
          }
        )
      ]
    }
  );
}
function readSessionSnapshot(entry) {
  try {
    return entry.runtime?.getSnapshot(entry.sessionId) ?? null;
  } catch {
    return null;
  }
}
function sessionToneClass(snapshot) {
  if (!snapshot) return "bg-muted-foreground/40";
  if (snapshot.status === "failed") return "bg-danger";
  if (snapshot.status === "launching") return "animate-pulse bg-info";
  if (snapshot.status === "waiting") return "animate-pulse bg-warning";
  if (snapshot.status === "running") return "bg-success";
  if (snapshot.connection === "connected") return "bg-success";
  return "bg-muted-foreground/40";
}
function AgentSessionDeckStrip({
  activeSessionId,
  baseId,
  controlsRef,
  fullscreenActive,
  fullscreenSupported,
  onActivate,
  onCloseSession,
  onCreateSession,
  onToggleFullscreen,
  compact = false,
  showFullscreen = true,
  sessions
}) {
  return /* @__PURE__ */ jsxs("header", { className: `flex shrink-0 items-center gap-1 border-b border-border px-2 ${compact ? "h-12 [&_[role=tab]]:min-h-11 [&_[role=tab]]:min-w-11 [&_button]:min-h-11 [&_button]:min-w-11" : "h-14"}`, children: [
    /* @__PURE__ */ jsx(
      AgentSessionDeckTabs,
      {
        activeSessionId,
        baseId,
        onActivate,
        onCloseSession,
        sessions
      }
    ),
    /* @__PURE__ */ jsx("div", { ref: controlsRef, className: "flex min-w-0 max-w-[55%] shrink-0 items-center overflow-x-auto" }),
    /* @__PURE__ */ jsx(
      AgentSessionDeckActions,
      {
        fullscreenActive,
        fullscreenSupported: showFullscreen && fullscreenSupported,
        onCreateSession,
        onToggleFullscreen
      }
    )
  ] });
}
function AgentSessionDeckTabs({
  activeSessionId,
  baseId,
  onActivate,
  onCloseSession,
  sessions
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsx(
    "div",
    {
      "aria-label": text.sessions,
      className: "flex min-w-0 flex-1 items-center gap-1 overflow-x-auto",
      onKeyDown: focusAdjacentTab,
      role: "tablist",
      children: sessions.map((entry) => /* @__PURE__ */ jsx(
        AgentSessionDeckTab,
        {
          active: entry.sessionId === activeSessionId,
          entry,
          id: `${baseId}-${entry.sessionId}-tab`,
          onActivate: () => onActivate(entry.sessionId),
          onClose: onCloseSession ? () => onCloseSession(entry.sessionId) : void 0,
          panelId: `${baseId}-${entry.sessionId}-panel`
        },
        entry.sessionId
      ))
    }
  );
}
function AgentSessionDeckActions({
  fullscreenActive,
  fullscreenSupported,
  onCreateSession,
  onToggleFullscreen
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 items-center gap-1", children: [
    onCreateSession && /* @__PURE__ */ jsxs(
      "button",
      {
        "aria-label": text.newSession,
        className: "flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted/60",
        onClick: onCreateSession,
        type: "button",
        children: [
          /* @__PURE__ */ jsx(Plus, { className: "size-3.5" }),
          /* @__PURE__ */ jsx("span", { className: "hidden sm:inline", children: text.newSession })
        ]
      }
    ),
    /* @__PURE__ */ jsx(
      WorkspaceFullscreenButton,
      {
        active: fullscreenActive,
        onToggle: onToggleFullscreen,
        supported: fullscreenSupported
      }
    )
  ] });
}
function AgentSessionDeckFrame({
  activeSessionId,
  className = "",
  defaultActiveSessionId,
  locale = "en-US",
  onActiveSessionChange,
  onCloseSession,
  onCreateSession,
  sessionDeckMode = "full",
  renderSession,
  sessions
}) {
  const baseId = useId();
  const [innerActive, setInnerActive] = useState(defaultActiveSessionId);
  const requested = activeSessionId ?? innerActive;
  const resolvedActive = sessions.some((s3) => s3.sessionId === requested) ? requested : sessions[0]?.sessionId;
  const [visited, setVisited] = useState(
    () => resolvedActive ? [resolvedActive] : []
  );
  useEffect(() => {
    if (resolvedActive && !visited.includes(resolvedActive)) {
      setVisited((current) => [...current, resolvedActive]);
    }
  }, [resolvedActive, visited]);
  const mounted = sessions.filter(
    (entry) => entry.sessionId === resolvedActive || visited.includes(entry.sessionId)
  );
  const activate = (sessionId) => {
    setInnerActive(sessionId);
    onActiveSessionChange?.(sessionId);
  };
  const deckRef = useRef(null);
  const [headerControls, setHeaderControls] = useState(null);
  const fullscreen = useElementFullscreen(deckRef);
  return /* @__PURE__ */ jsx(AgentWorkspaceLocaleProvider, { locale, children: /* @__PURE__ */ jsxs(
    "div",
    {
      className: `flex h-full min-h-0 flex-col overflow-hidden bg-background text-foreground ${fullscreen.active ? "agent-workspace-fullscreen" : ""} ${className}`,
      "data-agent-session-deck": "",
      ref: deckRef,
      children: [
        /* @__PURE__ */ jsx(
          AgentSessionDeckStrip,
          {
            activeSessionId: resolvedActive,
            baseId,
            controlsRef: setHeaderControls,
            fullscreenActive: fullscreen.active,
            fullscreenSupported: fullscreen.supported,
            compact: sessionDeckMode === "compact",
            showFullscreen: sessionDeckMode === "full",
            onActivate: activate,
            onCloseSession,
            onCreateSession,
            onToggleFullscreen: fullscreen.toggle,
            sessions
          }
        ),
        mounted.map((entry) => {
          const active = entry.sessionId === resolvedActive;
          return /* @__PURE__ */ jsx(
            SessionDeckHeaderContext.Provider,
            {
              value: { controls: active ? headerControls : null, inDeck: true },
              children: /* @__PURE__ */ jsx(
                "section",
                {
                  "aria-hidden": !active,
                  "aria-labelledby": `${baseId}-${entry.sessionId}-tab`,
                  className: active ? "flex min-h-0 flex-1 flex-col" : "hidden",
                  id: `${baseId}-${entry.sessionId}-panel`,
                  role: "tabpanel",
                  children: renderSession(entry, active)
                }
              )
            },
            entry.sessionId
          );
        })
      ]
    }
  ) });
}
function AgentSessionDeck({
  activeSessionId,
  className = "",
  defaultActiveSessionId,
  locale = "en-US",
  onActiveSessionChange,
  onCloseSession,
  onCreateSession,
  sessionDeckMode,
  sessions,
  ...workspaceProps
}) {
  return /* @__PURE__ */ jsx(
    AgentSessionDeckFrame,
    {
      activeSessionId,
      className,
      defaultActiveSessionId,
      locale,
      onActiveSessionChange,
      onCloseSession,
      onCreateSession,
      sessionDeckMode,
      renderSession: (entry) => entry.runtime ? /* @__PURE__ */ jsx(
        AgentWorkspace,
        {
          ...workspaceProps,
          className: "min-h-0 flex-1",
          locale,
          runtime: entry.runtime,
          sessionId: entry.sessionId,
          showFullscreen: false,
          terminalRuntime: entry.terminalRuntime
        }
      ) : null,
      sessions
    }
  );
}
function WorkerConversationDeck({
  activeSessionId,
  className = "",
  defaultActiveSessionId,
  locale = "en-US",
  onActiveSessionChange,
  onCreateSession,
  sessionDeckMode,
  renderSession,
  sessions,
  ...conversationProps
}) {
  return /* @__PURE__ */ jsx(
    AgentSessionDeckFrame,
    {
      activeSessionId,
      className,
      defaultActiveSessionId,
      locale,
      onActiveSessionChange,
      onCreateSession,
      sessionDeckMode,
      renderSession: (entry, active) => renderSession ? renderSession(entry, active) : /* @__PURE__ */ jsx(
        WorkerConversation,
        {
          ...conversationProps,
          className: "min-h-0 flex-1",
          locale,
          showFullscreen: false,
          terminalRuntime: entry.terminalRuntime,
          workerRef: entry.workerRef
        }
      ),
      sessions
    }
  );
}
const presentations = {
  "artifact.publish": { icon: PackageCheck, label: "Publish artifact" },
  "code.interpret": {
    icon: Code,
    inputLabel: "Code",
    label: "Run code",
    outputLabel: "Output"
  },
  "computer.use": {
    icon: MousePointerClick,
    inputLabel: "Action",
    label: "Computer use",
    outputLabel: "Result"
  },
  "filesystem.change": {
    icon: FileDiff,
    inputLabel: "Path",
    label: "File change",
    outputLabel: "Change"
  },
  "filesystem.edit": {
    icon: FilePenLine,
    inputLabel: "Path",
    label: "File change",
    outputLabel: "Change"
  },
  "filesystem.read": {
    icon: FileText,
    inputLabel: "Path",
    label: "Read file",
    outputLabel: "Content"
  },
  "filesystem.search": {
    icon: Search,
    inputLabel: "Query",
    label: "Search",
    outputLabel: "Matches"
  },
  "filesystem.write": {
    icon: FilePlus2,
    inputLabel: "Path",
    label: "Write file",
    outputLabel: "Result"
  },
  "interaction.question": {
    icon: MessageCircleQuestion,
    inputLabel: "Prompt",
    label: "Ask user",
    outputLabel: "Result"
  },
  "mcp.call": {
    icon: Plug,
    inputLabel: "Request",
    label: "MCP call",
    outputLabel: "Result"
  },
  "mcp.list-tools": { icon: Plug, label: "MCP tools" },
  "media.image.generate": {
    icon: Image,
    inputLabel: "Prompt",
    label: "Image generation",
    outputLabel: "Result"
  },
  "plan.todo": { icon: ListChecks, label: "Plan" },
  "shell.execute": {
    icon: Terminal,
    inputLabel: "Command",
    label: "Shell",
    outputLabel: "Output"
  },
  "web.fetch": {
    icon: Globe,
    inputLabel: "Request",
    label: "Fetch page",
    outputLabel: "Content"
  },
  "web.search": {
    icon: Globe,
    inputLabel: "Query",
    label: "Web search",
    outputLabel: "Matches"
  }
};
const namespaces = [
  "agentcloud.acp",
  "agentcloud.claude",
  "agentcloud.codex",
  "agentcloud.runner"
];
function createBuiltinToolRenderers(namespacesOverride = namespaces) {
  const registry = new ToolRendererRegistry();
  for (const [semanticKey, presentation] of Object.entries(presentations)) {
    for (const namespace of namespacesOverride) {
      registry.register(
        { namespace, schemaVersion: "1", semanticKey },
        { presentation },
        `builtin.${semanticKey}`
      );
    }
  }
  return registry;
}
const DEFAULT_AGENT_COMMANDS = [
  {
    name: "compact",
    label: "/compact",
    description: "Compact the current context"
  },
  {
    name: "context",
    label: "/context",
    description: "Show context usage"
  },
  {
    name: "help",
    label: "/help",
    description: "Show available commands"
  },
  {
    name: "model",
    label: "/model",
    description: "Switch the active model",
    requiresArgument: true
  },
  {
    name: "effort",
    label: "/effort",
    description: "Change reasoning effort",
    requiresArgument: true
  }
];
const MIN_ZOOM = 1;
const MAX_ZOOM = 8;
const ZOOM_STEP = 0.5;
function AgentImageZoomViewer({ image }) {
  const [zoom, setZoom] = useState(MIN_ZOOM);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const dragRef = useRef(null);
  const zoomed = zoom > MIN_ZOOM;
  const applyZoom = (next) => setZoom(Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, next)));
  useEffect(() => {
    if (!zoomed) setOffset({ x: 0, y: 0 });
  }, [zoomed]);
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    /* @__PURE__ */ jsx(
      "div",
      {
        className: "absolute inset-0 flex items-center justify-center overflow-hidden",
        onDoubleClick: () => applyZoom(zoomed ? MIN_ZOOM : 2),
        onPointerCancel: () => {
          dragRef.current = null;
        },
        onPointerDown: (event) => {
          if (!zoomed) return;
          dragRef.current = {
            pointerId: event.pointerId,
            startX: event.clientX - offset.x,
            startY: event.clientY - offset.y
          };
          event.currentTarget.setPointerCapture(event.pointerId);
        },
        onPointerMove: (event) => {
          const drag = dragRef.current;
          if (!drag) return;
          setOffset({
            x: event.clientX - drag.startX,
            y: event.clientY - drag.startY
          });
        },
        onPointerUp: (event) => {
          if (dragRef.current?.pointerId === event.pointerId) {
            dragRef.current = null;
          }
        },
        style: { cursor: zoomed ? "grab" : "zoom-in" },
        children: /* @__PURE__ */ jsx(
          "img",
          {
            alt: image.alt,
            className: "max-h-[92vh] max-w-[94vw] origin-center object-contain select-none",
            draggable: false,
            src: image.src,
            style: {
              transform: `translate(${offset.x}px, ${offset.y}px) scale(${zoom})`,
              transition: dragRef.current ? "none" : "transform 120ms ease-out"
            }
          }
        )
      }
    ),
    /* @__PURE__ */ jsxs("div", { className: "absolute bottom-3 left-1/2 z-[62] flex -translate-x-1/2 items-center gap-1 rounded-full bg-background/80 p-1 shadow-sm ring-1 ring-foreground/10 backdrop-blur-xs", children: [
      /* @__PURE__ */ jsx(
        ZoomButton,
        {
          disabled: zoom <= MIN_ZOOM,
          label: "Zoom out",
          onClick: () => applyZoom(zoom - ZOOM_STEP),
          children: /* @__PURE__ */ jsx(ZoomOutIcon, { className: "size-4" })
        }
      ),
      /* @__PURE__ */ jsxs(
        "button",
        {
          "aria-label": "Reset zoom",
          className: "min-w-[3ch] px-1 text-center text-xs tabular-nums text-muted-foreground hover:text-foreground",
          onClick: () => applyZoom(MIN_ZOOM),
          type: "button",
          children: [
            Math.round(zoom * 100),
            "%"
          ]
        }
      ),
      /* @__PURE__ */ jsx(
        ZoomButton,
        {
          disabled: zoom >= MAX_ZOOM,
          label: "Zoom in",
          onClick: () => applyZoom(zoom + ZOOM_STEP),
          children: /* @__PURE__ */ jsx(ZoomInIcon, { className: "size-4" })
        }
      )
    ] })
  ] });
}
function ZoomButton({
  children,
  disabled,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": label,
      className: "inline-flex size-8 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40",
      disabled,
      onClick,
      type: "button",
      children
    }
  );
}
const LightboxContext = createContext(null);
function useLightbox() {
  const value = useContext(LightboxContext);
  if (!value) {
    throw new Error("useLightbox must be used within ImageLightboxProvider");
  }
  return value;
}
function ZoomableImage({
  alt,
  className,
  src,
  ...imgProps
}) {
  const { open } = useLightbox();
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": alt ? `Zoom image: ${alt}` : "Zoom image",
      className: "m-0 inline-flex max-w-full cursor-zoom-in appearance-none border-0 bg-transparent p-0 leading-none",
      onClick: () => {
        if (src) open({ alt, src });
      },
      type: "button",
      children: /* @__PURE__ */ jsx("img", { ...imgProps, alt, className, src })
    }
  );
}
function ImageLightboxProvider({
  children,
  portalContainer
}) {
  const [image, setImage] = useState(null);
  const open = useCallback((next) => setImage(next), []);
  const value = useMemo(() => ({ open }), [open]);
  const target = portalContainer ?? (typeof document === "undefined" ? null : document.body);
  useEffect(() => {
    if (!image) return;
    const handleKeyDown = (event) => {
      if (event.key === "Escape") setImage(null);
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [image]);
  return /* @__PURE__ */ jsxs(LightboxContext.Provider, { value, children: [
    children,
    target && image ? createPortal(
      /* @__PURE__ */ jsx(
        ImageLightboxDialog,
        {
          image,
          onClose: () => setImage(null)
        }
      ),
      target
    ) : null
  ] });
}
function ImageLightboxDialog({
  image,
  onClose
}) {
  const closeButtonRef = useRef(null);
  useEffect(() => {
    closeButtonRef.current?.focus();
  }, []);
  return /* @__PURE__ */ jsx("div", { className: "agent-ui fixed inset-0 z-[60] bg-black/80", role: "presentation", children: /* @__PURE__ */ jsxs(
    "section",
    {
      "aria-label": image.alt || "Image preview",
      "aria-modal": "true",
      className: "fixed inset-0 z-[61] outline-none",
      role: "dialog",
      children: [
        /* @__PURE__ */ jsx(AgentImageZoomViewer, { image }),
        /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": "Close",
            className: "absolute right-3 top-3 inline-flex size-8 items-center justify-center rounded-md bg-background/80 text-foreground shadow-sm ring-1 ring-foreground/10 hover:bg-background",
            onClick: onClose,
            ref: closeButtonRef,
            type: "button",
            children: /* @__PURE__ */ jsx(XIcon, { className: "size-4" })
          }
        )
      ]
    }
  ) });
}
class AgentAdapterRegistry {
  contentRenderers;
  toolRenderers;
  adapters = /* @__PURE__ */ new Map();
  constructor(options = {}) {
    this.contentRenderers = options.contentRenderers;
    this.toolRenderers = options.toolRenderers;
  }
  register(adapter) {
    const id = adapter.descriptor.id;
    if (this.adapters.has(id)) {
      throw new Error(`agent_adapter_conflict: id=${id}`);
    }
    const renderers = adapter.renderers;
    const sourceId = `adapter.${id}`;
    validateRendererEntries({
      adapterId: id,
      entries: renderers?.tools ?? [],
      registry: this.toolRenderers,
      keyId: toolRendererKeyId,
      missingRegistryError: "agent_adapter_tool_registry_missing"
    });
    validateRendererEntries({
      adapterId: id,
      entries: renderers?.content ?? [],
      registry: this.contentRenderers,
      keyId: contentRendererKeyId,
      missingRegistryError: "agent_adapter_content_registry_missing"
    });
    for (const entry of renderers?.tools ?? []) {
      this.toolRenderers?.register(
        entry.key,
        entry.renderer,
        entry.sourceId ?? sourceId
      );
    }
    for (const entry of renderers?.content ?? []) {
      this.contentRenderers?.register(
        entry.key,
        entry.renderer,
        entry.sourceId ?? sourceId
      );
    }
    this.adapters.set(id, adapter);
  }
  get(id) {
    return this.adapters.get(id);
  }
  require(id) {
    const adapter = this.get(id);
    if (!adapter) throw new Error(`agent_adapter_missing: id=${id}`);
    return adapter;
  }
  list() {
    return [...this.adapters.values()];
  }
}
function validateRendererEntries(input) {
  if (input.entries.length > 0 && !input.registry) {
    throw new Error(`${input.missingRegistryError}: id=${input.adapterId}`);
  }
  const keys = /* @__PURE__ */ new Set();
  for (const entry of input.entries) {
    const id = input.keyId(entry.key);
    if (keys.has(id) || input.registry?.has(entry.key)) {
      throw new Error(`agent_adapter_renderer_conflict: id=${input.adapterId}`);
    }
    keys.add(id);
  }
}
const identity = { "namespace": "agentcloud.ui", "semantic_key": "form", "schema_version": "1" };
const components = { "Panel": { "description": "表单区域", "children": true, "props": { "type": "object", "additionalProperties": false, "properties": {} } }, "SchemaForm": { "description": "应用动态表单", "children": false, "props": { "type": "object", "additionalProperties": false, "properties": {} } }, "FieldIssues": { "description": "应用字段错误", "children": false, "props": { "type": "object", "additionalProperties": false, "properties": {} } }, "NoticeDialog": { "description": "应用须知弹框", "children": false, "props": { "type": "object", "additionalProperties": false, "required": ["title", "trigger", "paragraphs"], "properties": { "title": { "type": "string", "minLength": 1, "maxLength": 100 }, "trigger": { "type": "string", "minLength": 1, "maxLength": 100 }, "paragraphs": { "type": "array", "minItems": 1, "maxItems": 12, "items": { "type": "string", "minLength": 1, "maxLength": 1e3 } } } } }, "WorkflowActions": { "description": "须知同意、签署与取消申请", "children": false, "props": { "type": "object", "additionalProperties": false, "properties": {} } } };
const limits = { "payload_bytes": 262144, "nodes": 16, "children": 16, "schema_nodes": 4e3, "schema_depth": 20 };
const schema_policy = { "dialect": "http://json-schema.org/draft-07/schema#", "forbidden_keywords": ["__proto__", "prototype", "constructor", "$ref", "$dynamicRef", "$async", "ui:field", "ui:FieldTemplate", "ui:ObjectFieldTemplate", "ui:ArrayFieldTemplate"], "widgets": ["text", "textarea", "select", "checkbox", "checkboxes", "radio", "date", "hidden", "LocalDateTime"], "formats": ["date", "date-time", "email", "uri"] };
const surface_schema = { "$schema": "http://json-schema.org/draft-07/schema#", "type": "object", "additionalProperties": false, "required": ["kind", "app_slug", "form_id", "instance_id", "data_revision", "data", "schema", "actions"], "properties": { "kind": { "const": "form" }, "app_slug": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "form_id": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "instance_id": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "data_revision": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 }, "data": { "type": "object" }, "schema": { "type": "object", "required": ["type"], "properties": { "type": { "const": "object" } } }, "ui_schema": { "type": "object" }, "read_only": { "type": "boolean" }, "actions": { "type": "array", "maxItems": 12, "items": { "type": "object", "additionalProperties": false, "required": ["id", "label", "behavior"], "properties": { "id": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "label": { "type": "string", "minLength": 1, "maxLength": 100 }, "behavior": { "enum": ["validate", "send"] }, "confirmation": { "type": "object", "additionalProperties": false, "required": ["title", "description", "confirmLabel"], "properties": { "title": { "type": "string", "minLength": 1, "maxLength": 100 }, "description": { "type": "string", "minLength": 1, "maxLength": 500 }, "confirmLabel": { "type": "string", "minLength": 1, "maxLength": 100 } } } } } }, "spec": { "type": "object" }, "errors": { "type": "object", "additionalProperties": false, "required": ["data_revision", "fields"], "properties": { "data_revision": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 }, "fields": { "type": "array", "maxItems": 100, "items": { "type": "object", "additionalProperties": false, "required": ["field", "description"], "properties": { "field": { "type": "string", "maxLength": 400 }, "description": { "type": "string", "maxLength": 500 } } } } } }, "workflow": { "type": "object", "additionalProperties": false, "required": ["notice", "pledge", "actions"], "properties": { "notice": { "type": "object", "additionalProperties": false, "required": ["id", "version", "title", "paragraphs"], "properties": { "id": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "version": { "type": "string", "minLength": 1, "maxLength": 100 }, "title": { "type": "string", "minLength": 1, "maxLength": 100 }, "paragraphs": { "type": "array", "minItems": 1, "maxItems": 12, "items": { "type": "string", "minLength": 1, "maxLength": 1e3 } } } }, "pledge": { "type": "object", "additionalProperties": false, "required": ["id", "version", "title", "paragraphs"], "properties": { "id": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "version": { "type": "string", "minLength": 1, "maxLength": 100 }, "title": { "type": "string", "minLength": 1, "maxLength": 100 }, "paragraphs": { "type": "array", "minItems": 1, "maxItems": 12, "items": { "type": "string", "minLength": 1, "maxLength": 1e3 } } } }, "actions": { "type": "object", "additionalProperties": false, "required": ["accept", "decline", "save", "sign", "cancel"], "properties": { "accept": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "decline": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "save": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "sign": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" }, "cancel": { "type": "string", "minLength": 2, "maxLength": 100, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$" } } } } } } };
const sections_schema = { "type": "array", "minItems": 1, "maxItems": 12, "items": { "type": "object", "additionalProperties": false, "required": ["title", "fields"], "properties": { "title": { "type": "string", "minLength": 1, "maxLength": 100 }, "fields": { "type": "array", "minItems": 1, "maxItems": 100, "items": { "type": "string", "minLength": 1, "maxLength": 100 } }, "collapsed": { "type": "boolean" }, "summaryFields": { "type": "array", "maxItems": 5, "items": { "type": "string", "minLength": 1, "maxLength": 100 } } } } };
const application_actions = { "vehicle-booking/booking": ["send-booking", "accept-rules", "decline-rules", "sign-pledge", "cancel-booking"] };
const catalog = {
  identity,
  components,
  limits,
  schema_policy,
  surface_schema,
  sections_schema,
  application_actions
};
const agentFormCatalog = catalog;
function formCommandFailure(error) {
  const receipt = error && typeof error === "object" && "receipt" in error ? error.receipt : void 0;
  const value = receipt && typeof receipt === "object" && "error" in receipt ? receipt.error : void 0;
  if (!value || typeof value !== "object") return { fields: [], message: "保存未确认，可重试同一请求。", terminal: false };
  const fields = "fieldViolations" in value && Array.isArray(value.fieldViolations) ? value.fieldViolations.flatMap((field) => field && typeof field.field === "string" && typeof field.description === "string" ? [{ field: field.field, description: field.description }] : []) : [];
  return { fields, message: fields.length ? "申请未保存，请修改标出的字段。" : "申请处理失败，请重新核对后发送。", terminal: true };
}
function SessionResourceImage({
  alt,
  className,
  loadBlob,
  path
}) {
  const [blobUrl, setBlobUrl] = useState(null);
  const [state, setState] = useState(
    "loading"
  );
  useEffect(() => {
    if (!path) {
      setState("error");
      return;
    }
    setBlobUrl(null);
    setState("loading");
    let active = true;
    let objectUrl = null;
    void loadBlob(path).then((blob) => {
      if (!active) return;
      objectUrl = URL.createObjectURL(blob);
      setBlobUrl(objectUrl);
      setState("loaded");
    }).catch(() => {
      if (active) setState("error");
    });
    return () => {
      active = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [loadBlob, path]);
  if (state === "error") {
    return /* @__PURE__ */ jsxs(
      "div",
      {
        "aria-label": alt,
        className: joinClassNames(
          "flex items-center gap-1.5 rounded-md border border-border bg-muted px-2 py-1.5 text-xs text-muted-foreground",
          className
        ),
        role: "img",
        children: [
          /* @__PURE__ */ jsx(ImageIcon, { className: "size-3.5 shrink-0" }),
          /* @__PURE__ */ jsx("span", { className: "truncate", children: alt })
        ]
      }
    );
  }
  if (state === "loading" || !blobUrl) {
    return /* @__PURE__ */ jsx(
      "div",
      {
        "aria-label": "Loading image",
        className: joinClassNames(
          "flex size-24 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground",
          className
        ),
        role: "status",
        children: /* @__PURE__ */ jsx(LoaderCircle, { className: "size-4 animate-spin" })
      }
    );
  }
  return /* @__PURE__ */ jsx(ZoomableImage, { alt, className, src: blobUrl });
}
function joinClassNames(...values) {
  return values.filter(Boolean).join(" ");
}
function UserVideoTaskPresentation({
  artifacts,
  locale = "en-US",
  snapshot
}) {
  const steps = userVideoExecutionSteps(snapshot, artifacts);
  return /* @__PURE__ */ jsxs(AgentWorkspaceLocaleProvider, { locale, children: [
    /* @__PURE__ */ jsx(UserTaskStatus, { artifacts, snapshot }),
    /* @__PURE__ */ jsx(UserVideoExecutionTrace, { steps })
  ] });
}
function workspaceFileArtifacts(sourceId, changes) {
  if (!Array.isArray(changes)) return [];
  return changes.flatMap((change, index) => {
    const path = changedPath(change);
    const mimeType = path ? deliverableMimeType(path) : void 0;
    if (!path || !mimeType || hasIgnoredPathSegment(path) || changedStatus(change) === "deleted") {
      return [];
    }
    return [{
      actions: [],
      id: `${sourceId}:artifact:${index}`,
      kind: "artifact",
      artifactId: `workspace:${path}`,
      filename: path.split("/").pop() || path,
      grants: [],
      manifest: null,
      mimeType,
      representations: [],
      revision: BigInt(0),
      role: "preview",
      schemaVersion: "1",
      selectedRepresentationId: null,
      status: "completed"
    }];
  });
}
function changedPath(value) {
  if (!value || typeof value !== "object") return null;
  const path = value.path;
  return typeof path === "string" && path.trim() ? path : null;
}
function changedStatus(value) {
  if (!value || typeof value !== "object") return null;
  const status = value.status;
  return typeof status === "string" ? status.toLowerCase() : null;
}
function hasIgnoredPathSegment(path) {
  return path.split("/").some(
    (segment) => segment.startsWith(".") || segment === "node_modules" || segment === "AGENTS.md"
  );
}
function fileExtension(path) {
  return path.split(".").pop()?.toLowerCase() ?? "";
}
function deliverableMimeType(path) {
  const extension = fileExtension(path);
  return deliverableTypes[extension] ?? (hasDeliverableRoot(path) ? textDeliverableTypes[extension] : void 0);
}
function hasDeliverableRoot(path) {
  return ["artifacts", "deliverables", "output", "outputs"].includes(
    path.split("/")[0]
  );
}
const deliverableTypes = {
  aac: "audio/aac",
  avif: "image/avif",
  flac: "audio/flac",
  gif: "image/gif",
  htm: "text/html",
  html: "text/html",
  jpeg: "image/jpeg",
  jpg: "image/jpeg",
  json: "application/json",
  m4a: "audio/mp4",
  m4v: "video/x-m4v",
  md: "text/markdown",
  mermaid: "text/x-mermaid",
  mmd: "text/x-mermaid",
  mov: "video/quicktime",
  mp3: "audio/mpeg",
  mp4: "video/mp4",
  oga: "audio/ogg",
  ogg: "audio/ogg",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  pdf: "application/pdf",
  png: "image/png",
  ppt: "application/vnd.ms-powerpoint",
  pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  svg: "image/svg+xml",
  wav: "audio/wav",
  webm: "video/webm",
  webp: "image/webp",
  xls: "application/vnd.ms-excel",
  xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
};
const textDeliverableTypes = {
  cjs: "text/javascript",
  css: "text/css",
  csv: "text/csv",
  go: "text/x-go",
  js: "text/javascript",
  jsx: "text/javascript",
  json: "application/json",
  md: "text/markdown",
  mjs: "text/javascript",
  py: "text/x-python",
  rs: "text/x-rust",
  scss: "text/x-scss",
  sh: "text/x-shellscript",
  toml: "text/plain",
  ts: "text/typescript",
  tsx: "text/typescript",
  txt: "text/plain",
  yaml: "text/yaml",
  yml: "text/yaml"
};
function artifactActionError(cause) {
  return cause instanceof Error ? cause.message : String(cause);
}
function useArtifactRepresentationUrls(item, runtime, sessionId, representationIds) {
  const uniqueIds = [...new Set(representationIds.filter(Boolean))];
  const requestedRepresentations = uniqueIds.map((representationId) => {
    const representation = item.representations.find(
      (candidate) => candidate.representationId === representationId
    );
    return {
      mediaType: representation?.mediaType ?? "",
      representationId,
      revision: representation?.revision.toString() ?? "",
      status: representation?.status ?? ""
    };
  });
  const requestKey = JSON.stringify({
    artifactId: item.artifactId,
    artifactRevision: item.revision.toString(),
    artifactStatus: item.status,
    representations: requestedRepresentations
  });
  const [states, setStates] = useState(() => loadingStates(uniqueIds));
  useEffect(() => {
    setStates(loadingStates(uniqueIds));
    if (uniqueIds.length === 0) return;
    if (item.status === "failed") {
      setStates(errorStates(uniqueIds, "Artifact generation failed"));
      return;
    }
    if (!runtime.loadArtifact) {
      setStates(errorStates(uniqueIds, "Artifact loading is unavailable"));
      return;
    }
    const unauthorized = uniqueIds.filter(
      (representationId) => !artifactActionAllowed(item, "artifact.download", representationId)
    );
    if (unauthorized.length > 0) {
      setStates((current) => ({
        ...current,
        ...errorStates(unauthorized, "artifact_download_not_authorized")
      }));
    }
    let active = true;
    const urls = /* @__PURE__ */ new Set();
    for (const representationId of uniqueIds) {
      if (unauthorized.includes(representationId)) continue;
      void runtime.loadArtifact(sessionId, item.artifactId, representationId).then((blob) => {
        if (!active) return;
        const url = URL.createObjectURL(blob);
        urls.add(url);
        const representation = requestedRepresentations.find(
          (candidate) => candidate.representationId === representationId
        );
        setStates((current) => ({
          ...current,
          [representationId]: {
            status: "ready",
            mimeType: representation?.mediaType || blob.type,
            url
          }
        }));
      }).catch((cause) => {
        if (!active) return;
        setStates((current) => ({
          ...current,
          [representationId]: {
            status: "error",
            message: cause instanceof Error ? cause.message : String(cause)
          }
        }));
      });
    }
    return () => {
      active = false;
      urls.forEach((url) => URL.revokeObjectURL(url));
    };
  }, [requestKey, runtime, sessionId]);
  return states;
}
function loadingStates(representationIds) {
  return Object.fromEntries(
    representationIds.map((representationId) => [
      representationId,
      { status: "loading" }
    ])
  );
}
function errorStates(representationIds, message) {
  return Object.fromEntries(
    representationIds.map((representationId) => [
      representationId,
      { status: "error", message }
    ])
  );
}
function ArtifactViewerLoading({
  filename,
  fill = false
}) {
  return /* @__PURE__ */ jsxs(
    "article",
    {
      className: fill ? "flex min-h-0 flex-1 items-center justify-center rounded-md border border-border bg-muted/30 px-3 py-3 text-sm text-muted-foreground" : "rounded-md border border-border bg-muted/30 px-3 py-3 text-sm text-muted-foreground",
      role: "status",
      children: [
        "正在加载 ",
        filename
      ]
    }
  );
}
function ArtifactViewerError({
  filename,
  message
}) {
  return /* @__PURE__ */ jsx(ArtifactError, { filename, message });
}
const MODES = [
  { icon: Image, label: "查看源图", value: "source" },
  { icon: Images, label: "查看结果图", value: "result" },
  { icon: Columns2, label: "并排比较", value: "side-by-side" },
  { icon: SlidersHorizontal, label: "滑块比较", value: "slider" }
];
function ImageComparisonViewer({
  defaultMode = "source",
  fill = false,
  result,
  source
}) {
  const [mode, setMode] = useState(defaultMode);
  const [sliderPosition, setSliderPosition] = useState(50);
  return /* @__PURE__ */ jsxs(
    "section",
    {
      className: fill ? "flex min-h-0 flex-1 flex-col gap-3" : "space-y-3",
      children: [
        /* @__PURE__ */ jsx(
          "div",
          {
            "aria-label": "图片比较模式",
            className: "flex shrink-0 flex-wrap items-center gap-1 rounded-md border border-border bg-muted/30 p-1",
            role: "group",
            children: MODES.map((option) => /* @__PURE__ */ jsx(
              ModeButton,
              {
                active: mode === option.value,
                icon: option.icon,
                label: option.label,
                onClick: () => setMode(option.value)
              },
              option.value
            ))
          }
        ),
        mode === "source" && /* @__PURE__ */ jsx(ImageFrame, { fill, image: source }),
        mode === "result" && /* @__PURE__ */ jsx(ImageFrame, { fill, image: result }),
        mode === "side-by-side" && /* @__PURE__ */ jsxs(
          "div",
          {
            className: fill ? "grid min-h-0 min-w-0 flex-1 grid-rows-1 gap-3 sm:grid-cols-2" : "grid min-w-0 gap-3 sm:grid-cols-2",
            children: [
              /* @__PURE__ */ jsx(ImageFrame, { fill, image: source }),
              /* @__PURE__ */ jsx(ImageFrame, { fill, image: result })
            ]
          }
        ),
        mode === "slider" && /* @__PURE__ */ jsxs("div", { className: fill ? "flex min-h-0 flex-1 flex-col gap-2" : "space-y-2", children: [
          /* @__PURE__ */ jsxs(
            "div",
            {
              className: fill ? "relative min-h-0 w-full flex-1 overflow-hidden rounded-md border border-border bg-muted" : "relative aspect-video w-full overflow-hidden rounded-md border border-border bg-muted",
              "data-testid": "image-comparison-slider-viewport",
              children: [
                /* @__PURE__ */ jsx(ComparisonImageElement, { image: source }),
                /* @__PURE__ */ jsx(
                  "div",
                  {
                    className: "absolute inset-0 overflow-hidden",
                    "data-testid": "image-comparison-result-layer",
                    style: {
                      clipPath: `inset(0 ${100 - sliderPosition}% 0 0)`
                    },
                    children: /* @__PURE__ */ jsx(ComparisonImageElement, { image: result })
                  }
                ),
                /* @__PURE__ */ jsx(
                  "div",
                  {
                    "aria-hidden": "true",
                    className: "pointer-events-none absolute inset-y-0 w-px bg-primary",
                    style: { left: `${sliderPosition}%` }
                  }
                )
              ]
            }
          ),
          /* @__PURE__ */ jsx(
            "input",
            {
              "aria-label": "比较位置",
              className: "h-11 w-full shrink-0 accent-primary",
              max: 100,
              min: 0,
              onChange: (event) => setSliderPosition(Number(event.target.value)),
              type: "range",
              value: sliderPosition
            }
          )
        ] })
      ]
    }
  );
}
function ModeButton({
  active,
  icon: Icon,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsxs(
    "button",
    {
      "aria-pressed": active,
      className: `inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring ${active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`,
      onClick,
      type: "button",
      children: [
        /* @__PURE__ */ jsx(Icon, { className: "size-3.5 shrink-0" }),
        label
      ]
    }
  );
}
function ImageFrame({
  fill = false,
  image
}) {
  return /* @__PURE__ */ jsx(
    "div",
    {
      className: fill ? "flex min-h-0 min-w-0 flex-1 items-center justify-center overflow-hidden rounded-md border border-border bg-muted" : "flex min-h-56 min-w-0 items-center justify-center overflow-hidden rounded-md border border-border bg-muted",
      children: /* @__PURE__ */ jsx(ComparisonImageElement, { image })
    }
  );
}
function ComparisonImageElement({ image }) {
  return /* @__PURE__ */ jsx(
    "img",
    {
      alt: image.alt,
      className: "size-full object-contain",
      draggable: false,
      src: image.src
    }
  );
}
function ImageEditComposer({
  actionSchemaVersion,
  artifactId,
  baseRevision,
  disabled = false,
  normalizedRegion,
  onSubmit,
  representationId,
  sourceDimensions
}) {
  const [instruction, setInstruction] = useState("");
  const submittedInstruction = instruction.trim();
  return /* @__PURE__ */ jsxs(
    "form",
    {
      className: "shrink-0 space-y-3 rounded-md border border-border bg-card p-3",
      onSubmit: (event) => {
        event.preventDefault();
        if (!submittedInstruction || disabled) return;
        onSubmit({
          actionSchemaVersion,
          actionType: "image.edit",
          artifactId,
          baseRevision,
          commandId: crypto.randomUUID(),
          payload: {
            instruction: submittedInstruction,
            ...normalizedRegion ? { normalizedRegion } : {},
            sourceDimensions
          },
          ...representationId ? { representationId } : {}
        });
      },
      children: [
        /* @__PURE__ */ jsxs("label", { className: "block space-y-1.5 text-sm font-medium", children: [
          "编辑说明",
          /* @__PURE__ */ jsx(
            "textarea",
            {
              "aria-label": "编辑说明",
              className: "min-h-24 w-full resize-y rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring",
              disabled,
              onChange: (event) => setInstruction(event.target.value),
              placeholder: "描述希望对图片进行的修改",
              value: instruction
            }
          )
        ] }),
        /* @__PURE__ */ jsx("div", { className: "flex justify-end", children: /* @__PURE__ */ jsxs(
          "button",
          {
            className: "inline-flex h-11 items-center gap-1.5 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground outline-none hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
            disabled: disabled || !submittedInstruction,
            type: "submit",
            children: [
              /* @__PURE__ */ jsx(Send, { className: "size-4" }),
              "提交编辑"
            ]
          }
        ) })
      ]
    }
  );
}
function ImageEditArtifactViewer({
  filename,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const manifest = item.manifest;
  if (manifest?.kind !== "image_edit") {
    return imageEditError(filename, presentation, text, "image_edit_manifest_missing");
  }
  return /* @__PURE__ */ jsx(
    ImageEditArtifactContent,
    {
      filename,
      fill,
      item,
      manifest,
      presentation,
      runtime,
      sessionId
    }
  );
}
function ImageEditArtifactContent({
  filename,
  fill = false,
  item,
  manifest,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const resultRepresentationId = selectResultRepresentationId(item, manifest);
  const representationIds = new Set(
    item.representations.map((representation) => representation.representationId)
  );
  if (!representationIds.has(manifest.sourceRepresentationId)) {
    return imageEditError(
      filename,
      presentation,
      text,
      "image_edit_source_representation_missing"
    );
  }
  if (manifest.resultRepresentationId && !representationIds.has(manifest.resultRepresentationId)) {
    return imageEditError(
      filename,
      presentation,
      text,
      "image_edit_result_representation_missing"
    );
  }
  const requestedRepresentationIds = [
    manifest.sourceRepresentationId,
    ...resultRepresentationId ? [resultRepresentationId] : []
  ];
  const resources = useArtifactRepresentationUrls(
    item,
    runtime,
    sessionId,
    requestedRepresentationIds
  );
  const [regionIndex, setRegionIndex] = useState(0);
  const [actionError, setActionError] = useState(null);
  const [actionPending, setActionPending] = useState(false);
  const source = resources[manifest.sourceRepresentationId];
  const result = resultRepresentationId ? resources[resultRepresentationId] : void 0;
  const error = requestedRepresentationIds.map((id) => resources[id]).find((state) => state?.status === "error");
  const loading = requestedRepresentationIds.some(
    (id) => resources[id]?.status !== "ready"
  );
  if (error?.status === "error") {
    return imageEditError(filename, presentation, text, error.message);
  }
  if (loading || source?.status !== "ready") {
    return /* @__PURE__ */ jsx(ArtifactViewerLoading, { fill, filename });
  }
  const canEdit = artifactActionAllowed(
    item,
    "image.edit",
    manifest.sourceRepresentationId
  ) && runtime.executeArtifactAction !== void 0;
  const normalizedRegion = manifest.regions[regionIndex];
  return /* @__PURE__ */ jsxs("section", { className: "flex min-h-0 flex-1 flex-col gap-3", children: [
    result?.status === "ready" ? /* @__PURE__ */ jsx(
      ImageComparisonViewer,
      {
        fill,
        result: { alt: "结果图", src: result.url },
        source: { alt: "源图", src: source.url }
      }
    ) : /* @__PURE__ */ jsx(
      "img",
      {
        alt: "源图",
        className: "min-h-0 w-full flex-1 rounded-md border border-border bg-muted object-contain",
        src: source.url
      }
    ),
    manifest.regions.length > 1 && /* @__PURE__ */ jsxs("label", { className: "flex shrink-0 items-center gap-2 text-sm", children: [
      /* @__PURE__ */ jsx("span", { children: "编辑区域" }),
      /* @__PURE__ */ jsx(
        "select",
        {
          "aria-label": "选择编辑区域",
          className: "h-11 rounded-md border border-input bg-background px-2",
          onChange: (event) => setRegionIndex(Number(event.target.value)),
          value: regionIndex,
          children: manifest.regions.map((_region, index) => /* @__PURE__ */ jsxs("option", { value: index, children: [
            "区域 ",
            index + 1
          ] }, index))
        }
      )
    ] }),
    !canEdit && /* @__PURE__ */ jsx("div", { className: "text-xs text-muted-foreground", role: "status", children: "当前会话没有图片编辑动作权限" }),
    actionPending && /* @__PURE__ */ jsx("div", { className: "text-xs text-muted-foreground", role: "status", children: "正在提交图片编辑" }),
    actionError && /* @__PURE__ */ jsx("div", { className: "text-xs text-destructive", role: "alert", children: actionError }),
    /* @__PURE__ */ jsx(
      ImageEditComposer,
      {
        actionSchemaVersion: item.schemaVersion,
        artifactId: item.artifactId,
        baseRevision: item.revision,
        disabled: !canEdit || actionPending,
        normalizedRegion,
        onSubmit: (command) => {
          setActionError(null);
          setActionPending(true);
          void runtime.executeArtifactAction(sessionId, command).catch((cause) => setActionError(artifactActionError(cause))).finally(() => setActionPending(false));
        },
        representationId: manifest.sourceRepresentationId,
        sourceDimensions: manifest.sourceDimensions
      }
    )
  ] });
}
function selectResultRepresentationId(item, manifest) {
  if (manifest.resultRepresentationId) return manifest.resultRepresentationId;
  const representationIds = new Set(
    item.representations.map((representation) => representation.representationId)
  );
  return manifest.candidateRepresentationIds.find(
    (id) => representationIds.has(id)
  );
}
function imageEditError(filename, presentation, text, developerMessage) {
  return /* @__PURE__ */ jsx(
    ArtifactViewerError,
    {
      filename,
      message: presentation === "user" ? text.artifact.loadFailed : developerMessage
    }
  );
}
const PRESENTATION_GRANTS = {
  exportPresentation: "presentation.export",
  regenerateSlide: "presentation.regenerate_slide",
  reorderSlide: "presentation.reorder_slide",
  replaceSlide: "presentation.replace_slide",
  selectVersion: "presentation.select_version"
};
function PresentationActionToolbar({
  currentIndex,
  grants,
  onExport,
  onMove,
  onRegenerate,
  onReplace,
  slideCount
}) {
  const hasSlide = currentIndex >= 0;
  const granted = new Set(grants);
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-label": "演示文稿操作",
      className: "flex min-w-0 flex-wrap items-center gap-1",
      role: "toolbar",
      children: [
        /* @__PURE__ */ jsx(
          ActionButton,
          {
            disabled: !hasSlide || !granted.has(PRESENTATION_GRANTS.regenerateSlide),
            icon: RefreshCw,
            label: "重新生成当前页",
            onClick: onRegenerate
          }
        ),
        /* @__PURE__ */ jsx(
          ActionButton,
          {
            disabled: !hasSlide || !granted.has(PRESENTATION_GRANTS.replaceSlide),
            icon: Replace,
            label: "替换当前页",
            onClick: onReplace
          }
        ),
        /* @__PURE__ */ jsx(
          ActionButton,
          {
            disabled: currentIndex <= 0 || !granted.has(PRESENTATION_GRANTS.reorderSlide),
            icon: ArrowUp,
            label: "上移当前页",
            onClick: () => onMove(currentIndex - 1)
          }
        ),
        /* @__PURE__ */ jsx(
          ActionButton,
          {
            disabled: currentIndex < 0 || currentIndex >= slideCount - 1 || !granted.has(PRESENTATION_GRANTS.reorderSlide),
            icon: ArrowDown,
            label: "下移当前页",
            onClick: () => onMove(currentIndex + 1)
          }
        ),
        /* @__PURE__ */ jsx(
          ActionButton,
          {
            disabled: !hasSlide || !granted.has(PRESENTATION_GRANTS.exportPresentation),
            icon: Download,
            label: "导出演示文稿",
            onClick: onExport
          }
        )
      ]
    }
  );
}
function ActionButton({
  disabled,
  icon: Icon,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": label,
      className: "inline-flex size-9 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40",
      disabled,
      onClick,
      title: label,
      type: "button",
      children: /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: "size-4" })
    }
  );
}
function createPresentationAction(context, actionType, payload) {
  return {
    actionSchemaVersion: context.actionSchemaVersion,
    actionType,
    artifactId: context.artifactId,
    baseRevision: context.baseRevision,
    commandId: crypto.randomUUID(),
    payload,
    ...context.representationId ? { representationId: context.representationId } : {}
  };
}
function orderPresentationSlides(slides) {
  return [...slides].sort(
    (left, right) => left.position - right.position || left.slideId.localeCompare(right.slideId)
  );
}
function presentationSlideLabel(slide, index) {
  return slide.title?.trim() || `第 ${index + 1} 页`;
}
function PresentationViewControls({
  canFullscreen,
  disabled,
  fitToWindow,
  onFitToWindow,
  onRequestFullscreen,
  onZoomChange,
  zoomPercent
}) {
  return /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 flex-wrap items-center gap-2", children: [
    /* @__PURE__ */ jsxs(
      "button",
      {
        "aria-pressed": fitToWindow,
        className: "inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        disabled,
        onClick: onFitToWindow,
        type: "button",
        children: [
          /* @__PURE__ */ jsx(Scan, { "aria-hidden": "true", className: "size-3.5" }),
          "适应窗口"
        ]
      }
    ),
    /* @__PURE__ */ jsxs("label", { className: "flex h-9 min-w-44 items-center gap-2 text-xs text-muted-foreground", children: [
      /* @__PURE__ */ jsx(ZoomIn, { "aria-hidden": "true", className: "size-3.5 shrink-0" }),
      /* @__PURE__ */ jsx(
        "input",
        {
          "aria-label": "缩放比例",
          className: "min-w-24 flex-1 accent-primary",
          disabled,
          max: 200,
          min: 50,
          onChange: (event) => onZoomChange(Number(event.currentTarget.value)),
          step: 10,
          type: "range",
          value: zoomPercent
        }
      ),
      /* @__PURE__ */ jsxs("span", { className: "w-10 text-right tabular-nums", children: [
        zoomPercent,
        "%"
      ] })
    ] }),
    /* @__PURE__ */ jsxs(
      "button",
      {
        className: "inline-flex size-9 items-center justify-center rounded-md border border-border outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        disabled: disabled || !canFullscreen,
        onClick: onRequestFullscreen,
        title: canFullscreen ? "全屏查看" : "当前环境不支持全屏",
        type: "button",
        children: [
          /* @__PURE__ */ jsx(Maximize2, { "aria-hidden": "true", className: "size-4" }),
          /* @__PURE__ */ jsx("span", { className: "sr-only", children: "全屏查看" })
        ]
      }
    )
  ] });
}
function PresentationStage({
  currentIndex,
  currentSlide,
  onRequestFullscreen,
  slideCount
}) {
  const [zoomPercent, setZoomPercent] = useState(100);
  const [fitToWindow, setFitToWindow] = useState(true);
  const stageRef = useRef(null);
  const fullscreenSupported = Boolean(onRequestFullscreen) || typeof HTMLElement !== "undefined" && typeof HTMLElement.prototype.requestFullscreen === "function";
  return /* @__PURE__ */ jsxs("main", { className: "flex min-h-0 min-w-0 flex-1 flex-col p-3", children: [
    /* @__PURE__ */ jsxs("div", { className: "mb-2 flex min-w-0 flex-wrap items-center justify-between gap-2", children: [
      /* @__PURE__ */ jsx("span", { className: "text-xs font-medium tabular-nums", children: currentSlide ? `第 ${currentIndex + 1} / ${slideCount} 页` : "暂无页面" }),
      /* @__PURE__ */ jsx(
        PresentationViewControls,
        {
          canFullscreen: fullscreenSupported,
          disabled: !currentSlide,
          fitToWindow,
          onFitToWindow: () => {
            setFitToWindow(true);
            setZoomPercent(100);
          },
          onRequestFullscreen: () => {
            if (onRequestFullscreen) {
              void onRequestFullscreen();
              return;
            }
            void stageRef.current?.requestFullscreen();
          },
          onZoomChange: (value) => {
            setFitToWindow(false);
            setZoomPercent(value);
          },
          zoomPercent
        }
      )
    ] }),
    /* @__PURE__ */ jsx(
      "div",
      {
        className: "flex min-h-0 min-w-0 w-full flex-1 items-center justify-center overflow-auto rounded-md border border-border bg-muted/30",
        ref: stageRef,
        children: currentSlide ? /* @__PURE__ */ jsx(
          "img",
          {
            alt: `第 ${currentIndex + 1} 页：${presentationSlideLabel(
              currentSlide,
              currentIndex
            )}`,
            className: "max-h-full max-w-full object-contain transition-transform motion-reduce:transition-none",
            "data-testid": "presentation-slide-image",
            draggable: false,
            src: currentSlide.imageSrc,
            style: {
              transform: `scale(${zoomPercent / 100})`,
              transformOrigin: "center"
            }
          }
        ) : /* @__PURE__ */ jsx("span", { className: "text-sm text-muted-foreground", children: "暂无可预览页面" })
      }
    )
  ] });
}
function PresentationThumbnailRail({
  activeSlideId,
  onSelect,
  slides
}) {
  return /* @__PURE__ */ jsx(
    "nav",
    {
      "aria-label": "演示文稿页面",
      className: "flex min-w-0 shrink-0 gap-2 overflow-x-auto border-b border-border p-2 lg:h-full lg:min-h-0 lg:w-40 lg:flex-col lg:overflow-y-auto lg:border-b-0 lg:border-r",
      "data-testid": "presentation-thumbnail-rail",
      children: slides.map((slide, index) => {
        const label = presentationSlideLabel(slide, index);
        const active = slide.slideId === activeSlideId;
        return /* @__PURE__ */ jsxs(
          "button",
          {
            "aria-current": active ? "page" : void 0,
            "aria-label": `转到第 ${index + 1} 页：${label}`,
            className: `w-28 shrink-0 rounded-md border p-1 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring lg:w-full ${active ? "border-primary bg-primary/5" : "border-border bg-background hover:bg-muted"}`,
            onClick: () => onSelect(slide.slideId),
            type: "button",
            children: [
              /* @__PURE__ */ jsx("div", { className: "aspect-video overflow-hidden rounded-sm bg-muted", children: /* @__PURE__ */ jsx(
                "img",
                {
                  alt: "",
                  className: "size-full object-cover",
                  draggable: false,
                  src: slide.thumbnailSrc ?? slide.imageSrc
                }
              ) }),
              /* @__PURE__ */ jsxs("div", { className: "mt-1 flex min-w-0 items-center gap-1 text-xs", children: [
                /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: index + 1 }),
                /* @__PURE__ */ jsx("span", { className: "truncate font-medium", title: label, children: label })
              ] })
            ]
          },
          slide.slideId
        );
      })
    }
  );
}
function PresentationArtifactViewer({
  actionSchemaVersion,
  artifactId,
  baseRevision,
  grants,
  initialSlideId,
  onAction,
  onRequestFullscreen,
  onSelectVersion,
  representationId,
  selectedVersionId,
  slides,
  versions
}) {
  const orderedSlides = useMemo(
    () => orderPresentationSlides(slides),
    [slides]
  );
  const [currentSlideId, setCurrentSlideId] = useState(
    () => orderedSlides.find((slide) => slide.slideId === initialSlideId)?.slideId ?? orderedSlides[0]?.slideId
  );
  useEffect(() => {
    if (!orderedSlides.some((slide) => slide.slideId === currentSlideId)) {
      setCurrentSlideId(
        orderedSlides.find((slide) => slide.slideId === initialSlideId)?.slideId ?? orderedSlides[0]?.slideId
      );
    }
  }, [currentSlideId, initialSlideId, orderedSlides]);
  const currentIndex = orderedSlides.findIndex(
    (slide) => slide.slideId === currentSlideId
  );
  const currentSlide = orderedSlides[currentIndex];
  const emitAction = (actionType, payload) => {
    if (!currentSlide) return;
    onAction(
      createPresentationAction(
        {
          actionSchemaVersion,
          artifactId,
          baseRevision,
          representationId: actionType === PRESENTATION_GRANTS.exportPresentation ? representationId : currentSlide.representationId ?? representationId
        },
        actionType,
        payload
      )
    );
  };
  return /* @__PURE__ */ jsxs("section", { className: "flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-background text-foreground", children: [
    /* @__PURE__ */ jsxs("header", { className: "flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-3 border-b border-border px-3 py-2.5", children: [
      /* @__PURE__ */ jsxs("div", { className: "min-w-0", children: [
        /* @__PURE__ */ jsx("div", { className: "text-sm font-semibold", children: "演示文稿" }),
        /* @__PURE__ */ jsxs("div", { className: "text-xs text-muted-foreground", children: [
          "修订 ",
          baseRevision.toString()
        ] })
      ] }),
      /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 flex-wrap items-center gap-3", children: [
        /* @__PURE__ */ jsxs("label", { className: "flex items-center gap-2 text-xs text-muted-foreground", children: [
          /* @__PURE__ */ jsx("span", { children: "版本" }),
          /* @__PURE__ */ jsx(
            "select",
            {
              "aria-label": "选择演示文稿版本",
              className: "h-9 max-w-44 rounded-md border border-input bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
              disabled: !onSelectVersion || versions.length < 2,
              onChange: (event) => onSelectVersion?.(event.currentTarget.value),
              value: selectedVersionId,
              children: versions.map((version) => /* @__PURE__ */ jsx("option", { value: version.id, children: version.label }, version.id))
            }
          )
        ] }),
        /* @__PURE__ */ jsx(
          PresentationActionToolbar,
          {
            currentIndex,
            grants,
            onExport: () => currentSlide && emitAction(PRESENTATION_GRANTS.exportPresentation, {
              format: "pptx",
              slideId: currentSlide.slideId
            }),
            onMove: (targetIndex) => currentSlide && emitAction(PRESENTATION_GRANTS.reorderSlide, {
              slideId: currentSlide.slideId,
              targetIndex
            }),
            onRegenerate: () => currentSlide && emitAction(PRESENTATION_GRANTS.regenerateSlide, {
              slideId: currentSlide.slideId
            }),
            onReplace: () => currentSlide && emitAction(PRESENTATION_GRANTS.replaceSlide, {
              slideId: currentSlide.slideId
            }),
            slideCount: orderedSlides.length
          }
        )
      ] })
    ] }),
    /* @__PURE__ */ jsxs("div", { className: "flex min-h-0 flex-1 flex-col lg:grid lg:grid-cols-[10rem_minmax(0,1fr)_16rem] lg:grid-rows-1", children: [
      /* @__PURE__ */ jsx(
        PresentationThumbnailRail,
        {
          activeSlideId: currentSlide?.slideId,
          onSelect: setCurrentSlideId,
          slides: orderedSlides
        }
      ),
      /* @__PURE__ */ jsx(
        PresentationStage,
        {
          currentIndex,
          currentSlide,
          onRequestFullscreen,
          slideCount: orderedSlides.length
        }
      ),
      /* @__PURE__ */ jsxs("aside", { className: "max-h-[40%] min-h-0 min-w-0 overflow-auto border-t border-border p-3 lg:max-h-none lg:border-l lg:border-t-0", children: [
        /* @__PURE__ */ jsxs("h2", { className: "flex items-center gap-2 text-sm font-semibold", children: [
          /* @__PURE__ */ jsx(StickyNote, { "aria-hidden": "true", className: "size-4" }),
          "讲者备注"
        ] }),
        /* @__PURE__ */ jsx("div", { className: "mt-3 whitespace-pre-wrap text-sm leading-6 text-muted-foreground", children: currentSlide?.notes?.trim() || "暂无讲者备注" })
      ] })
    ] })
  ] });
}
function presentationManifestSlides(manifest, representations) {
  const representationMap = new Map(
    representations.map((representation) => [
      representation.representationId,
      representation
    ])
  );
  return manifest.slides.flatMap((slide) => {
    if (!slide.pageRepresentationId) return [];
    const page = representationMap.get(slide.pageRepresentationId);
    if (page?.status !== "ready" || !isSafeArtifactImage(page.mediaType)) {
      return [];
    }
    const thumbnail = slide.thumbnailRepresentationId ? representationMap.get(slide.thumbnailRepresentationId) : void 0;
    return [{
      ...slide,
      pageRepresentationId: slide.pageRepresentationId,
      thumbnailRepresentationId: thumbnail?.status === "ready" && isSafeArtifactImage(thumbnail.mediaType) ? thumbnail.representationId : void 0
    }];
  });
}
function presentationRepresentationIds(slides) {
  return [
    ...new Set(
      slides.flatMap((slide) => [
        slide.pageRepresentationId,
        ...slide.thumbnailRepresentationId ? [slide.thumbnailRepresentationId] : []
      ])
    )
  ];
}
function PresentationManifestArtifactViewer({
  filename,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const manifest = item.manifest;
  if (manifest?.kind !== "presentation") {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.artifact.loadFailed : "presentation_manifest_missing"
      }
    );
  }
  return /* @__PURE__ */ jsx(
    PresentationManifestArtifactContent,
    {
      filename,
      fill,
      item,
      manifest,
      presentation,
      runtime,
      sessionId
    }
  );
}
function PresentationManifestArtifactContent({
  filename,
  fill = false,
  item,
  manifest,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const renderableSlides = presentationManifestSlides(
    manifest,
    item.representations
  );
  const representationIds = presentationRepresentationIds(renderableSlides);
  const resources = useArtifactRepresentationUrls(
    item,
    runtime,
    sessionId,
    representationIds
  );
  const [actionError, setActionError] = useState(null);
  const [actionPending, setActionPending] = useState(false);
  const error = representationIds.map((id) => resources[id]).find((state) => state?.status === "error");
  if (error?.status === "error") {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.artifact.loadFailed : error.message
      }
    );
  }
  if (representationIds.some((id) => resources[id]?.status !== "ready")) {
    return /* @__PURE__ */ jsx(ArtifactViewerLoading, { fill, filename });
  }
  const slides = renderableSlides.flatMap((slide) => {
    const page = resources[slide.pageRepresentationId];
    const thumbnail = slide.thumbnailRepresentationId ? resources[slide.thumbnailRepresentationId] : void 0;
    if (page?.status !== "ready") return [];
    return [{
      imageSrc: page.url,
      notes: slide.notes,
      position: slide.position,
      representationId: slide.pageRepresentationId,
      slideId: slide.slideId,
      thumbnailSrc: thumbnail?.status === "ready" ? thumbnail.url : void 0,
      title: slide.title
    }];
  });
  const canExecute = runtime.executeArtifactAction !== void 0;
  const actionRepresentationIds = [
    ...item.selectedRepresentationId ? [item.selectedRepresentationId] : [],
    ...slides.flatMap(
      (slide) => slide.representationId ? [slide.representationId] : []
    )
  ];
  const grants = canExecute && !actionPending ? item.actions.filter(
    (action) => isPresentationGrant(action) && actionRepresentationIds.some(
      (representationId) => artifactActionAllowed(item, action, representationId)
    )
  ) : [];
  const selectedVersionId = manifest.selectedVersionId ?? manifest.versions[0]?.id ?? "";
  const canSelectVersion = canExecute && !actionPending && artifactActionAllowed(
    item,
    PRESENTATION_GRANTS.selectVersion,
    item.selectedRepresentationId ?? void 0
  );
  const execute = (command) => {
    setActionError(null);
    setActionPending(true);
    void runtime.executeArtifactAction(sessionId, command).catch((cause) => setActionError(artifactActionError(cause))).finally(() => setActionPending(false));
  };
  return /* @__PURE__ */ jsxs("section", { className: "flex min-h-0 flex-1 flex-col overflow-hidden", children: [
    actionPending && /* @__PURE__ */ jsx("div", { className: "shrink-0 text-xs text-muted-foreground", role: "status", children: "正在提交演示文稿操作" }),
    actionError && /* @__PURE__ */ jsx("div", { className: "shrink-0 text-xs text-destructive", role: "alert", children: actionError }),
    /* @__PURE__ */ jsx(
      PresentationArtifactViewer,
      {
        actionSchemaVersion: item.schemaVersion,
        artifactId: item.artifactId,
        baseRevision: item.revision,
        grants,
        onAction: execute,
        onSelectVersion: canSelectVersion ? (versionId) => execute({
          actionSchemaVersion: item.schemaVersion,
          actionType: PRESENTATION_GRANTS.selectVersion,
          artifactId: item.artifactId,
          baseRevision: item.revision,
          commandId: crypto.randomUUID(),
          payload: { versionId },
          ...item.selectedRepresentationId ? { representationId: item.selectedRepresentationId } : {}
        }) : void 0,
        representationId: item.selectedRepresentationId ?? void 0,
        selectedVersionId,
        slides,
        versions: manifest.versions
      }
    )
  ] });
}
function isPresentationGrant(action) {
  return Object.values(PRESENTATION_GRANTS).includes(
    action
  );
}
function VideoPlaybackSurface({
  filename,
  posterSrc,
  src
}) {
  const text = useAgentWorkspaceText().artifact;
  const surfaceRef = useRef(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [fullscreenError, setFullscreenError] = useState(false);
  const fullscreenSupported = typeof HTMLElement !== "undefined" && typeof HTMLElement.prototype.requestFullscreen === "function";
  useEffect(() => {
    const syncFullscreen = () => {
      setFullscreen(document.fullscreenElement === surfaceRef.current);
    };
    document.addEventListener("fullscreenchange", syncFullscreen);
    return () => document.removeEventListener("fullscreenchange", syncFullscreen);
  }, []);
  const toggleFullscreen = async () => {
    setFullscreenError(false);
    try {
      if (document.fullscreenElement === surfaceRef.current) {
        await document.exitFullscreen();
      } else {
        await surfaceRef.current?.requestFullscreen();
      }
    } catch {
      setFullscreenError(true);
    }
  };
  return /* @__PURE__ */ jsxs(
    "div",
    {
      className: `relative flex min-h-0 flex-1 flex-col bg-black ${fullscreen ? "h-full items-center justify-center" : ""}`,
      ref: surfaceRef,
      children: [
        /* @__PURE__ */ jsx(
          "video",
          {
            "aria-label": `视频预览：${filename}`,
            className: `w-full object-contain ${fullscreen ? "h-full max-h-none" : "min-h-0 flex-1"}`,
            controls: true,
            playsInline: true,
            poster: posterSrc,
            preload: "metadata",
            src,
            children: text.videoUnsupported
          }
        ),
        fullscreenSupported && /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": fullscreen ? text.exitFullscreen : text.fullscreenVideo,
            className: "absolute right-3 top-3 inline-flex size-11 items-center justify-center rounded-md bg-black/70 text-white outline-none hover:bg-black/85 focus-visible:ring-2 focus-visible:ring-white",
            onClick: () => void toggleFullscreen(),
            title: fullscreen ? text.exitFullscreen : text.fullscreenVideo,
            type: "button",
            children: fullscreen ? /* @__PURE__ */ jsx(Minimize2, { "aria-hidden": "true", className: "size-4" }) : /* @__PURE__ */ jsx(Maximize2, { "aria-hidden": "true", className: "size-4" })
          }
        ),
        fullscreenError && /* @__PURE__ */ jsx(
          "div",
          {
            className: "absolute inset-x-3 bottom-3 rounded-md bg-black/80 px-3 py-2 text-sm text-white",
            role: "alert",
            children: text.fullscreenUnavailable
          }
        )
      ]
    }
  );
}
const STATUS_TEXT = {
  queued: "视频已排队，等待生成",
  rendering: "正在渲染视频",
  transcoding: "正在转码视频",
  failed: "视频生成失败"
};
function VideoArtifactViewer({
  src,
  filename,
  mimeType,
  posterSrc,
  durationSeconds,
  status,
  technicalMetadata = true,
  progress,
  versions = [],
  selectedVersionId,
  onSelectVersion,
  onDownload
}) {
  const selectedId = selectedVersionId ?? versions[0]?.id ?? "";
  const selectedVersion = versions.find((version) => version.id === selectedId);
  const activeFilename = selectedVersion?.filename?.trim() || filename.trim() || "生成视频";
  const activeSrc = selectedVersion?.src ?? src;
  const activeMimeType = selectedVersion?.mimeType ?? mimeType;
  const activePosterSrc = selectedVersion?.posterSrc ?? posterSrc;
  const activeDuration = selectedVersion?.durationSeconds ?? durationSeconds;
  const canDownload = status === "ready" && Boolean(onDownload);
  return /* @__PURE__ */ jsxs("article", { className: "flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-card", children: [
    status === "ready" ? /* @__PURE__ */ jsx(
      VideoPlaybackSurface,
      {
        filename: activeFilename,
        posterSrc: activePosterSrc,
        src: activeSrc
      },
      `${activeSrc}:${activeMimeType}`
    ) : /* @__PURE__ */ jsx(VideoStatusPanel, { progress, status }),
    /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 shrink-0 flex-wrap items-center gap-3 border-t border-border px-3 py-2.5", children: [
      /* @__PURE__ */ jsxs("div", { className: "min-w-40 flex-1", children: [
        /* @__PURE__ */ jsx("div", { className: "truncate text-sm font-medium", title: activeFilename, children: activeFilename }),
        /* @__PURE__ */ jsxs("div", { className: "flex flex-wrap gap-x-3 text-xs text-muted-foreground", children: [
          technicalMetadata && /* @__PURE__ */ jsx("span", { children: activeMimeType }),
          activeDuration !== void 0 && /* @__PURE__ */ jsxs("span", { children: [
            "时长 ",
            formatDuration(activeDuration)
          ] })
        ] })
      ] }),
      versions.length > 1 && /* @__PURE__ */ jsxs("label", { className: "flex items-center gap-2 text-xs text-muted-foreground", children: [
        /* @__PURE__ */ jsx("span", { children: "版本" }),
        /* @__PURE__ */ jsx(
          "select",
          {
            "aria-label": "选择视频版本",
            className: "h-11 max-w-40 rounded-md border border-input bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
            disabled: !onSelectVersion || versions.length < 2,
            onChange: (event) => onSelectVersion?.(event.currentTarget.value),
            value: selectedId,
            children: versions.map((version, index) => /* @__PURE__ */ jsx("option", { value: version.id, children: technicalMetadata ? version.label ?? `版本 ${index + 1}` : `版本 ${index + 1}` }, version.id))
          }
        )
      ] }),
      /* @__PURE__ */ jsxs(
        "button",
        {
          "aria-label": `下载视频：${activeFilename}`,
          className: "inline-flex h-11 items-center gap-2 rounded-md border border-border px-3 text-sm font-medium text-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          disabled: !canDownload,
          onClick: onDownload,
          type: "button",
          children: [
            /* @__PURE__ */ jsx(Download, { "aria-hidden": "true", className: "size-4" }),
            "下载"
          ]
        }
      )
    ] })
  ] });
}
function VideoStatusPanel({
  progress,
  status
}) {
  const failed = status === "failed";
  const normalizedProgress = progress === void 0 || !Number.isFinite(progress) ? void 0 : Math.round(Math.min(100, Math.max(0, progress)));
  const StatusIcon = failed ? CircleAlert : status === "queued" ? Clock3 : LoaderCircle;
  return /* @__PURE__ */ jsxs(
    "section",
    {
      "aria-live": failed ? "assertive" : "polite",
      className: `flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-6 text-center ${failed ? "bg-destructive/5 text-destructive" : "bg-muted/30"}`,
      role: failed ? "alert" : "status",
      children: [
        /* @__PURE__ */ jsx(
          StatusIcon,
          {
            "aria-hidden": "true",
            className: `size-6 ${failed ? "" : status === "queued" ? "" : "animate-spin motion-reduce:animate-none"}`
          }
        ),
        /* @__PURE__ */ jsx("div", { className: "text-sm font-medium", children: STATUS_TEXT[status] }),
        !failed && /* @__PURE__ */ jsxs("div", { className: "w-full max-w-72", children: [
          /* @__PURE__ */ jsx(
            "div",
            {
              "aria-label": "视频生成进度",
              "aria-valuemax": 100,
              "aria-valuemin": 0,
              "aria-valuenow": normalizedProgress,
              "aria-valuetext": normalizedProgress === void 0 ? "进度未知" : `${normalizedProgress}%`,
              className: "h-2 overflow-hidden rounded-full bg-muted",
              role: "progressbar",
              children: /* @__PURE__ */ jsx(
                "div",
                {
                  className: `h-full rounded-full bg-primary ${normalizedProgress === void 0 ? "w-1/3 animate-pulse motion-reduce:animate-none" : ""}`,
                  style: normalizedProgress === void 0 ? void 0 : { width: `${normalizedProgress}%` }
                }
              )
            }
          ),
          /* @__PURE__ */ jsx("div", { className: "mt-2 text-xs text-muted-foreground", children: normalizedProgress === void 0 ? "正在等待进度更新" : `${normalizedProgress}%` })
        ] })
      ]
    }
  );
}
function formatDuration(durationSeconds) {
  const totalSeconds = Math.max(0, Math.floor(durationSeconds));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor(totalSeconds % 3600 / 60);
  const seconds = totalSeconds % 60;
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}` : `${minutes}:${String(seconds).padStart(2, "0")}`;
}
function PlainVideoArtifactViewer({
  filename,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText().artifact;
  const state = useArtifactBlobUrl(item, runtime, sessionId);
  if (state.status === "idle" || state.status === "loading") {
    return /* @__PURE__ */ jsx(ArtifactViewerLoading, { fill, filename });
  }
  if (state.status === "error") {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.loadFailed : state.message
      }
    );
  }
  return /* @__PURE__ */ jsx(
    VideoArtifactViewer,
    {
      filename,
      mimeType: state.mimeType || item.mimeType || "video/mp4",
      onDownload: () => download$1(state.url, filename),
      src: state.url,
      status: "ready",
      technicalMetadata: presentation === "developer"
    }
  );
}
function download$1(url, filename) {
  const link = document.createElement("a");
  link.download = filename;
  link.href = url;
  link.click();
}
function videoRepresentations(representations, manifest) {
  const ids = new Set(
    [
      manifest.playableRepresentationId,
      manifest.originalRepresentationId,
      ...manifest.derivativeRepresentationIds
    ].filter((value) => Boolean(value))
  );
  return representations.filter(
    (representation) => ids.has(representation.representationId) && representation.status === "ready"
  );
}
function videoPosterRepresentation(representations, manifest) {
  return representations.find(
    (representation) => representation.representationId === manifest.posterRepresentationId && representation.status === "ready" && isSafeArtifactImage(representation.mediaType)
  );
}
function videoVersions(representations, resources) {
  return representations.map((representation, index) => {
    const resource = resources[representation.representationId];
    return {
      id: representation.representationId,
      label: representation.role || `版本 ${index + 1}`,
      filename: representation.filename,
      mimeType: representation.mediaType,
      durationSeconds: representation.durationMillis === void 0 ? void 0 : Number(representation.durationMillis) / 1e3,
      src: resource?.status === "ready" ? resource.url : void 0
    };
  });
}
function VideoManifestArtifactViewer({
  filename,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText().artifact;
  const manifest = item.manifest;
  if (manifest?.kind !== "video") {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.loadFailed : "video_manifest_missing"
      }
    );
  }
  if (manifest.stage === "unknown") {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.loadFailed : "video_stage_unknown"
      }
    );
  }
  return /* @__PURE__ */ jsx(
    VideoManifestArtifactContent,
    {
      filename,
      fill,
      item,
      manifest,
      presentation,
      runtime,
      sessionId,
      stage: manifest.stage
    }
  );
}
function VideoManifestArtifactContent({
  filename,
  fill = false,
  item,
  manifest,
  presentation = "developer",
  runtime,
  sessionId,
  stage
}) {
  const text = useAgentWorkspaceText().artifact;
  const versionRepresentations = useMemo(
    () => videoRepresentations(item.representations, manifest),
    [item.representations, manifest]
  );
  const posterRepresentation = useMemo(
    () => videoPosterRepresentation(item.representations, manifest),
    [item.representations, manifest]
  );
  const versionIds = new Set(
    versionRepresentations.map((value) => value.representationId)
  );
  const initialId = [manifest.playableRepresentationId, manifest.originalRepresentationId].find((id) => Boolean(id && versionIds.has(id))) ?? versionRepresentations[0]?.representationId ?? "";
  const [selectedId, setSelectedId] = useState(initialId);
  useEffect(() => {
    if (!versionRepresentations.some((value) => value.representationId === selectedId)) {
      setSelectedId(initialId);
    }
  }, [initialId, selectedId, versionRepresentations]);
  const representationIds = [
    ...selectedId ? [selectedId] : [],
    ...posterRepresentation ? [posterRepresentation.representationId] : []
  ];
  const resources = useArtifactRepresentationUrls(
    item,
    runtime,
    sessionId,
    representationIds
  );
  const versions = useMemo(
    () => videoVersions(versionRepresentations, resources),
    [resources, versionRepresentations]
  );
  const selected = versions.find((version) => version.id === selectedId);
  const posterId = posterRepresentation?.representationId;
  const poster = posterId ? resources[posterId] : void 0;
  if (stage === "ready" && versionRepresentations.length === 0) {
    return /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.loadFailed : "video_playable_representation_missing"
      }
    );
  }
  if (manifest.stage === "ready" && selected?.src === void 0) {
    const failed = selectedId && resources[selectedId]?.status === "error";
    return failed ? /* @__PURE__ */ jsx(
      ArtifactViewerError,
      {
        filename,
        message: presentation === "user" ? text.loadFailed : resources[selectedId]?.status === "error" ? resources[selectedId].message : "video_representation_load_failed"
      }
    ) : /* @__PURE__ */ jsx(ArtifactViewerLoading, { fill, filename });
  }
  return /* @__PURE__ */ jsx(
    VideoArtifactViewer,
    {
      durationSeconds: manifest.durationMillis === void 0 ? void 0 : Number(manifest.durationMillis) / 1e3,
      filename: selected?.filename ?? filename,
      mimeType: selected?.mimeType ?? item.mimeType ?? "video/mp4",
      onDownload: selected?.src ? () => download(selected.src, selected.filename) : void 0,
      onSelectVersion: setSelectedId,
      posterSrc: poster?.status === "ready" ? poster.url : void 0,
      progress: manifest.progressFraction === void 0 ? void 0 : manifest.progressFraction * 100,
      selectedVersionId: selectedId,
      src: selected?.src ?? "",
      status: stage,
      technicalMetadata: presentation === "developer",
      versions
    }
  );
}
function download(url, filename) {
  const link = document.createElement("a");
  link.href = url;
  link.download = filename || "video";
  link.click();
}
const VIDEO_MEDIA_TYPES = [
  "video/mp4",
  "video/quicktime",
  "video/webm",
  "video/x-m4v"
];
const VIDEO_ROLES = [
  "artifact",
  "original",
  "playable",
  "preview",
  "primary"
];
function createBuiltinContentRenderers(options = {}) {
  const registry = new ContentRendererRegistry();
  registerManifest(registry, "image_edit", ImageEditArtifactViewer);
  registerManifest(registry, "video", VideoManifestArtifactViewer);
  registerManifest(
    registry,
    "presentation",
    PresentationManifestArtifactViewer
  );
  for (const mediaType of VIDEO_MEDIA_TYPES) {
    for (const role of VIDEO_ROLES) {
      registry.register(
        {
          blockKind: "artifact",
          mediaType,
          role,
          schemaVersion: "1"
        },
        { viewer: PlainVideoArtifactViewer },
        `builtin.video.${mediaType}.${role}`
      );
    }
  }
  if (options.jsonRenderer) {
    registry.register(
      {
        blockKind: "json",
        mediaType: "application/json",
        namespace: "agentcloud.ui",
        semanticKey: "form",
        schemaVersion: "1"
      },
      { viewer: PlainJsonArtifactViewer, json: options.jsonRenderer },
      "builtin.json.form"
    );
  }
  return registry;
}
function PlainJsonArtifactViewer(props) {
  return /* @__PURE__ */ jsx(GenericArtifactCard, { ...props });
}
function registerManifest(registry, manifestType, viewer) {
  registry.register(
    {
      blockKind: "artifact",
      manifestType,
      schemaVersion: "1"
    },
    { viewer },
    `builtin.${manifestType}`
  );
}
export {
  X as ActivityTimeline,
  AgentAdapterRegistry,
  A2 as AgentSessionConnection,
  AgentSessionDeck,
  a as AgentSessionRuntimeV2,
  l as AgentTaskQueue,
  Y as AgentTenantBrandingProvider,
  b2 as AgentWorkbenchConnectTransport,
  AgentWorkspace,
  Z as ApprovalDock,
  _ as ArtifactCard,
  $ as AttachmentPreview,
  a0 as ComposerCapabilityBar,
  ContentRendererRegistry,
  a1 as ConversationComposer,
  a2 as ConversationEmptyState,
  DEFAULT_AGENT_COMMANDS,
  E as EventsConnectTransport,
  a3 as ExecutionTrace,
  a4 as GoalPanel,
  ImageComparisonViewer,
  ImageEditComposer,
  ImageLightboxProvider,
  a5 as MarkdownMessage,
  PRESENTATION_GRANTS,
  a6 as PlanStrip,
  PresentationArtifactViewer,
  a7 as ResultWorkbench,
  a8 as STATIC_HTML_CSP,
  a9 as STATIC_HTML_REFERRER_POLICY,
  aa as STATIC_HTML_SANDBOX,
  S as SessionAttachmentError,
  SessionResourceImage,
  ab as TerminalMobileToolbar,
  ac as TerminalSurface,
  ToolRendererRegistry,
  UserVideoTaskPresentation,
  VideoArtifactViewer,
  W as WorkerClient,
  WorkerConversation,
  WorkerConversationDeck,
  b as WorkerLivenessView,
  c as WorkerProvider,
  ad as WorkspaceHeader,
  ZoomableImage,
  agentFormCatalog,
  c2 as artifactActionPayload,
  ae as artifactPresentation,
  d as buildReconnectCommand,
  d2 as canComposePrompt,
  e2 as configurationPayload,
  e as createAgentAdapterBridge,
  f2 as createAgentArtifactLoader,
  d3 as createAgentCommandEnvelope,
  f as createAgentHarnessDescriptor,
  g2 as createAgentWorkbenchRuntime,
  createBuiltinContentRenderers,
  createBuiltinToolRenderers,
  g as defaultAgentHarnessCapabilities,
  h as defaultAgentHarnessPermissionCapabilities,
  i as defaultAgentHarnessWorkspaceCapabilities,
  j as emptyWorkerDirectoryState,
  formCommandFailure,
  af as goalWithInteractionEvidence,
  i2 as interruptPayload,
  j2 as isAgentWorkbenchCursorRejected,
  k as isUnboundCodingFork,
  ag as markdownImageSource,
  ah as mergeAgentAdapterRenderers,
  ai as openStaticHtmlInNewWindow,
  m2 as parseAgentFormSurface,
  p2 as permissionPayload,
  p as projectAttachmentFailure,
  k2 as projectGeneratedSessionSnapshot,
  m as projectPodLiveness,
  r as readWorkerDirectoryState,
  n as reconcileWorkerSessionOrder,
  o as recoveryOptionsFor,
  q as refKey,
  aj as resultScopeKey,
  s2 as sendPromptPayload,
  s as sessionAttachmentFailureOf,
  t as sessionReadOnlyReason,
  ak as staticHtmlDocument,
  al as useAgentTenantBranding,
  useElementFullscreen,
  useLightbox,
  u as useRunnerOnlineEdge,
  am as useWorkbenchContainerMode,
  v as useWorkerClient,
  w as useWorkerLiveness,
  x as useWorkerRuntime,
  y as useWorkerSessionId,
  an as userVideoFailureState,
  w2 as withPartnerIdentity,
  ao as workbenchContainerMode,
  z as workerDirectoryStorageKey,
  workspaceFileArtifacts,
  A as writeWorkerDirectoryState
};
