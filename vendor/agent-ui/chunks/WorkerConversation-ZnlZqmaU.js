import { createContext, useContext, useMemo, useState, useEffect, useRef, useId, Children, isValidElement, useLayoutEffect, useCallback, useSyncExternalStore, useImperativeHandle } from "react";
import { jsx, jsxs, Fragment } from "react/jsx-runtime";
import { LoaderCircle, ImageIcon, FileText, Download, FileAudio, FileImage, FileVideo, Presentation, FileCode2, File, AlertCircle, RotateCcw, Loader2, Play, ExternalLink, FileDown, Check, Copy, Wrench, ChevronRight, Clock3, AlertTriangle, CircleStop, CheckCircle2, Atom, HelpCircle, ChevronDown, X, ChevronLeft, ShieldAlert, Paperclip, Sparkles, Bot, ShieldCheck, SquareTerminal, Pencil, Trash2, ListOrdered, Square, ArrowUp, Folder, CirclePause, WifiOff, CircleAlert, Circle, FolderTree, Search, GitCommitHorizontal, GitBranch, PanelLeft, RefreshCw, Minimize, Maximize, Wifi, MessageSquare, Terminal, ArrowLeft, ArrowDown, ArrowRight, CornerDownLeft, Keyboard } from "lucide-react";
import Papa from "papaparse";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useVirtualizer } from "@tanstack/react-virtual";
import { createPortal } from "react-dom";
const permissionLabels = {
  permissionMode: "权限",
  permission_mode: "权限"
};
function localizeConfigurationLabel(id, fallback) {
  return {
    ...permissionLabels,
    model: "模型",
    mode: "模式",
    effort: "推理强度",
    costControlMode: "成本控制"
  }[id] ?? fallback;
}
function localizeConfigurationOption(id, value, fallback) {
  const permissionOptions = {
    default: "更改前询问",
    acceptEdits: "自动接受编辑",
    bypassPermissions: "无需确认",
    plan: "规划模式",
    bypass: "无需确认",
    ask_dangerous: "危险操作时询问",
    ask_any_write: "写入前询问",
    allow: "允许",
    restricted: "受限"
  };
  const options = {
    permissionMode: permissionOptions,
    permission_mode: permissionOptions,
    mode: { default: "默认", auto: "自动执行", plan: "规划模式" },
    effort: { low: "低", medium: "中", high: "高", xhigh: "极高" },
    costControlMode: { on: "开启", off: "关闭" }
  };
  return options[id]?.[value] ?? fallback;
}
function englishConfigurationLabel(id, fallback) {
  return id === "permissionMode" || id === "permission_mode" ? "Permissions" : fallback;
}
function englishConfigurationOption(id, value, fallback) {
  if (id !== "permissionMode" && id !== "permission_mode") return fallback;
  return {
    bypass: "Full access",
    ask_dangerous: "Ask for dangerous actions",
    ask_any_write: "Ask before writes",
    allow: "Allow",
    restricted: "Restricted"
  }[value] ?? fallback;
}
function englishSessionStatus(status, connection) {
  if (connection === "reconnecting") return "Reconnecting";
  if (connection === "disconnected") return "Offline";
  if (status === "running" || status === "waiting") return "Working";
  if (status === "launching") return "Starting";
  if (status === "failed") return "Failed";
  if (status === "completed") return "Completed";
  return "Ready";
}
function chineseSessionStatus(status, connection) {
  if (connection === "reconnecting") return "正在重连";
  if (connection === "disconnected") return "已离线";
  if (status === "running" || status === "waiting") return "执行中";
  if (status === "launching") return "正在启动";
  if (status === "failed") return "执行失败";
  if (status === "completed") return "已完成";
  return "就绪";
}
function englishActivityStatus(status) {
  return {
    pending: "Pending",
    running: "Running",
    waiting: "Waiting",
    completed: "Completed",
    failed: "Failed",
    cancelled: "Cancelled"
  }[status];
}
function chineseActivityStatus(status) {
  return {
    pending: "等待中",
    running: "执行中",
    waiting: "等待批准",
    completed: "已完成",
    failed: "失败",
    cancelled: "已取消"
  }[status];
}
const zhToolText = {
  Command: "命令",
  Output: "输出",
  "File change": "文件变更",
  Change: "变更",
  Result: "结果",
  "Read file": "读取文件",
  Path: "路径",
  Content: "内容",
  Search: "搜索",
  Query: "查询",
  Matches: "匹配结果",
  Browser: "浏览器",
  Action: "操作",
  "Image generation": "图片生成",
  Prompt: "提示词",
  Presentation: "演示文稿",
  Request: "请求",
  "Video generation": "视频生成",
  Input: "输入",
  Shell: "执行命令",
  "Write file": "写入文件",
  "Fetch page": "抓取网页",
  "Web search": "网页搜索",
  "Ask user": "询问用户",
  "Computer use": "计算机操作",
  "MCP call": "MCP 调用",
  "MCP tools": "MCP 工具列表",
  "Run code": "运行代码",
  Code: "代码",
  Plan: "计划",
  "Publish artifact": "发布成果",
  "Custom tool": "自定义工具"
};
function localizeToolText(value) {
  return zhToolText[value] ?? value;
}
function englishToolActivityGroupSummary(counts) {
  return counts.map(englishCount).join(" · ");
}
function chineseToolActivityGroupSummary(counts) {
  return counts.map(chineseCount).join(" · ");
}
function englishCount({ count, label }) {
  switch (label) {
    case "Command":
      return `Ran ${count} ${plural(count, "command")}`;
    case "File change":
      return `Changed ${count} ${plural(count, "file")}`;
    case "Read file":
      return `Read ${count} ${plural(count, "file")}`;
    case "Search":
      return `Ran ${count} ${plural(count, "search", "searches")}`;
    case "Browser":
      return `Used browser ${count} ${plural(count, "time")}`;
    case "Image generation":
      return `Generated ${count} ${plural(count, "image")}`;
    case "Presentation":
      return `Generated ${count} ${plural(count, "presentation")}`;
    case "Video generation":
      return `Generated ${count} ${plural(count, "video")}`;
    default:
      return `Used ${label} ${count} ${plural(count, "time")}`;
  }
}
function chineseCount({ count, label }) {
  switch (label) {
    case "Command":
      return `运行了 ${count} 个命令`;
    case "File change":
      return `修改了 ${count} 个文件`;
    case "Read file":
      return `读取了 ${count} 个文件`;
    case "Search":
      return `执行了 ${count} 次搜索`;
    case "Browser":
      return `使用浏览器 ${count} 次`;
    case "Image generation":
      return `生成了 ${count} 张图片`;
    case "Presentation":
      return `生成了 ${count} 份演示文稿`;
    case "Video generation":
      return `生成了 ${count} 个视频`;
    default:
      return `使用 ${localizeToolText(label)} ${count} 次`;
  }
}
function plural(count, singular, pluralForm = `${singular}s`) {
  return count === 1 ? singular : pluralForm;
}
function artifactWorkspaceText(locale) {
  return locale === "zh-CN" ? zhCN : enUS;
}
const enUS = {
  generatedArtifact: "Generated artifact",
  generationFailed: "Artifact generation failed",
  loadingUnavailable: "Artifact loading is unavailable",
  downloadNotAuthorized: "You do not have permission to access this artifact",
  loadFailed: "Artifact loading failed. Try again.",
  loading: (filename) => `Loading ${filename}`,
  load: (filename) => `Load ${filename}`,
  retry: (filename) => `Retry loading ${filename}`,
  preview: (filename) => `Preview ${filename}`,
  audioPreview: (filename) => `Audio preview for ${filename}`,
  csvPreview: (filename) => `CSV preview for ${filename}`,
  csvPreviewFailed: (filename) => `Unable to preview ${filename} as CSV.`,
  diagramRenderFailed: "Unable to render this diagram.",
  previewLimited: (bytes) => `Preview limited to the first ${Math.floor(bytes / 1024 / 1024)} MiB.`,
  pdfPreview: (filename) => `PDF preview for ${filename}`,
  videoPreview: (filename) => `Video preview for ${filename}`,
  videoPlaybackFailed: (filename) => `Unable to play ${filename}. Try loading it again.`,
  videoUnsupported: "Your browser does not support video playback.",
  fullscreenVideo: "View video fullscreen",
  exitFullscreen: "Exit fullscreen",
  fullscreenUnavailable: "Fullscreen is unavailable in this browser context.",
  open: (filename) => `Open ${filename}`,
  download: (filename) => `Download ${filename}`
};
const zhCN = {
  generatedArtifact: "生成的成果",
  generationFailed: "成果生成失败",
  loadingUnavailable: "当前无法加载成果",
  downloadNotAuthorized: "你没有权限访问此成果",
  loadFailed: "成果加载失败，请重试。",
  loading: (filename) => `正在加载 ${filename}`,
  load: (filename) => `加载 ${filename}`,
  retry: (filename) => `重新加载 ${filename}`,
  preview: (filename) => `预览 ${filename}`,
  audioPreview: (filename) => `${filename} 的音频预览`,
  csvPreview: (filename) => `${filename} 的 CSV 预览`,
  csvPreviewFailed: (filename) => `${filename} 无法作为 CSV 预览。`,
  diagramRenderFailed: "无法渲染这张图。",
  previewLimited: (bytes) => `仅预览前 ${Math.floor(bytes / 1024 / 1024)} MiB 内容。`,
  pdfPreview: (filename) => `${filename} 的 PDF 预览`,
  videoPreview: (filename) => `${filename} 的视频预览`,
  videoPlaybackFailed: (filename) => `${filename} 无法播放，请重新加载。`,
  videoUnsupported: "当前浏览器不支持视频播放。",
  fullscreenVideo: "全屏预览视频",
  exitFullscreen: "退出全屏预览",
  fullscreenUnavailable: "当前页面环境无法进入全屏预览。",
  open: (filename) => `打开 ${filename}`,
  download: (filename) => `下载 ${filename}`
};
function knowledgeAnswerText(locale) {
  return locale === "zh-CN" ? {
    disclaimer: "以上信息由AI大模型依据知识库文档生成，内容仅供参考",
    sourcesHeading: "回答引用来源"
  } : {
    disclaimer: "This answer was generated from the knowledge base and is for reference only.",
    sourcesHeading: "Answer sources"
  };
}
const englishVideoTaskStatus = {
  failed: {
    title: "Video generation failed",
    detail: "No verified playable video was produced."
  },
  model_quota_exhausted: {
    title: "Agent primary-model quota exhausted",
    detail: "No verified video file is available."
  },
  partial: {
    title: "Video file available, but the task did not finish cleanly",
    detail: "The published file remains available; a later step failed."
  },
  processing: {
    title: "Creating video",
    detail: "The result will appear after generation and file verification."
  },
  provider_auth_failed: {
    title: "Video service credentials failed",
    detail: "The provider rejected the video credentials, so no video file was generated."
  },
  provider_unavailable: {
    title: "Video service temporarily unavailable",
    detail: "The third-party video account pool has no available account, so no video file was generated."
  },
  task_failed: {
    title: "Task failed",
    detail: "No verified result is available."
  },
  verified: {
    title: "Video file published and integrity checked",
    detail: "The platform verified the published playable file, not its provider origin."
  },
  verification_failed: {
    title: "Video result failed verification",
    detail: "The preview is hidden because the playable file metadata is incomplete."
  }
};
const chineseVideoTaskStatus = {
  failed: {
    title: "视频生成失败",
    detail: "没有生成通过校验的可播放视频。"
  },
  model_quota_exhausted: {
    title: "智能体主模型额度耗尽",
    detail: "未取得可验证视频文件。"
  },
  partial: {
    title: "视频文件可用，但任务未完整结束",
    detail: "已发布的视频文件仍可使用；后续步骤执行失败。"
  },
  processing: {
    title: "正在生成视频",
    detail: "生成完成并通过文件校验后，成果会显示在右侧。"
  },
  provider_auth_failed: {
    title: "视频服务凭据无效",
    detail: "供应商拒绝了当前视频生成凭据，未生成视频文件。"
  },
  provider_unavailable: {
    title: "视频服务暂不可用",
    detail: "第三方视频账号池当前没有可用账号，未生成视频文件。"
  },
  task_failed: {
    title: "任务执行失败",
    detail: "当前没有可验证的成果。"
  },
  verified: {
    title: "视频文件已发布并通过完整性校验",
    detail: "平台已校验发布文件；此状态不代表供应商来源已被证明。"
  },
  verification_failed: {
    title: "视频成果校验未通过",
    detail: "可播放文件信息不完整，平台已停止展示预览。"
  }
};
const englishVideoExecutionTrace = {
  label: "Video creation progress",
  step: {
    request: "Request received",
    generation: "Generate video",
    preview: "Prepare preview",
    verification: "Verify and publish"
  },
  detail: {
    generation_failed: "Video generation did not finish",
    generation_ready: "Video frames are ready",
    model_quota_exhausted: "Agent primary-model quota exhausted; no verified video file is available",
    preview_failed: "Preview preparation did not finish",
    preview_ready: "Playable file is ready",
    preparing_preview: "Preparing a playable file",
    provider_auth_failed: "Video credentials were rejected",
    provider_unavailable: "No video account is currently available",
    published: "File checked and published",
    queued: "Waiting to start",
    request_received: "Video creation request accepted",
    verification_failed: "File verification did not pass",
    verification_incomplete: "Task ended before file verification",
    verifying: "Checking the playable file",
    waiting: "Waiting for the previous step"
  },
  rendering: (progress) => progress === void 0 ? "Generating video" : `Generating video (${Math.round(progress * 100)}%)`
};
const chineseVideoExecutionTrace = {
  label: "视频创作进度",
  step: {
    request: "接收创作请求",
    generation: "生成视频画面",
    preview: "准备播放文件",
    verification: "校验并发布结果"
  },
  detail: {
    generation_failed: "视频生成未完成",
    generation_ready: "视频画面已生成",
    model_quota_exhausted: "智能体主模型额度不足；未取得可验证视频文件",
    preview_failed: "播放文件准备未完成",
    preview_ready: "可播放文件已准备",
    preparing_preview: "正在准备可播放文件",
    provider_auth_failed: "视频服务凭据校验失败",
    provider_unavailable: "视频服务当前没有可用账号",
    published: "文件已校验并发布",
    queued: "等待开始生成",
    request_received: "已接收视频创作请求",
    verification_failed: "文件校验未通过",
    verification_incomplete: "任务结束前未完成文件校验",
    verifying: "正在校验可播放文件",
    waiting: "等待上一步完成"
  },
  rendering: (progress) => progress === void 0 ? "正在生成视频" : `正在生成视频（${Math.round(progress * 100)}%）`
};
const zhActivityTitles = {
  "Sending task…": "正在发送任务…",
  "Starting agent…": "正在启动智能体…",
  "Processing task…": "正在处理任务…",
  "Thinking…": "正在思考…",
  "Using tools…": "正在使用工具…",
  "Writing response…": "正在生成回复…",
  "Waiting for your confirmation…": "等待你确认…",
  "Reconnecting and syncing execution status…": "连接恢复中，正在同步执行状态…",
  "Disconnected; execution status is unavailable": "连接已断开，执行状态暂不可用",
  Reasoning: "推理",
  "Plan updated": "计划已更新",
  "Permission requested": "请求权限",
  System: "系统",
  "Unsupported timeline item": "不支持的记录",
  "Unsupported content": "不支持的内容",
  "Unsupported artifact": "不支持的成果",
  "Unsupported tool execution": "不支持的工具执行",
  "Unsupported permission request": "不支持的权限请求"
};
const zhSessionTitles = {
  idle: "会话空闲",
  launching: "会话启动中",
  running: "会话执行中",
  waiting: "会话等待中",
  completed: "会话已完成",
  failed: "会话失败",
  "resync required": "会话需重新同步",
  unspecified: "会话状态未知"
};
function englishActivityTitle(title) {
  return title;
}
function chineseActivityTitle(title) {
  if (title.startsWith("Session ")) {
    return zhSessionTitles[title.slice("Session ".length)] ?? title;
  }
  return zhActivityTitles[title] ?? title;
}
function workbenchWorkspaceText(locale) {
  if (locale === "zh-CN") {
    return {
      codeMode: "代码",
      workMode: "工作",
      goalMode: "目标",
      files: "文件",
      changes: "变更",
      codePane: "代码",
      resultsPane: "成果",
      goalPane: "目标进度",
      goalCriteria: "验收标准",
      goalEvidence: "证据",
      goalStart: "启动目标",
      goalVerify: "验证目标",
      goalCancel: "取消目标",
      goalActionRunning: "处理中…",
      goalActionFailed: "目标操作失败，请重试",
      emptyWorkspace: "工作区暂无文件",
      workspaceLoading: "正在读取工作区文件树",
      workspaceLoadFailed: "文件树加载失败，点击重试",
      workspaceRoot: "工作区根目录",
      selectFile: "选择一个文件开始查看",
      gitUnsupported: "不支持 Git 对比",
      unavailable: "当前会话未提供代码工作区",
      cachedContent: "缓存内容",
      truncated: "文件已截断",
      binaryFile: "二进制文件，无法作为源码显示",
      deletedFile: "文件已删除",
      findInFile: "在文件中查找",
      copy: "复制",
      refresh: "刷新",
      closeTab: "关闭标签",
      currentWorkspace: "当前工作区",
      tooLargeToHighlight: "文件过大，无法高亮",
      downloadFile: "下载",
      emptyBaseline: "相对空基线对比",
      accessDenied: "没有访问该工作区的权限",
      refreshFailed: "刷新失败，仍显示上次内容",
      searchFiles: "搜索文件名或路径",
      noSearchResults: "没有匹配的文件",
      searchIncomplete: "结果不完整，请缩小范围",
      searchTimedOut: "搜索超时，请缩小范围",
      searching: "搜索中",
      referenceCode: "引用代码",
      git: "Git",
      gitUnavailable: "Git 信息不可用",
      noGitRepository: "当前工作区不是 Git 仓库",
      detachedHead: "分离 HEAD",
      cleanWorkspace: "工作区干净",
      dirtyWorkspace: "工作区有未提交变更",
      branches: "分支",
      history: "提交历史",
      noCommits: "暂无提交历史"
    };
  }
  return {
    codeMode: "Code",
    workMode: "Work",
    goalMode: "Goal",
    files: "Files",
    changes: "Changes",
    codePane: "Code",
    resultsPane: "Results",
    goalPane: "Goal progress",
    goalCriteria: "Acceptance criteria",
    goalEvidence: "Evidence",
    goalStart: "Start goal",
    goalVerify: "Verify goal",
    goalCancel: "Cancel goal",
    goalActionRunning: "Working…",
    goalActionFailed: "Goal action failed; try again",
    emptyWorkspace: "This workspace has no files",
    workspaceLoading: "Reading workspace files",
    workspaceLoadFailed: "File tree failed to load; click to retry",
    workspaceRoot: "Workspace root",
    selectFile: "Select a file to inspect",
    gitUnsupported: "Git comparison is not available",
    unavailable: "This session does not provide a code workspace",
    cachedContent: "Cached content",
    truncated: "File is truncated",
    binaryFile: "Binary file cannot be shown as source",
    deletedFile: "File was deleted",
    findInFile: "Find in file",
    copy: "Copy",
    refresh: "Refresh",
    closeTab: "Close tab",
    currentWorkspace: "Current workspace",
    tooLargeToHighlight: "Too large to highlight",
    downloadFile: "Download",
    emptyBaseline: "Compared against an empty baseline",
    accessDenied: "You do not have access to this workspace",
    refreshFailed: "Refresh failed; showing the last loaded content",
    searchFiles: "Search file names or paths",
    noSearchResults: "No matching files",
    searchIncomplete: "Results are incomplete; narrow your search",
    searchTimedOut: "Search timed out; narrow your search",
    searching: "Searching",
    referenceCode: "Reference code",
    git: "Git",
    gitUnavailable: "Git information is unavailable",
    noGitRepository: "This workspace is not a Git repository",
    detachedHead: "Detached HEAD",
    cleanWorkspace: "Clean workspace",
    dirtyWorkspace: "Uncommitted changes",
    branches: "Branches",
    history: "History",
    noCommits: "No commit history"
  };
}
const chineseAgentWorkspaceText = {
  conversation: "对话",
  results: "成果",
  artifacts: "成果列表",
  workspaceViews: "工作区视图",
  terminal: "终端",
  enterFullscreen: "进入全屏",
  exitFullscreen: "退出全屏",
  sessions: "会话",
  newSession: "新对话",
  plan: "执行计划",
  agentPlan: "智能体执行计划",
  loadEarlierActivity: "加载更早记录",
  jumpToLatest: "跳到最新",
  readyForTask: "可以开始任务",
  startSession: "发送消息以启动智能体会话。",
  you: "你",
  agent: "智能体",
  system: "系统",
  agentic: "智能体模式",
  terminalMode: "终端模式",
  approvals: "请求审批",
  messageAgent: "给智能体发送消息",
  readOnly: "此会话为只读状态",
  stopAgent: "停止智能体",
  sendMessage: "发送消息",
  queueMessage: "排队发送",
  queuedMessages: "排队消息",
  queuePending: "排队中",
  queueDispatching: "发送中",
  queueCancelled: "已取消",
  cancelQueuedMessage: "取消排队消息",
  editQueuedMessage: "编辑排队消息",
  saveQueuedMessage: "保存排队消息",
  cancelQueuedEdit: "取消编辑",
  retryQueuedMessage: "重试排队消息",
  removeQueuedMessage: "移除排队消息",
  messagePending: "等待发送确认…",
  messageFailed: "消息发送失败",
  copyMessage: "复制消息",
  copiedMessage: "已复制",
  copyMessageFailed: "消息复制失败",
  addAttachment: "添加附件",
  removeAttachment: "移除附件",
  uploadingAttachment: "正在上传附件",
  attachmentUploadFailed: "附件上传失败，请重试。",
  attachmentUploadUnsupported: "当前会话不支持此附件类型。",
  reject: "拒绝",
  skipQuestion: "跳过本题",
  approve: "批准",
  submitAnswers: "提交回答",
  customAnswer: "自定义回答",
  previousQuestion: "上一题",
  nextQuestion: "下一题",
  expandQuestion: "展开提问",
  collapseQuestion: "收起提问",
  details: "详细信息",
  takeControl: "接管终端",
  reconnectSession: "重新连接",
  wakeWorker: "唤醒新 Worker",
  sessionNeedsRecovery: "当前无法发送。请重新连接，或唤醒一个新的 Worker 后继续。",
  taskFailed: "任务执行失败，当前没有可验证的成果。",
  conversationError: "对话处理遇到问题，请重试。",
  dismissError: "关闭错误提示",
  rawToolEvidence: "原始工具证据",
  videoExecutionTrace: chineseVideoExecutionTrace,
  videoTaskStatus: chineseVideoTaskStatus,
  artifact: artifactWorkspaceText("zh-CN"),
  knowledgeAnswer: knowledgeAnswerText("zh-CN"),
  generatedArtifact: "生成的成果",
  partnerIdentity: "智能体伙伴",
  workbench: workbenchWorkspaceText("zh-CN"),
  emptyHeading: (agentLabel) => `${agentLabel}，我能为你做什么？`,
  composerPlaceholder: (agentLabel) => `让 ${agentLabel} 帮你完成任务…`,
  requiresArgument: (commandLabel) => `${commandLabel} 需要填写参数`,
  slashCommandAttachmentsUnsupported: "斜杠命令不能携带附件。请移除附件，或改为发送普通消息。",
  customAnswerFor: (prompt) => `${prompt}的自定义回答`,
  loadingArtifact: (filename) => `正在加载 ${filename}`,
  previewArtifact: (filename) => `预览 ${filename}`,
  retryArtifact: (filename) => `重试预览 ${filename}`,
  videoPreview: (filename) => `${filename} 的视频预览`,
  openArtifact: (filename) => `打开 ${filename}`,
  downloadArtifact: (filename) => `下载 ${filename}`,
  closeSession: (title) => `关闭 ${title}`,
  conversationTitle: (index) => `对话 ${index}`,
  configurationOptions: (label) => `${label}选项`,
  configurationLabel: localizeConfigurationLabel,
  configurationOption: localizeConfigurationOption,
  sessionStatus: chineseSessionStatus,
  activityStatus: chineseActivityStatus,
  activityTitle: chineseActivityTitle,
  artifactType: (kind, fallback) => ({
    "HTML document": "HTML 文档",
    "SVG document": "SVG 文档",
    "Mermaid diagram": "Mermaid 图",
    Audio: "音频",
    "CSV table": "CSV 表格",
    Markdown: "Markdown",
    Image: "图片",
    Video: "视频",
    PDF: "PDF 文档",
    PowerPoint: "PowerPoint 演示文稿",
    "Code file": "代码文件",
    "Text file": "文本文件",
    File: "文件"
  })[kind] ?? fallback,
  toolText: localizeToolText,
  toolActivityGroupSummary: chineseToolActivityGroupSummary
};
const englishAgentWorkspaceText = {
  conversation: "Conversation",
  results: "Results",
  artifacts: "Artifacts",
  workspaceViews: "Workspace views",
  terminal: "Terminal",
  enterFullscreen: "Enter fullscreen",
  exitFullscreen: "Exit fullscreen",
  sessions: "Sessions",
  newSession: "New conversation",
  plan: "Plan",
  agentPlan: "Agent plan",
  loadEarlierActivity: "Load earlier activity",
  jumpToLatest: "Jump to latest",
  readyForTask: "Ready for a task",
  startSession: "Send a message to start this agent session.",
  you: "You",
  agent: "Agent",
  system: "System",
  agentic: "Agentic",
  terminalMode: "Terminal mode",
  approvals: "Approvals",
  messageAgent: "Message the agent",
  readOnly: "This session is read-only",
  stopAgent: "Stop agent",
  sendMessage: "Send message",
  queueMessage: "Queue message",
  queuedMessages: "Queued messages",
  queuePending: "Queued",
  queueDispatching: "Sending",
  queueCancelled: "Cancelled",
  cancelQueuedMessage: "Cancel queued message",
  editQueuedMessage: "Edit queued message",
  saveQueuedMessage: "Save queued message",
  cancelQueuedEdit: "Cancel queued edit",
  retryQueuedMessage: "Retry queued message",
  removeQueuedMessage: "Remove queued message",
  messagePending: "Waiting for confirmation…",
  messageFailed: "Message failed",
  copyMessage: "Copy message",
  copiedMessage: "Copied",
  copyMessageFailed: "Could not copy the message",
  addAttachment: "Add attachment",
  removeAttachment: "Remove attachment",
  uploadingAttachment: "Uploading attachment",
  attachmentUploadFailed: "Could not upload the attachment. Try again.",
  attachmentUploadUnsupported: "This attachment type is not supported by the current session.",
  reject: "Reject",
  skipQuestion: "Skip question",
  approve: "Approve",
  submitAnswers: "Submit answers",
  customAnswer: "Custom answer",
  previousQuestion: "Previous question",
  nextQuestion: "Next question",
  expandQuestion: "Expand question",
  collapseQuestion: "Collapse question",
  details: "Details",
  takeControl: "Take control",
  reconnectSession: "Reconnect",
  wakeWorker: "Wake a new Worker",
  sessionNeedsRecovery: "This conversation cannot send yet. Reconnect, or wake a new Worker to continue.",
  taskFailed: "The task failed. No verified result is available.",
  conversationError: "The conversation encountered a problem. Please try again.",
  dismissError: "Dismiss error",
  rawToolEvidence: "Raw tool evidence",
  videoExecutionTrace: englishVideoExecutionTrace,
  videoTaskStatus: englishVideoTaskStatus,
  artifact: artifactWorkspaceText("en-US"),
  knowledgeAnswer: knowledgeAnswerText("en-US"),
  generatedArtifact: "Generated artifact",
  partnerIdentity: "Agent partner",
  workbench: workbenchWorkspaceText("en-US"),
  emptyHeading: (agentLabel) => `${agentLabel}, what should we work on?`,
  composerPlaceholder: (agentLabel) => `Ask ${agentLabel} to work on a task…`,
  requiresArgument: (commandLabel) => `${commandLabel} requires an argument`,
  slashCommandAttachmentsUnsupported: "Slash commands cannot include attachments. Remove the attachments or send a regular message.",
  customAnswerFor: (prompt) => `Custom answer for ${prompt}`,
  loadingArtifact: (filename) => `Loading ${filename}`,
  previewArtifact: (filename) => `Preview ${filename}`,
  retryArtifact: (filename) => `Retry preview for ${filename}`,
  videoPreview: (filename) => `Video preview for ${filename}`,
  openArtifact: (filename) => `Open ${filename}`,
  downloadArtifact: (filename) => `Download ${filename}`,
  closeSession: (title) => `Close ${title}`,
  conversationTitle: (index) => `Conversation ${index}`,
  configurationOptions: (label) => `${label} options`,
  configurationLabel: englishConfigurationLabel,
  configurationOption: englishConfigurationOption,
  sessionStatus: englishSessionStatus,
  activityStatus: englishActivityStatus,
  activityTitle: englishActivityTitle,
  artifactType: (_kind, fallback) => fallback,
  toolText: (value) => value,
  toolActivityGroupSummary: englishToolActivityGroupSummary
};
function agentWorkspaceText(locale) {
  return locale === "zh-CN" ? chineseAgentWorkspaceText : englishAgentWorkspaceText;
}
const AgentWorkspaceTextContext = createContext(agentWorkspaceText("en-US"));
const AgentWorkspaceLocaleContext = createContext("en-US");
function AgentWorkspaceLocaleProvider({
  children,
  locale
}) {
  const text = useMemo(() => agentWorkspaceText(locale), [locale]);
  return /* @__PURE__ */ jsx(AgentWorkspaceLocaleContext.Provider, { value: locale, children: /* @__PURE__ */ jsx(AgentWorkspaceTextContext.Provider, { value: text, children }) });
}
function useAgentWorkspaceText() {
  return useContext(AgentWorkspaceTextContext);
}
function useAgentWorkspaceLocale() {
  return useContext(AgentWorkspaceLocaleContext);
}
function AttachmentPreview({
  item,
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const isImage = isImageAttachment(item.filename, item.mimeType);
  const canLoad = Boolean(runtime.loadAttachment);
  const [state, setState] = useState(
    isImage && canLoad ? "loading" : "idle"
  );
  const [url, setUrl] = useState(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!isImage || !runtime.loadAttachment) {
      setState("idle");
      setUrl(null);
      return;
    }
    let active = true;
    let objectUrl = null;
    setState("loading");
    void runtime.loadAttachment(sessionId, item.attachmentId).then((blob) => {
      if (!active) return;
      objectUrl = URL.createObjectURL(blob);
      setUrl(objectUrl);
      setState("ready");
    }).catch(() => {
      if (active) setState("error");
    });
    return () => {
      active = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [attempt, isImage, item.attachmentId, item.representationId, runtime, sessionId]);
  const download = async () => {
    if (!runtime.loadAttachment) return;
    setState("loading");
    try {
      const blob = await runtime.loadAttachment(sessionId, item.attachmentId);
      const downloadUrl = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = downloadUrl;
      link.download = item.filename;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(downloadUrl), 0);
      setState("ready");
    } catch {
      setState("error");
    }
  };
  if (state === "ready" && url) {
    return /* @__PURE__ */ jsxs(
      "a",
      {
        "aria-label": text.previewArtifact(item.filename),
        className: "ml-auto block max-w-[85%] min-w-0 overflow-hidden rounded-md border border-border bg-muted/20 transition-colors hover:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        href: url,
        rel: "noreferrer",
        target: "_blank",
        children: [
          /* @__PURE__ */ jsx(
            "img",
            {
              alt: item.filename,
              className: "max-h-72 max-w-full object-contain",
              decoding: "async",
              onError: () => {
                setUrl((current) => {
                  if (current) URL.revokeObjectURL(current);
                  return null;
                });
                setState("error");
              },
              src: url
            }
          ),
          /* @__PURE__ */ jsx("span", { className: "block max-w-full truncate px-2 py-1 text-xs text-muted-foreground", title: item.filename, children: item.filename })
        ]
      }
    );
  }
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-busy": state === "loading",
      className: "ml-auto flex max-w-[85%] min-w-0 items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2",
      children: [
        isImage ? state === "loading" ? /* @__PURE__ */ jsx(
          LoaderCircle,
          {
            "aria-label": text.loadingArtifact(item.filename),
            className: "size-4 shrink-0 animate-spin text-muted-foreground"
          }
        ) : /* @__PURE__ */ jsx(ImageIcon, { className: "size-4 shrink-0 text-muted-foreground" }) : /* @__PURE__ */ jsx(FileText, { className: "size-4 shrink-0 text-muted-foreground" }),
        /* @__PURE__ */ jsxs("div", { className: "min-w-0 flex-1", children: [
          /* @__PURE__ */ jsx("div", { className: "truncate text-sm font-medium", title: item.filename, children: item.filename }),
          item.mimeType ? /* @__PURE__ */ jsx("div", { className: "truncate text-xs text-muted-foreground", children: item.mimeType }) : null
        ] }),
        state === "error" && isImage ? /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": text.retryArtifact(item.filename),
            className: "inline-flex min-h-8 shrink-0 items-center justify-center rounded-md px-2 text-xs text-primary hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            onClick: () => {
              setState("loading");
              setAttempt((value) => value + 1);
            },
            title: text.retryArtifact(item.filename),
            type: "button",
            children: text.retryArtifact(item.filename)
          }
        ) : null,
        runtime.loadAttachment && !isImage ? /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": text.downloadArtifact(item.filename),
            className: "inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
            disabled: state === "loading",
            onClick: () => void download(),
            title: text.downloadArtifact(item.filename),
            type: "button",
            children: state === "loading" ? /* @__PURE__ */ jsx(LoaderCircle, { className: "size-3.5 animate-spin" }) : /* @__PURE__ */ jsx(Download, { className: "size-3.5" })
          }
        ) : null
      ]
    }
  );
}
function isImageAttachment(filename, mimeType) {
  if (mimeType?.startsWith("image/")) return true;
  return /\.(avif|bmp|gif|jpe?g|png|svg|webp)$/i.test(filename);
}
function artifactPresentation(mimeType, filename) {
  const mime = mimeType?.toLowerCase() ?? "";
  const extension = filename.split(".").pop()?.toLowerCase() ?? "";
  if (mime === "text/html" || extension === "html" || extension === "htm") {
    return { kind: "html", label: "HTML document" };
  }
  if (mime === "image/svg+xml" || extension === "svg") {
    return { kind: "svg", label: "SVG document" };
  }
  if (mime === "text/x-mermaid" || mermaidExtensions.has(extension)) {
    return { kind: "mermaid", label: "Mermaid diagram" };
  }
  if (mime.startsWith("audio/") || audioExtensions.has(extension)) {
    return { kind: "audio", label: "Audio" };
  }
  if (mime === "text/csv" || extension === "csv") {
    return { kind: "csv", label: "CSV table" };
  }
  if (mime === "text/markdown" || extension === "md") {
    return { kind: "markdown", label: "Markdown" };
  }
  if (mime.startsWith("image/") || imageExtensions.has(extension)) {
    return { kind: "image", label: "Image" };
  }
  if (mime.startsWith("video/") || videoExtensions.has(extension)) {
    return { kind: "video", label: "Video" };
  }
  if (mime === "application/pdf" || extension === "pdf") {
    return { kind: "pdf", label: "PDF" };
  }
  if (mime.includes("presentation") || extension === "ppt" || extension === "pptx") {
    return { kind: "presentation", label: "PowerPoint" };
  }
  if (codeMimeTypes.has(mime) || codeExtensions.has(extension)) {
    return { kind: "code", label: "Code file" };
  }
  if (mime.startsWith("text/") || textExtensions.has(extension)) {
    return { kind: "text", label: "Text file" };
  }
  return { kind: "file", label: mimeType || "File" };
}
const imageExtensions = /* @__PURE__ */ new Set(["avif", "gif", "jpeg", "jpg", "png", "webp"]);
const mermaidExtensions = /* @__PURE__ */ new Set(["mmd", "mermaid"]);
const audioExtensions = /* @__PURE__ */ new Set(["aac", "flac", "m4a", "mp3", "oga", "ogg", "wav"]);
const videoExtensions = /* @__PURE__ */ new Set(["mov", "mp4", "m4v", "webm"]);
const codeMimeTypes = /* @__PURE__ */ new Set([
  "application/javascript",
  "application/json",
  "application/typescript",
  "text/css",
  "text/javascript",
  "text/typescript"
]);
const codeExtensions = /* @__PURE__ */ new Set([
  "cjs",
  "css",
  "go",
  "js",
  "jsx",
  "json",
  "mjs",
  "py",
  "rs",
  "scss",
  "sh",
  "ts",
  "tsx"
]);
const textExtensions = /* @__PURE__ */ new Set(["md", "txt", "toml", "yaml", "yml"]);
const SAFE_IMAGE_MEDIA_TYPES = /* @__PURE__ */ new Set([
  "image/avif",
  "image/gif",
  "image/jpeg",
  "image/png",
  "image/webp"
]);
function isSafeArtifactImage(mediaType) {
  return SAFE_IMAGE_MEDIA_TYPES.has(mediaType.toLowerCase());
}
function artifactRendererKey(item) {
  if (item.manifest?.kind === "image_edit") {
    return manifestKey(item, "image_edit");
  }
  if (item.manifest?.kind === "video") {
    return manifestKey(item, "video");
  }
  if (item.manifest?.kind === "presentation" && hasPresentationPages(item)) {
    return manifestKey(item, "presentation");
  }
  if (item.manifest?.kind === "extension") {
    const manifest = item.manifest;
    if (!manifest.namespace || !manifest.semanticType || !manifest.schemaVersion) return null;
    return {
      blockKind: "artifact",
      manifestType: "extension",
      namespace: manifest.namespace,
      semanticKey: manifest.semanticType,
      schemaVersion: manifest.schemaVersion
    };
  }
  const videoMediaType = playableVideoMediaType(item);
  if (videoMediaType) {
    return {
      blockKind: "artifact",
      mediaType: videoMediaType,
      role: item.role,
      schemaVersion: item.schemaVersion
    };
  }
  if (!item.mimeType) return null;
  return {
    blockKind: "artifact",
    mediaType: item.mimeType,
    role: item.role,
    schemaVersion: item.schemaVersion
  };
}
function artifactUnsupportedReason(item) {
  if (item.role === "image_edit" && item.manifest?.kind !== "image_edit") {
    return "image_edit_manifest_missing";
  }
  return null;
}
function manifestKey(item, manifestType) {
  return {
    blockKind: "artifact",
    manifestType,
    schemaVersion: item.schemaVersion
  };
}
function hasPresentationPages(item) {
  if (item.manifest?.kind !== "presentation") return false;
  const representations = new Map(
    item.representations.map((representation) => [
      representation.representationId,
      representation
    ])
  );
  return item.manifest.slides.some((slide) => {
    if (!slide.pageRepresentationId) return false;
    const representation = representations.get(slide.pageRepresentationId);
    return representation?.status === "ready" && isSafeArtifactImage(representation.mediaType);
  });
}
function playableVideoMediaType(item) {
  if (artifactPresentation(item.mimeType, item.filename).kind !== "video") {
    return null;
  }
  const mime = item.mimeType?.toLowerCase() ?? "";
  if (mime.startsWith("video/")) return mime;
  const extension = item.filename.split(".").pop()?.toLowerCase() ?? "";
  if (extension === "webm") return "video/webm";
  if (extension === "mov") return "video/quicktime";
  if (extension === "m4v") return "video/x-m4v";
  return "video/mp4";
}
function artifactKindIcon(kind) {
  if (kind === "audio") return FileAudio;
  if (kind === "image" || kind === "svg") return FileImage;
  if (kind === "video") return FileVideo;
  if (kind === "presentation") return Presentation;
  if (kind === "code" || kind === "csv" || kind === "html" || kind === "mermaid") return FileCode2;
  if (kind === "markdown" || kind === "pdf" || kind === "text") return FileText;
  return File;
}
function artifactActionAllowed(item, actionType, representationId) {
  return item.grants.some((grant) => {
    if (!grant.actions.includes(actionType)) return false;
    if (grant.expiresAt) {
      const expiresAt = Date.parse(grant.expiresAt);
      if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) return false;
    }
    if (grant.representationIds.length > 0 && (!representationId || !grant.representationIds.includes(representationId))) {
      return false;
    }
    if (grant.minimumRevision !== void 0 && item.revision < grant.minimumRevision) {
      return false;
    }
    return grant.maximumRevision === void 0 || item.revision <= grant.maximumRevision;
  });
}
const FILLED = {
  scroll: "min-h-0 w-full flex-1 overflow-auto border-b border-border",
  media: "min-h-0 w-full flex-1 bg-muted object-contain",
  frame: "min-h-0 w-full flex-1 border-b border-border bg-white",
  shell: "flex min-h-0 w-full flex-1 items-center justify-center border-b border-border"
};
function previewPaneClass(fill, compact, surface = "scroll") {
  return fill ? FILLED[surface] : compact;
}
function ArtifactPreviewError({
  filename,
  fill = false,
  message,
  onRetry
}) {
  const text = useAgentWorkspaceText().artifact;
  return /* @__PURE__ */ jsxs(
    "div",
    {
      className: `${previewPaneClass(
        fill,
        "flex min-h-36 items-center justify-center border-b border-destructive/30 bg-destructive/5",
        "shell"
      )} gap-3 border-destructive/30 bg-destructive/5 p-4 text-sm`,
      role: "alert",
      children: [
        /* @__PURE__ */ jsx(AlertCircle, { className: "size-4 shrink-0 text-destructive" }),
        /* @__PURE__ */ jsx("span", { className: "text-destructive", children: message }),
        onRetry && /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": text.retry(filename),
            className: "inline-flex size-11 shrink-0 items-center justify-center rounded-md hover:bg-destructive/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            onClick: onRetry,
            type: "button",
            children: /* @__PURE__ */ jsx(RotateCcw, { className: "size-4" })
          }
        )
      ]
    }
  );
}
const shells = {
  idle: ({ filename, fill, onLoad, text }) => /* @__PURE__ */ jsx(
    "div",
    {
      className: `${previewPaneClass(
        fill,
        "flex min-h-36 items-center justify-center border-b border-border bg-muted/30",
        "shell"
      )} bg-muted/30 p-4`,
      children: /* @__PURE__ */ jsxs(
        "button",
        {
          className: "inline-flex min-h-11 items-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          onClick: onLoad,
          type: "button",
          children: [
            /* @__PURE__ */ jsx(Play, { className: "size-4" }),
            text.load(filename)
          ]
        }
      )
    }
  ),
  loading: ({ filename, fill, text }) => /* @__PURE__ */ jsxs(
    "div",
    {
      className: `${previewPaneClass(
        fill,
        "flex min-h-36 items-center justify-center border-b border-border bg-muted/30",
        "shell"
      )} gap-2 bg-muted/30 p-4 text-sm text-muted-foreground`,
      role: "status",
      children: [
        /* @__PURE__ */ jsx(Loader2, { className: "size-4 animate-spin" }),
        text.loading(filename)
      ]
    }
  ),
  error: ({ filename, fill, state, onRetry, text }) => state.status === "error" ? /* @__PURE__ */ jsx(
    ArtifactPreviewError,
    {
      filename,
      fill,
      message: previewErrorMessage(state.code, text),
      onRetry: state.retryable ? onRetry : void 0
    }
  ) : null
};
function ArtifactPreviewShell({
  filename,
  fill,
  state,
  onLoad,
  onRetry
}) {
  const text = useAgentWorkspaceText().artifact;
  return shells[state.status]({ filename, fill, state, onLoad, onRetry, text });
}
function previewErrorMessage(code2, text) {
  const messages = {
    generation_failed: text.generationFailed,
    authorization_required: text.downloadNotAuthorized,
    loading_unavailable: text.loadingUnavailable,
    load_failed: text.loadFailed
  };
  return messages[code2];
}
const strategies = /* @__PURE__ */ new Map();
function registerArtifactPreviewStrategy(strategy) {
  for (const kind of strategy.kinds) {
    strategies.set(kind, strategy);
  }
}
function artifactPreviewStrategy(kind) {
  return strategies.get(kind);
}
function ArtifactAudioPreview({
  filename,
  fill = false,
  src
}) {
  const text = useAgentWorkspaceText().artifact;
  return /* @__PURE__ */ jsx(
    "div",
    {
      className: `${previewPaneClass(
        fill,
        "border-b border-border bg-muted/30 p-4",
        "shell"
      )} bg-muted/30 p-4`,
      children: /* @__PURE__ */ jsx(
        "audio",
        {
          "aria-label": text.audioPreview(filename),
          className: "w-full",
          controls: true,
          preload: "metadata",
          src
        }
      )
    }
  );
}
function ArtifactCodePreview({
  filename,
  fill,
  text
}) {
  const title = useAgentWorkspaceText().artifact.preview(filename);
  if (text === null) return null;
  return /* @__PURE__ */ jsx(
    "pre",
    {
      "aria-label": title,
      className: `${previewPaneClass(
        fill,
        "max-h-80 overflow-auto border-b border-border bg-muted/40 p-4"
      )} bg-muted/40 p-4 text-xs leading-5`,
      children: /* @__PURE__ */ jsx("code", { children: text })
    }
  );
}
async function loadBlobPreviewSource(runtime, item, sessionId) {
  if (!runtime.loadArtifact) {
    throw new Error("Artifact loading is unavailable");
  }
  const blob = item.selectedRepresentationId ? await runtime.loadArtifact(sessionId, item.artifactId, item.selectedRepresentationId) : await runtime.loadArtifact(sessionId, item.artifactId);
  return { url: null, blob };
}
async function loadLivePreviewSource(runtime, item, sessionId) {
  if (runtime.loadPreviewUrl) {
    try {
      const url = await runtime.loadPreviewUrl(item, sessionId);
      if (url && await livePreviewIsReady(url)) return { url, blob: null };
    } catch {
    }
  }
  return loadBlobPreviewSource(runtime, item, sessionId);
}
async function livePreviewIsReady(url) {
  if (typeof fetch !== "function") return true;
  try {
    const response = await fetch(url, {
      cache: "no-store",
      credentials: "omit",
      method: "HEAD"
    });
    return response.ok;
  } catch {
    return true;
  }
}
function useArtifactBlobUrl(item, runtime, sessionId, enabled = true, attempt = 0) {
  const [state, setState] = useState(
    enabled ? { status: "loading" } : { status: "idle" }
  );
  useEffect(() => {
    if (item.status === "failed") {
      setState({
        status: "error",
        code: "generation_failed",
        retryable: false,
        message: "Artifact generation failed"
      });
      return;
    }
    if (!enabled) {
      setState({ status: "idle" });
      return;
    }
    if (item.status !== "completed") {
      setState({ status: "loading" });
      return;
    }
    if (!artifactActionAllowed(
      item,
      "artifact.download",
      item.selectedRepresentationId ?? void 0
    )) {
      setState({
        status: "error",
        code: "authorization_required",
        retryable: false,
        message: "artifact_download_not_authorized"
      });
      return;
    }
    if (!runtime.loadArtifact && !runtime.loadPreviewUrl) {
      setState({
        status: "error",
        code: "loading_unavailable",
        retryable: false,
        message: "Artifact loading is unavailable"
      });
      return;
    }
    let active = true;
    let objectUrl = null;
    setState({ status: "loading" });
    const strategy = artifactPreviewStrategy(
      artifactPresentation(item.mimeType, item.filename).kind
    );
    const load = strategy?.load ?? loadBlobPreviewSource;
    void load(runtime, item, sessionId).then(async ({ url, blob }) => {
      if (!active) return;
      if (url) {
        setState({
          status: "ready",
          url,
          mimeType: item.mimeType || null,
          text: null,
          textTruncated: false
        });
        return;
      }
      if (!blob) throw new Error("artifact_preview_empty");
      const previewBlob = typedPreviewBlob(blob, item);
      objectUrl = URL.createObjectURL(previewBlob);
      const mimeType = previewBlob.type || item.mimeType || null;
      const resolved = artifactPreviewStrategy(
        artifactPresentation(mimeType, item.filename).kind
      );
      const readsText = resolved?.needsText ?? false;
      const text = readsText ? await readBlobText(blob.slice(0, MAX_TEXT_PREVIEW_BYTES)) : null;
      if (!active) return;
      setState({
        status: "ready",
        url: objectUrl,
        mimeType,
        text,
        textTruncated: readsText && blob.size > MAX_TEXT_PREVIEW_BYTES
      });
    }).catch((cause) => {
      if (!active) return;
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
        objectUrl = null;
      }
      console.error("Artifact loading failed", cause);
      setState({
        status: "error",
        code: "load_failed",
        retryable: true,
        message: cause instanceof Error ? cause.message : String(cause)
      });
    });
    return () => {
      active = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [
    attempt,
    enabled,
    item.artifactId,
    item.filename,
    item.mimeType,
    item.revision,
    item.selectedRepresentationId,
    item.status,
    runtime,
    sessionId
  ]);
  return state;
}
const MAX_TEXT_PREVIEW_BYTES = 2 << 20;
function typedPreviewBlob(blob, item) {
  const kind = artifactPresentation(blob.type || item.mimeType, item.filename).kind;
  if (kind === "svg" && blob.type !== "image/svg+xml") {
    return new Blob([blob], { type: "image/svg+xml" });
  }
  return blob;
}
function readBlobText(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => resolve(String(reader.result ?? "")));
    reader.addEventListener("error", () => reject(reader.error));
    reader.readAsText(blob);
  });
}
const MAX_COLUMNS = 30;
const MAX_ROWS = 200;
function ArtifactCsvPreview({
  filename,
  fill = false,
  text,
  truncated
}) {
  const labels = useAgentWorkspaceText().artifact;
  const parsed = Papa.parse(text, {
    skipEmptyLines: "greedy"
  });
  if (parsed.errors.length > 0 || parsed.data.length === 0) {
    return /* @__PURE__ */ jsx(
      "div",
      {
        className: `${previewPaneClass(
          fill,
          "border-b border-destructive/30 bg-destructive/5 p-4",
          "shell"
        )} border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive`,
        role: "alert",
        children: labels.csvPreviewFailed(filename)
      }
    );
  }
  const rows = parsed.data.slice(0, MAX_ROWS + 1);
  const columnCount = Math.min(
    MAX_COLUMNS,
    rows.reduce((maximum, row) => Math.max(maximum, row.length), 0)
  );
  return /* @__PURE__ */ jsxs("div", { className: previewPaneClass(fill, "max-h-[32rem] overflow-auto border-b border-border"), children: [
    /* @__PURE__ */ jsxs("table", { "aria-label": labels.csvPreview(filename), className: "min-w-full border-collapse text-left text-xs", children: [
      /* @__PURE__ */ jsx("thead", { className: "sticky top-0 z-10 bg-muted", children: /* @__PURE__ */ jsx("tr", { children: rows[0].slice(0, columnCount).map((cell, index) => /* @__PURE__ */ jsx("th", { className: "border border-border px-2 py-1.5 font-medium", children: cell || `Column ${index + 1}` }, `${index}:${cell}`)) }) }),
      /* @__PURE__ */ jsx("tbody", { children: rows.slice(1).map((row, rowIndex) => /* @__PURE__ */ jsx("tr", { children: Array.from({ length: columnCount }, (_, columnIndex) => /* @__PURE__ */ jsx("td", { className: "max-w-72 border border-border px-2 py-1.5 align-top", children: /* @__PURE__ */ jsx("span", { className: "line-clamp-3 break-words", children: row[columnIndex] ?? "" }) }, columnIndex)) }, rowIndex)) })
    ] }),
    (parsed.data.length > MAX_ROWS + 1 || columnCount === MAX_COLUMNS) && /* @__PURE__ */ jsxs("div", { className: "border-t border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground", children: [
      "Preview limited to ",
      MAX_ROWS,
      " rows and ",
      MAX_COLUMNS,
      " columns."
    ] }),
    truncated && /* @__PURE__ */ jsx("div", { className: "border-t border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground", children: labels.previewLimited(MAX_TEXT_PREVIEW_BYTES) })
  ] });
}
const STATIC_HTML_SANDBOX = "";
const STATIC_HTML_REFERRER_POLICY = "no-referrer";
const STATIC_HTML_CSP = [
  "default-src 'none'",
  "img-src data: blob:",
  "media-src data: blob:",
  "style-src 'unsafe-inline'",
  "script-src 'none'",
  "font-src data:",
  "connect-src 'none'",
  "frame-src 'none'",
  "worker-src 'none'",
  "object-src 'none'",
  "form-action 'none'",
  "base-uri 'none'"
].join("; ");
function staticHtmlDocument(html2) {
  const parsed = new DOMParser().parseFromString(html2, "text/html");
  parsed.querySelectorAll("base").forEach((element) => element.remove());
  parsed.querySelectorAll("meta").forEach((element) => {
    const name = element.name.toLowerCase();
    const httpEquiv = element.httpEquiv.toLowerCase();
    if (name === "referrer" || httpEquiv === "referrer" || httpEquiv === "content-security-policy" || httpEquiv === "refresh") {
      element.remove();
    }
  });
  const csp = parsed.createElement("meta");
  csp.httpEquiv = "Content-Security-Policy";
  csp.content = STATIC_HTML_CSP;
  const referrer = parsed.createElement("meta");
  referrer.name = "referrer";
  referrer.content = STATIC_HTML_REFERRER_POLICY;
  const base = parsed.createElement("base");
  base.target = "_blank";
  parsed.head.prepend(csp, referrer, base);
  return `<!doctype html>${parsed.documentElement.outerHTML}`;
}
function openStaticHtmlInNewWindow(html2, title = "", opener = window) {
  const shell = opener.open("about:blank", "_blank");
  if (!shell) {
    return { opened: false, reason: "popup-blocked" };
  }
  shell.opener = null;
  const frame = shell.document.createElement("iframe");
  frame.setAttribute("sandbox", STATIC_HTML_SANDBOX);
  frame.setAttribute("referrerpolicy", STATIC_HTML_REFERRER_POLICY);
  frame.srcdoc = staticHtmlDocument(html2);
  frame.style.cssText = "border:0;height:100vh;width:100%";
  shell.document.title = title;
  shell.document.body.style.margin = "0";
  shell.document.body.replaceChildren(frame);
  return { opened: true };
}
function ArtifactHtmlPreview({
  filename,
  fill,
  url,
  text
}) {
  const title = useAgentWorkspaceText().artifact.preview(filename);
  const liveUrl = isLivePreviewUrl(url) ? url : null;
  return /* @__PURE__ */ jsx(
    "iframe",
    {
      className: previewPaneClass(
        fill,
        "aspect-[16/10] min-h-80 w-full border-b border-border bg-white",
        "frame"
      ),
      referrerPolicy: STATIC_HTML_REFERRER_POLICY,
      sandbox: STATIC_HTML_SANDBOX,
      src: liveUrl ?? void 0,
      srcDoc: liveUrl ? void 0 : staticHtmlDocument(text ?? ""),
      title
    }
  );
}
function isLivePreviewUrl(url) {
  return url.startsWith("http://") || url.startsWith("https://") || url.startsWith("/");
}
function ArtifactImagePreview({ filename, fill, url }) {
  return /* @__PURE__ */ jsx(
    "img",
    {
      alt: filename,
      className: previewPaneClass(
        fill,
        "max-h-96 w-full bg-muted object-contain",
        "media"
      ),
      src: url
    }
  );
}
const FORBIDDEN_TAGS = /* @__PURE__ */ new Set([
  "embed",
  "foreignobject",
  "iframe",
  "link",
  "meta",
  "object",
  "script"
]);
function sanitizeSvg(markup) {
  const parsed = new DOMParser().parseFromString(markup.trim(), "image/svg+xml");
  if (parsed.querySelector("parsererror")) return null;
  const root = parsed.documentElement;
  if (root.localName.toLowerCase() !== "svg") return null;
  sanitizeElement(root);
  if (root.localName.toLowerCase() !== "svg") return null;
  return root.outerHTML;
}
function sanitizeElement(element) {
  if (FORBIDDEN_TAGS.has(element.localName.toLowerCase())) {
    element.remove();
    return;
  }
  for (const attribute of [...element.attributes]) {
    if (isUnsafeAttribute(attribute.name, attribute.value)) {
      element.removeAttribute(attribute.name);
    }
  }
  for (const child of [...element.children]) {
    sanitizeElement(child);
  }
}
function isUnsafeAttribute(name, value) {
  const key = name.toLowerCase();
  if (key.startsWith("on") || key === "href" || key.endsWith(":href")) {
    return key.startsWith("on") || isUnsafeUrl(value);
  }
  return false;
}
function isUnsafeUrl(value) {
  const url = value.trim().toLowerCase();
  return url.startsWith("javascript:") || url.startsWith("data:text/html") || url.startsWith("data:application/");
}
function SvgGraphic({
  fill = false,
  markup,
  surface = "paper"
}) {
  const hostRef = useRef(null);
  const sanitized = sanitizeSvg(markup);
  const background = surface === "canvas" ? "bg-background" : "bg-white";
  useEffect(() => {
    const host = hostRef.current;
    if (!host || !sanitized) return;
    const root = host.shadowRoot ?? host.attachShadow({ mode: "open" });
    root.innerHTML = `<style>svg{display:block;height:auto;max-width:100%}</style>${sanitized}`;
    return () => {
      root.innerHTML = "";
    };
  }, [sanitized]);
  if (!sanitized) return null;
  return /* @__PURE__ */ jsx(
    "div",
    {
      className: `${previewPaneClass(
        fill,
        `max-h-[32rem] overflow-auto border-b border-border ${background} p-4`,
        "scroll"
      )} ${background} p-4`,
      "data-svg-graphic": "",
      "data-svg-surface": surface,
      ref: hostRef
    }
  );
}
function ArtifactSvgPreview({
  filename,
  fill,
  text
}) {
  const labels = useAgentWorkspaceText().artifact;
  const svg2 = text ? sanitizeSvg(text) : null;
  if (!svg2) {
    return /* @__PURE__ */ jsx(ArtifactPreviewError, { filename, fill, message: labels.loadFailed });
  }
  return /* @__PURE__ */ jsx(SvgGraphic, { fill, markup: svg2 });
}
const DARK_THEME = /\b(dark|solarized-dark)\b/;
function documentColorScheme() {
  if (typeof document === "undefined") return "light";
  return DARK_THEME.test(document.documentElement.className) ? "dark" : "light";
}
function useDocumentColorScheme() {
  const [scheme, setScheme] = useState(documentColorScheme);
  useEffect(() => {
    const root = document.documentElement;
    const sync = () => setScheme(documentColorScheme());
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributes: true, attributeFilter: ["class"] });
    sync();
    return () => observer.disconnect();
  }, []);
  return scheme;
}
let mermaidApi;
function loadMermaid() {
  mermaidApi ??= import("mermaid").then((module) => module.default);
  return mermaidApi.then((api) => {
    api.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: documentColorScheme() === "dark" ? "dark" : "neutral"
    });
    return api;
  });
}
function MermaidDiagram({
  filename = "mermaid",
  fill = false,
  source
}) {
  const labels = useAgentWorkspaceText().artifact;
  const scheme = useDocumentColorScheme();
  const renderId = `mermaid-${useId().replace(/:/g, "")}`;
  const [svg2, setSvg] = useState(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let active = true;
    setSvg(null);
    setFailed(false);
    void loadMermaid().then((api) => api.render(renderId, source)).then(({ svg: markup }) => {
      if (active) setSvg(markup);
    }).catch((cause) => {
      if (!active) return;
      console.error("Mermaid render failed", cause);
      setFailed(true);
    });
    return () => {
      active = false;
    };
  }, [renderId, scheme, source]);
  if (failed) {
    return /* @__PURE__ */ jsx(
      ArtifactPreviewError,
      {
        filename,
        fill,
        message: labels.diagramRenderFailed
      }
    );
  }
  if (!svg2) {
    return /* @__PURE__ */ jsxs(
      "div",
      {
        className: `${previewPaneClass(
          fill,
          "flex min-h-24 items-center justify-center gap-2 border-b border-border bg-muted/30 p-4",
          "shell"
        )} gap-2 bg-muted/30 p-4 text-sm text-muted-foreground`,
        role: "status",
        children: [
          /* @__PURE__ */ jsx(Loader2, { className: "size-4 animate-spin" }),
          labels.loading(filename)
        ]
      }
    );
  }
  return /* @__PURE__ */ jsx(SvgGraphic, { fill, markup: svg2, surface: "canvas" });
}
function ArtifactMermaidPreview({
  filename,
  fill,
  text
}) {
  const labels = useAgentWorkspaceText().artifact;
  if (!text?.trim()) {
    return /* @__PURE__ */ jsx(ArtifactPreviewError, { filename, fill, message: labels.loadFailed });
  }
  return /* @__PURE__ */ jsx(MermaidDiagram, { filename, fill, source: text });
}
function markdownImageSource(src) {
  if (!src) return void 0;
  return /^(blob:|data:image\/)/i.test(src) ? src : void 0;
}
function MarkdownCode({ children, className }) {
  if (fencedLanguage(className) === "mermaid") {
    return /* @__PURE__ */ jsx(MermaidDiagram, { source: fencedSource(children) });
  }
  if (className) {
    return /* @__PURE__ */ jsx("code", { className: `${className} font-mono text-[13px]`, children });
  }
  return /* @__PURE__ */ jsx("code", { className: "rounded bg-muted px-1.5 py-0.5 font-mono text-[0.9em]", children });
}
function MarkdownPre({ children }) {
  const only = Children.toArray(children)[0];
  if (isValidElement(only) && only.type === MermaidDiagram) {
    return only;
  }
  return /* @__PURE__ */ jsx("pre", { className: "my-3 overflow-x-auto rounded-md border border-border bg-muted/50 p-3 text-foreground", children });
}
function fencedLanguage(className) {
  return className?.replace(/^language-/, "") ?? "";
}
function fencedSource(children) {
  return String(children ?? "").replace(/\n$/, "");
}
function markdownArtifactReference(source, artifacts, origin) {
  const path = markdownWorkspaceOutputPath(source, origin);
  if (!path) return void 0;
  return artifacts.find((artifact) => artifact.artifactId === `workspace:${path}`);
}
function markdownWorkspaceOutputPath(source, origin) {
  if (!source || source.startsWith("//")) return void 0;
  try {
    const raw = decodeURIComponent(source.split(/[?#]/, 1)[0]);
    if (raw.split("/").some((part) => part === "..")) return void 0;
  } catch {
    return void 0;
  }
  let value = source;
  if (/^[a-z][a-z0-9+.-]*:/i.test(source)) {
    if (!origin) return void 0;
    try {
      const url = new URL(source);
      if (!/^https?:$/.test(url.protocol) || url.origin !== origin) return void 0;
      value = url.pathname;
    } catch {
      return void 0;
    }
  }
  try {
    value = decodeURIComponent(value.split(/[?#]/, 1)[0]);
  } catch {
    return void 0;
  }
  if (/[\\\u0000]/.test(value)) return void 0;
  const parts = value.split("/").filter((part) => part !== "" && part !== ".");
  if (parts.some((part) => part === "..")) return void 0;
  if (value.startsWith("/") && !outputRoots.has(parts[0]) && outputRoots.has(parts[1])) {
    parts.shift();
  }
  return parts.length > 1 && outputRoots.has(parts[0]) ? parts.join("/") : void 0;
}
const outputRoots = /* @__PURE__ */ new Set(["artifacts", "deliverables", "output", "outputs"]);
const MarkdownArtifactContext = createContext(null);
function MarkdownArtifactProvider({
  children,
  runtime,
  snapshot
}) {
  const value = useMemo(() => ({
    runtime,
    sessionId: snapshot.sessionId,
    artifacts: snapshot.items.filter((item) => item.kind === "artifact")
  }), [runtime, snapshot]);
  return /* @__PURE__ */ jsx(MarkdownArtifactContext.Provider, { value, children });
}
function useMarkdownArtifact(source) {
  const context = useContext(MarkdownArtifactContext);
  if (!context) return void 0;
  const origin = typeof window === "undefined" ? void 0 : window.location.origin;
  const artifact = markdownArtifactReference(
    source,
    context.artifacts,
    origin
  );
  return artifact ? { artifact, runtime: context.runtime, sessionId: context.sessionId } : void 0;
}
function MarkdownArtifactReference({
  artifact,
  image: image2 = false,
  label,
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText().artifact;
  const [attempt, setAttempt] = useState(0);
  const [failedImage, setFailedImage] = useState();
  const safeBlobPreview = isSafeBlobPreview(artifact.mimeType, artifact.filename);
  const state = useArtifactBlobUrl(artifact, runtime, sessionId, safeBlobPreview, attempt);
  if (!safeBlobPreview) {
    return /* @__PURE__ */ jsx("span", { children: label || artifact.filename });
  }
  if (state.status === "error" || state.status === "ready" && failedImage === state.url) {
    const retryable = state.status === "ready" || state.retryable;
    return /* @__PURE__ */ jsxs("span", { className: "inline-flex items-center gap-2 text-destructive", role: "alert", children: [
      text.loadFailed,
      retryable && /* @__PURE__ */ jsx("button", { className: "underline underline-offset-4", onClick: () => setAttempt((value) => value + 1), type: "button", children: text.retry(artifact.filename) })
    ] });
  }
  if (state.status !== "ready") {
    return /* @__PURE__ */ jsx("span", { className: "text-muted-foreground", role: "status", children: text.loading(artifact.filename) });
  }
  if (!isSafeBlobPreview(state.mimeType, artifact.filename)) return /* @__PURE__ */ jsx("span", { children: label || artifact.filename });
  return /* @__PURE__ */ jsx("a", { className: "font-medium text-primary underline underline-offset-4", href: state.url, rel: "noopener noreferrer", target: "_blank", children: image2 ? /* @__PURE__ */ jsx("img", { alt: typeof label === "string" ? label : artifact.filename, className: "max-h-96 max-w-full object-contain", onError: () => setFailedImage(state.url), src: state.url }) : label || artifact.filename });
}
function isSafeBlobPreview(mimeType, filename) {
  const normalizedMime = mimeType?.split(";", 1)[0].trim().toLowerCase() || null;
  const { kind } = artifactPresentation(normalizedMime, filename);
  return kind === "image" || kind === "audio" || kind === "video" || kind === "pdf";
}
const markdownUrlTransform = (url, key) => key === "src" ? url : defaultUrlTransform(url);
function MarkdownImage({ src, alt }) {
  const source = typeof src === "string" ? src : void 0;
  const artifact = useMarkdownArtifact(source);
  if (artifact) return /* @__PURE__ */ jsx(MarkdownArtifactReference, { ...artifact, image: true, label: alt });
  if (isWorkspaceOutput(source)) return /* @__PURE__ */ jsx("span", { children: alt || source });
  const inlineSource = markdownImageSource(source);
  if (inlineSource) {
    return /* @__PURE__ */ jsx("img", { src: inlineSource, alt });
  }
  if (source && /^https?:\/\//i.test(source)) {
    return /* @__PURE__ */ jsx(
      "a",
      {
        className: "font-medium text-primary underline underline-offset-4",
        href: source,
        referrerPolicy: "no-referrer",
        rel: "noopener noreferrer",
        target: "_blank",
        children: alt || source
      }
    );
  }
  return /* @__PURE__ */ jsx("span", { children: alt || source });
}
function MarkdownLink({ children, href, ...props }) {
  const artifact = useMarkdownArtifact(href);
  if (artifact) return /* @__PURE__ */ jsx(MarkdownArtifactReference, { ...artifact, label: children });
  if (isWorkspaceOutput(href)) return /* @__PURE__ */ jsx("span", { children });
  return /* @__PURE__ */ jsx("a", { ...props, className: "font-medium text-primary underline underline-offset-4", href, rel: "noreferrer", target: "_blank", children });
}
function isWorkspaceOutput(source) {
  return Boolean(markdownWorkspaceOutputPath(
    source,
    typeof window === "undefined" ? void 0 : window.location.origin
  ));
}
function MarkdownMessage({ text }) {
  return /* @__PURE__ */ jsx("div", { className: "min-w-0 text-sm leading-6", children: /* @__PURE__ */ jsx(
    ReactMarkdown,
    {
      components: {
        a: MarkdownLink,
        blockquote: ({ children }) => /* @__PURE__ */ jsx("blockquote", { className: "my-3 border-l-2 border-border pl-3 text-muted-foreground", children }),
        code: MarkdownCode,
        h1: ({ children }) => /* @__PURE__ */ jsx("h1", { className: "mb-2 mt-5 text-xl font-semibold first:mt-0", children }),
        h2: ({ children }) => /* @__PURE__ */ jsx("h2", { className: "mb-2 mt-5 text-lg font-semibold first:mt-0", children }),
        h3: ({ children }) => /* @__PURE__ */ jsx("h3", { className: "mb-1.5 mt-4 text-base font-semibold first:mt-0", children }),
        img: MarkdownImage,
        li: ({ children }) => /* @__PURE__ */ jsx("li", { className: "ml-5 list-disc pl-1", children }),
        ol: ({ children }) => /* @__PURE__ */ jsx("ol", { className: "my-3 space-y-1", children }),
        p: ({ children }) => /* @__PURE__ */ jsx("p", { className: "my-2 first:mt-0 last:mb-0", children }),
        pre: MarkdownPre,
        table: ({ children }) => /* @__PURE__ */ jsx("div", { className: "my-3 overflow-x-auto", children: /* @__PURE__ */ jsx("table", { className: "w-full border-collapse text-left text-sm", children }) }),
        td: ({ children }) => /* @__PURE__ */ jsx("td", { className: "border border-border px-2 py-1.5", children }),
        th: ({ children }) => /* @__PURE__ */ jsx("th", { className: "border border-border bg-muted px-2 py-1.5 font-medium", children }),
        ul: ({ children }) => /* @__PURE__ */ jsx("ul", { className: "my-3 space-y-1", children })
      },
      remarkPlugins: [remarkGfm],
      urlTransform: markdownUrlTransform,
      children: text
    }
  ) });
}
function ArtifactMarkdownPreview({
  fill = false,
  text,
  truncated
}) {
  const labels = useAgentWorkspaceText().artifact;
  return /* @__PURE__ */ jsxs("div", { className: `${previewPaneClass(fill, "max-h-[32rem] overflow-auto border-b border-border")} p-4`, children: [
    /* @__PURE__ */ jsx(MarkdownMessage, { text }),
    truncated && /* @__PURE__ */ jsx("div", { className: "mt-4 border-t border-border pt-3 text-xs text-muted-foreground", children: labels.previewLimited(MAX_TEXT_PREVIEW_BYTES) })
  ] });
}
function LazyArtifactPdfPreview({
  filename,
  fill = false,
  src
}) {
  const text = useAgentWorkspaceText().artifact;
  const [state, setState] = useState({ status: "loading" });
  useEffect(() => {
    let active = true;
    void import("./ArtifactPdfPreview-nczGEI45.js").then(
      ({ ArtifactPdfPreview }) => {
        if (active) setState({ status: "ready", Preview: ArtifactPdfPreview });
      },
      (cause) => {
        if (!active) return;
        console.error("PDF preview failed", cause);
        setState({ status: "error" });
      }
    );
    return () => {
      active = false;
    };
  }, []);
  if (state.status === "loading") {
    return /* @__PURE__ */ jsxs(
      "div",
      {
        className: `${previewPaneClass(
          fill,
          "flex h-[32rem] items-center justify-center border-b border-border bg-muted/30",
          "shell"
        )} gap-2 bg-muted/30 text-sm text-muted-foreground`,
        role: "status",
        children: [
          /* @__PURE__ */ jsx(Loader2, { className: "size-4 animate-spin" }),
          text.loading(filename)
        ]
      }
    );
  }
  if (state.status === "error") {
    return /* @__PURE__ */ jsxs(
      "div",
      {
        className: `${previewPaneClass(
          fill,
          "flex h-[32rem] items-center justify-center border-b border-destructive/30 bg-destructive/5",
          "shell"
        )} gap-2 border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive`,
        role: "alert",
        children: [
          /* @__PURE__ */ jsx(AlertCircle, { className: "size-4 shrink-0" }),
          text.loadFailed
        ]
      }
    );
  }
  return /* @__PURE__ */ jsx(state.Preview, { filename, fill, src });
}
function ArtifactVideoPreview({
  filename,
  fill,
  url,
  onRetry
}) {
  const text = useAgentWorkspaceText().artifact;
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    setFailed(false);
  }, [url]);
  if (failed) {
    return /* @__PURE__ */ jsx(
      ArtifactPreviewError,
      {
        filename,
        fill,
        message: text.videoPlaybackFailed(filename),
        onRetry: () => {
          setFailed(false);
          onRetry();
        }
      }
    );
  }
  return /* @__PURE__ */ jsx(
    "video",
    {
      "aria-label": text.videoPreview(filename),
      className: previewPaneClass(
        fill,
        "max-h-96 w-full bg-muted object-contain",
        "media"
      ),
      controls: true,
      onError: () => setFailed(true),
      playsInline: true,
      preload: "metadata",
      src: url
    }
  );
}
const html = {
  kinds: ["html"],
  load: loadLivePreviewSource,
  needsText: true,
  render: (context) => /* @__PURE__ */ jsx(ArtifactHtmlPreview, { ...context })
};
const svg = {
  kinds: ["svg"],
  load: loadBlobPreviewSource,
  needsText: true,
  render: (context) => /* @__PURE__ */ jsx(ArtifactSvgPreview, { ...context })
};
const mermaid = {
  kinds: ["mermaid"],
  load: loadBlobPreviewSource,
  needsText: true,
  render: (context) => /* @__PURE__ */ jsx(ArtifactMermaidPreview, { ...context })
};
const video = {
  kinds: ["video"],
  load: loadLivePreviewSource,
  needsText: false,
  render: (context) => /* @__PURE__ */ jsx(ArtifactVideoPreview, { ...context })
};
const audio = {
  kinds: ["audio"],
  load: loadLivePreviewSource,
  needsText: false,
  render: (context) => /* @__PURE__ */ jsx(ArtifactAudioPreview, { filename: context.filename, fill: context.fill, src: context.url })
};
const image = {
  kinds: ["image"],
  load: loadBlobPreviewSource,
  needsText: false,
  render: (context) => /* @__PURE__ */ jsx(ArtifactImagePreview, { ...context })
};
const markdown = {
  kinds: ["markdown"],
  load: loadBlobPreviewSource,
  needsText: true,
  render: ({ fill, text, textTruncated }) => text === null ? null : /* @__PURE__ */ jsx(ArtifactMarkdownPreview, { fill, text, truncated: textTruncated })
};
const csv = {
  kinds: ["csv"],
  load: loadBlobPreviewSource,
  needsText: true,
  render: ({ filename, fill, text, textTruncated }) => text === null ? null : /* @__PURE__ */ jsx(
    ArtifactCsvPreview,
    {
      filename,
      fill,
      text,
      truncated: textTruncated
    }
  )
};
const pdf = {
  kinds: ["pdf"],
  load: loadBlobPreviewSource,
  needsText: false,
  render: ({ filename, fill, url }) => /* @__PURE__ */ jsx(LazyArtifactPdfPreview, { filename, fill, src: url })
};
const code = {
  kinds: ["code", "text"],
  load: loadBlobPreviewSource,
  needsText: true,
  render: (context) => /* @__PURE__ */ jsx(ArtifactCodePreview, { ...context })
};
for (const strategy of [html, svg, mermaid, video, audio, image, markdown, csv, pdf, code]) {
  registerArtifactPreviewStrategy(strategy);
}
function ArtifactPreview({
  filename,
  fill = false,
  kind,
  state,
  onLoad,
  onRetry
}) {
  if (state.status !== "ready") {
    return /* @__PURE__ */ jsx(
      ArtifactPreviewShell,
      {
        filename,
        fill,
        state,
        onLoad,
        onRetry
      }
    );
  }
  return artifactPreviewStrategy(kind)?.render({
    filename,
    fill,
    url: state.url,
    mimeType: state.mimeType,
    text: state.text,
    textTruncated: state.textTruncated,
    onRetry
  }) ?? null;
}
function GenericArtifactCard({
  filename,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const declaredType = artifactPresentation(item.mimeType, filename);
  const [attempt, setAttempt] = useState(0);
  const [sourceDownloadError, setSourceDownloadError] = useState(null);
  const [sourceDownloading, setSourceDownloading] = useState(false);
  const state = useArtifactBlobUrl(item, runtime, sessionId, true, attempt);
  const selectedRepresentation = item.representations.find(
    (representation) => representation.representationId === item.selectedRepresentationId
  );
  const sourceRepresentation = item.representations.find(
    (representation) => representation.representationId === "original" && representation.status === "ready"
  );
  const loadedFilename = selectedRepresentation?.filename || filename;
  const sourceFilename = sourceRepresentation?.filename || filename;
  const canDownloadSource = Boolean(runtime.loadArtifact && sourceRepresentation) && artifactActionAllowed(
    item,
    "artifact.download",
    sourceRepresentation?.representationId
  ) && sourceRepresentation?.representationId !== item.selectedRepresentationId;
  const type = state.status === "ready" ? artifactPresentation(state.mimeType, filename) : declaredType;
  const load = () => {
    setAttempt((value) => value + 1);
  };
  const downloadSource = async () => {
    if (!runtime.loadArtifact || !sourceRepresentation) return;
    setSourceDownloadError(null);
    setSourceDownloading(true);
    try {
      const blob = await runtime.loadArtifact(
        sessionId,
        item.artifactId,
        sourceRepresentation.representationId
      );
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.download = sourceFilename;
      link.href = url;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (cause) {
      console.error("Artifact source download failed", cause);
      setSourceDownloadError(
        presentation === "user" ? text.artifact.loadFailed : cause instanceof Error ? cause.message : String(cause)
      );
    } finally {
      setSourceDownloading(false);
    }
  };
  return /* @__PURE__ */ jsxs(
    "article",
    {
      className: fill ? "flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-card" : "overflow-hidden rounded-md border border-border bg-card",
      children: [
        /* @__PURE__ */ jsx(
          ArtifactPreview,
          {
            filename,
            fill,
            kind: type.kind,
            onLoad: load,
            onRetry: load,
            state
          }
        ),
        /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 min-w-0 items-center gap-3 px-3 py-2.5", children: [
          /* @__PURE__ */ jsx(ArtifactTypeIcon, { kind: type.kind }),
          /* @__PURE__ */ jsxs("div", { className: "min-w-0 flex-1", children: [
            /* @__PURE__ */ jsx("div", { className: "truncate text-sm font-medium", children: filename }),
            /* @__PURE__ */ jsx("div", { className: "truncate text-xs text-muted-foreground", children: text.artifactType(type.label, type.label) })
          ] }),
          state.status === "ready" && type.kind !== "file" && type.kind !== "html" && type.kind !== "svg" && type.kind !== "mermaid" && /* @__PURE__ */ jsx(
            "a",
            {
              "aria-label": text.artifact.open(filename),
              className: "inline-flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              href: state.url,
              rel: "noreferrer",
              target: "_blank",
              children: /* @__PURE__ */ jsx(ExternalLink, { className: "size-4" })
            }
          ),
          canDownloadSource && /* @__PURE__ */ jsx(
            "button",
            {
              "aria-label": text.artifact.download(sourceFilename),
              className: "inline-flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
              disabled: sourceDownloading,
              onClick: () => void downloadSource(),
              type: "button",
              children: sourceDownloading ? /* @__PURE__ */ jsx(Loader2, { className: "size-4 animate-spin" }) : /* @__PURE__ */ jsx(FileDown, { className: "size-4" })
            }
          ),
          state.status === "ready" && /* @__PURE__ */ jsx(
            "a",
            {
              "aria-label": text.artifact.download(loadedFilename),
              className: "inline-flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              download: loadedFilename,
              href: state.url,
              children: /* @__PURE__ */ jsx(Download, { className: "size-4" })
            }
          )
        ] }),
        sourceDownloadError && /* @__PURE__ */ jsx("div", { className: "border-t border-destructive/20 px-3 py-2 text-xs text-destructive", role: "alert", children: sourceDownloadError })
      ]
    }
  );
}
function ArtifactError({
  filename,
  message
}) {
  return /* @__PURE__ */ jsxs(
    "article",
    {
      className: "rounded-md border border-destructive/30 bg-destructive/5 px-3 py-3 text-sm",
      role: "alert",
      children: [
        /* @__PURE__ */ jsx("div", { className: "truncate font-medium", children: filename }),
        /* @__PURE__ */ jsx("div", { className: "text-xs text-destructive", children: message })
      ]
    }
  );
}
function ArtifactTypeIcon({ kind }) {
  const Icon = artifactKindIcon(kind);
  return /* @__PURE__ */ jsx(Icon, { className: "size-5 shrink-0 text-muted-foreground" });
}
function ArtifactCard({
  contentRenderers,
  fill = false,
  item,
  presentation = "developer",
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const filename = item.filename.trim() || text.artifact.generatedArtifact;
  const unsupportedReason = artifactUnsupportedReason(item);
  if (unsupportedReason) {
    return /* @__PURE__ */ jsx(
      ArtifactError,
      {
        filename,
        message: presentation === "user" ? text.artifact.loadFailed : unsupportedReason
      }
    );
  }
  const key = artifactRendererKey(item);
  const RegisteredViewer = key ? contentRenderers?.lookup(key)?.viewer : void 0;
  if (RegisteredViewer) {
    return /* @__PURE__ */ jsx(
      RegisteredViewer,
      {
        filename,
        fill,
        item,
        presentation,
        runtime,
        sessionId
      }
    );
  }
  return /* @__PURE__ */ jsx(
    GenericArtifactCard,
    {
      filename,
      fill,
      item,
      presentation,
      runtime,
      sessionId
    }
  );
}
function JsonContentPreview({ content }) {
  return /* @__PURE__ */ jsxs("figure", { className: "my-2 min-w-0 rounded-md border border-border", "aria-label": "JSON", children: [
    /* @__PURE__ */ jsxs("figcaption", { className: "border-b border-border px-3 py-1 text-xs text-muted-foreground", children: [
      "JSON",
      content.identity?.semanticKey ? ` · ${content.identity.semanticKey}` : ""
    ] }),
    /* @__PURE__ */ jsx("pre", { className: "max-h-96 overflow-auto p-3 text-xs", "data-json-valid": content.valid, children: /* @__PURE__ */ jsx("code", { children: content.valid ? JSON.stringify(content.value, null, 2) : content.raw }) })
  ] });
}
function KnowledgeAnswerSources({
  showDisclaimer,
  sources
}) {
  const text = useAgentWorkspaceText().knowledgeAnswer;
  if (sources.length === 0 && !showDisclaimer) return null;
  return /* @__PURE__ */ jsxs("aside", { className: "mt-3 space-y-2", children: [
    sources.length > 0 && /* @__PURE__ */ jsxs("div", { className: "rounded-md border border-border bg-muted/40 px-3 py-2", children: [
      /* @__PURE__ */ jsx("div", { className: "mb-2 text-xs font-medium text-muted-foreground", children: text.sourcesHeading }),
      /* @__PURE__ */ jsx("ul", { className: "flex flex-col gap-2", children: sources.map((source) => /* @__PURE__ */ jsxs("li", { className: "min-w-0", children: [
        /* @__PURE__ */ jsxs("div", { className: "inline-flex max-w-full items-center gap-2 rounded-full border border-border bg-background px-2.5 py-1 text-xs", children: [
          /* @__PURE__ */ jsx(FileText, { className: "size-3.5 shrink-0 text-muted-foreground" }),
          /* @__PURE__ */ jsxs("span", { className: "font-mono text-[11px] text-muted-foreground", children: [
            "[",
            source.id,
            "]"
          ] }),
          source.url ? /* @__PURE__ */ jsx(
            "a",
            {
              className: "truncate font-medium text-primary underline-offset-4 hover:underline",
              href: source.url,
              rel: "noreferrer",
              target: "_blank",
              children: source.label
            }
          ) : /* @__PURE__ */ jsx("span", { className: "truncate", children: source.label })
        ] }),
        source.excerpt && /* @__PURE__ */ jsx("p", { className: "mt-1 pl-1 text-xs text-muted-foreground", children: source.excerpt })
      ] }, `${source.id}:${source.label}`)) })
    ] }),
    showDisclaimer && /* @__PURE__ */ jsx("p", { className: "text-xs text-muted-foreground", children: text.disclaimer })
  ] });
}
const SOURCE_HEADING = /^(?:#{1,3}[ \t]*)(?:回答引用来源|出处|Sources|Citations)[ \t]*$/im;
const DISCLAIMER = /(?:^|\n)[ \t]*以上信息由AI大模型依据知识库[^\n]*/;
const SOURCE_ITEM = /^(?:[-*]|\d+\.)?[ \t]*(?:\[([^\]]+)\])?[ \t]*(.+?)\s*$/;
const MARKDOWN_LINK = /^\[([^\]]+)\]\((https?:\/\/[^)]+)\)$/;
const CITATION_ID = /^Citation id:\s*(.+)\s*$/;
function splitKnowledgeAnswer(text) {
  const showDisclaimer = DISCLAIMER.test(text);
  const rest = text.replace(DISCLAIMER, "").replace(/\n{3,}/g, "\n\n").trimEnd();
  const heading = rest.match(SOURCE_HEADING);
  if (heading?.index !== void 0) {
    const sources = parseSourceLines(rest.slice(heading.index));
    return {
      body: rest.slice(0, heading.index).trimEnd(),
      showDisclaimer: showDisclaimer || sources.length > 0,
      sources
    };
  }
  const fallback = parseCitationIdBlocks(rest);
  if (fallback.sources.length > 0) {
    return { ...fallback, showDisclaimer: true };
  }
  return { body: rest, showDisclaimer, sources: [] };
}
function parseSourceLines(block) {
  const sources = [];
  for (const raw of block.split("\n")) {
    const line = raw.trim();
    if (!line || SOURCE_HEADING.test(line)) continue;
    const item = line.match(SOURCE_ITEM);
    if (!item) continue;
    const id = item[1]?.trim();
    const remainder = item[2]?.trim() ?? "";
    if (!id && sources.length > 0 && !/^(?:[-*]|\d+\.)/.test(line)) {
      const last = sources[sources.length - 1];
      last.excerpt = last.excerpt ? `${last.excerpt} ${remainder}` : remainder;
      continue;
    }
    if (!id && !/^(?:[-*]|\d+\.)/.test(line)) continue;
    sources.push(sourceFromLabel(id || String(sources.length + 1), remainder));
  }
  return sources;
}
function parseCitationIdBlocks(text) {
  const lines = text.split("\n");
  const sources = [];
  const body = [];
  for (let index = 0; index < lines.length; index += 1) {
    const idMatch = lines[index]?.match(CITATION_ID);
    if (!idMatch) {
      body.push(lines[index] ?? "");
      continue;
    }
    const labelLine = lines[index + 1]?.trim() ?? "";
    const excerptLine = lines[index + 2]?.trim() ?? "";
    sources.push(sourceFromLabel(idMatch[1] ?? String(sources.length + 1), labelLine, excerptLine));
    index += excerptLine ? 2 : labelLine ? 1 : 0;
  }
  return { body: body.join("\n").trimEnd(), showDisclaimer: sources.length > 0, sources };
}
function sourceFromLabel(id, label, excerpt) {
  const link = label.match(MARKDOWN_LINK);
  if (link) {
    return { excerpt: emptyToUndefined(excerpt), id, label: link[1] ?? label, url: link[2] };
  }
  return { excerpt: emptyToUndefined(excerpt), id, label };
}
function emptyToUndefined(value) {
  const trimmed = value?.trim();
  return trimmed ? trimmed : void 0;
}
function ConversationMessageRow({ contentRenderers, item, runtime, sessionId }) {
  const user = item.role === "user";
  const text = useAgentWorkspaceText();
  const [copyState, setCopyState] = useState("idle");
  const rawText = item.content?.filter((block) => block.kind !== "json").map((block) => block.text).join("\n") || item.text;
  const canCopy = Boolean(rawText.trim()) && item.status !== "streaming";
  const copyMessage = async () => {
    if (!canCopy) return;
    try {
      await navigator.clipboard.writeText(rawText);
      setCopyState("copied");
      window.setTimeout(() => setCopyState("idle"), 1600);
    } catch {
      setCopyState("failed");
      window.setTimeout(() => setCopyState("idle"), 2200);
    }
  };
  return /* @__PURE__ */ jsxs("article", { className: user ? "ml-auto max-w-[85%]" : "max-w-full", children: [
    /* @__PURE__ */ jsx("div", { className: "mb-1 text-xs text-muted-foreground", children: user ? text.you : item.role === "assistant" ? text.agent : text.system }),
    /* @__PURE__ */ jsxs("div", { className: user ? "rounded-md bg-muted px-3 py-2 text-sm whitespace-pre-wrap break-words" : "", children: [
      item.content ? item.content.map((block, index) => block.kind === "json" ? /* @__PURE__ */ jsx(JsonBlock, { content: block, contentRenderers, runtime, sessionId, readOnly: user || item.status !== "completed" }, `${block.id}:${index}`) : /* @__PURE__ */ jsx(ConversationTextContent, { item, value: block.text }, index)) : /* @__PURE__ */ jsx(ConversationTextContent, { item, value: item.text }),
      item.status === "streaming" && /* @__PURE__ */ jsx("span", { className: "ml-1 inline-block h-4 w-1.5 animate-pulse bg-current align-text-bottom" })
    ] }),
    (item.status === "pending" || item.status === "failed") && /* @__PURE__ */ jsx("div", { className: "mt-1 text-xs text-muted-foreground", role: "status", children: item.status === "pending" ? text.messagePending : text.messageFailed }),
    canCopy && /* @__PURE__ */ jsx(
      "button",
      {
        "aria-label": copyState === "copied" ? text.copiedMessage : text.copyMessage,
        className: "mt-1 inline-flex size-7 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
        onClick: () => void copyMessage(),
        title: copyState === "failed" ? text.copyMessageFailed : copyState === "copied" ? text.copiedMessage : text.copyMessage,
        type: "button",
        children: copyState === "copied" ? /* @__PURE__ */ jsx(Check, { className: "size-3.5" }) : /* @__PURE__ */ jsx(Copy, { className: "size-3.5" })
      }
    )
  ] });
}
function JsonBlock({ content, contentRenderers, runtime, sessionId, readOnly }) {
  const Renderer = content.identity?.namespace && content.identity.semanticKey && content.identity.schemaVersion ? contentRenderers?.lookup({
    blockKind: "json",
    mediaType: content.mediaType,
    namespace: content.identity?.namespace,
    semanticKey: content.identity?.semanticKey,
    schemaVersion: content.identity?.schemaVersion ?? "1"
  })?.json : void 0;
  if (Renderer && runtime && sessionId && content.form) return /* @__PURE__ */ jsx(Renderer, { content, runtime, sessionId, readOnly });
  return /* @__PURE__ */ jsx(JsonContentPreview, { content });
}
function ConversationTextContent({ item, value }) {
  if (item.role === "user") return /* @__PURE__ */ jsx(Fragment, { children: value });
  const answer = item.status === "streaming" ? { body: value, showDisclaimer: false, sources: [] } : splitKnowledgeAnswer(value);
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    answer.body ? /* @__PURE__ */ jsx(MarkdownMessage, { text: answer.body }) : null,
    /* @__PURE__ */ jsx(KnowledgeAnswerSources, { showDisclaimer: answer.showDisclaimer, sources: answer.sources })
  ] });
}
const toolActionText = {
  "en-US": { actions: "Actions", input: "Action input as JSON (optional)", working: "Working…" },
  "zh-CN": { actions: "操作", input: "动作输入 JSON（可选）", working: "处理中…" }
};
function ToolActionButtons({
  item,
  runtime,
  sessionId
}) {
  const labels = toolActionText[useAgentWorkspaceLocale()];
  const [pending, setPending] = useState(null);
  const [error, setError] = useState(null);
  const actions = item.actions.filter(
    (action) => !action.expiresAt || Date.parse(action.expiresAt) > Date.now()
  );
  if (!runtime.executeToolAction || !runtime.getSnapshot(sessionId).capabilities.executeToolAction || actions.length === 0) return null;
  return /* @__PURE__ */ jsxs("section", { className: "space-y-2 border-t border-border pt-2", children: [
    /* @__PURE__ */ jsx("div", { className: "text-[11px] font-medium uppercase text-muted-foreground", children: labels.actions }),
    /* @__PURE__ */ jsx("div", { className: "flex flex-wrap gap-2", children: actions.map((action) => /* @__PURE__ */ jsx(
      "button",
      {
        className: "rounded border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50",
        disabled: pending !== null,
        onClick: () => {
          if (action.confirmationRequired && !window.confirm(action.label || action.actionType)) return;
          let payload;
          if (action.inputSchema) {
            const raw = window.prompt(labels.input, "{}");
            if (raw === null) return;
            try {
              payload = JSON.parse(raw);
            } catch {
              setError("Invalid JSON action input");
              return;
            }
          }
          setError(null);
          setPending(action.actionId);
          void runtime.executeToolAction(sessionId, {
            actionId: action.actionId,
            actionType: action.actionType,
            commandId: crypto.randomUUID(),
            ...payload === void 0 ? {} : { payload },
            schemaVersion: action.schemaVersion,
            toolExecutionId: item.executionId || item.id
          }).catch((cause) => setError(cause instanceof Error ? cause.message : String(cause))).finally(() => setPending(null));
        },
        type: "button",
        children: pending === action.actionId ? labels.working : action.label || action.actionType
      },
      action.actionId
    )) }),
    error ? /* @__PURE__ */ jsx("p", { className: "text-xs text-destructive", role: "alert", children: error }) : null
  ] });
}
function toolActivityRawEvidence(item) {
  return JSON.stringify(
    {
      identity: item.identity,
      inputValue: item.inputValue,
      results: item.results,
      title: item.title
    },
    (_key, value) => typeof value === "bigint" ? value.toString() : value,
    2
  );
}
function cleanToolEvidence(value) {
  const clean = value?.trim();
  return !clean || clean === "{}" || clean === "null" ? void 0 : clean;
}
function resolveToolActivityPresentation(item, renderer) {
  const presentation = renderer?.presentation;
  return {
    icon: presentation?.icon ?? Wrench,
    inputLabel: presentation?.inputLabel ?? "Input",
    label: presentation?.label ?? fallbackToolLabel(item),
    outputLabel: presentation?.outputLabel ?? "Output",
    specialized: renderer !== void 0
  };
}
function fallbackToolLabel(item) {
  const title = item.title.trim();
  if (looksLikeCommandScript(title)) {
    return "Command";
  }
  if (item.identity.semanticKey === "tool.custom") {
    return title && title !== "tool.custom" ? title : "Custom tool";
  }
  return title || item.identity.semanticKey;
}
function looksLikeCommandScript(title) {
  return /[\n;&|<>]/.test(title) || title.length > 40;
}
const navigationContext = createContext(null);
function WorkspaceFileNavigationProvider({
  children,
  onReveal
}) {
  const handlerRef = useRef(null);
  const pendingRef = useRef(null);
  const workspaceRootRef = useRef(void 0);
  const reveal = (target) => {
    onReveal?.();
    if (handlerRef.current) {
      handlerRef.current(target);
      return;
    }
    pendingRef.current = target;
  };
  const setHandler = (handler) => {
    handlerRef.current = handler;
    if (!handler || !pendingRef.current) return;
    const pending = pendingRef.current;
    pendingRef.current = null;
    handler(pending);
  };
  return /* @__PURE__ */ jsx(
    navigationContext.Provider,
    {
      value: {
        reveal,
        setHandler,
        setWorkspaceRoot: (root) => {
          if (root) workspaceRootRef.current = root;
        },
        workspaceRoot: () => workspaceRootRef.current
      },
      children
    }
  );
}
function useWorkspaceFileNavigation() {
  return useContext(navigationContext);
}
const fileSemanticKeys = /* @__PURE__ */ new Set([
  "filesystem.read",
  "filesystem.write",
  "filesystem.edit",
  "filesystem.change"
]);
const pathKeys = ["path", "file_path", "filePath"];
function toolWorkspacePath(item, workspaceRoot) {
  if (!fileSemanticKeys.has(item.identity.semanticKey)) return null;
  const value = asRecord(item.inputValue);
  if (!value) return null;
  for (const key of pathKeys) {
    const path = value[key];
    if (typeof path !== "string") continue;
    const relative = relativeWorkspacePath(path, workspaceRoot);
    if (relative) return relative;
  }
  return null;
}
function toolWorkspaceLine(item) {
  const value = asRecord(item.inputValue);
  const line = value?.line ?? value?.lineNumber;
  return typeof line === "number" && Number.isFinite(line) && line > 0 ? Math.floor(line) : void 0;
}
function relativeWorkspacePath(raw, workspaceRoot) {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  if (trimmed.startsWith("/")) {
    if (!workspaceRoot) return null;
    const root = workspaceRoot.replace(/\/+$/, "");
    if (trimmed === root) return "";
    if (!trimmed.startsWith(`${root}/`)) return null;
    return sanitizeRelative(trimmed.slice(root.length + 1));
  }
  return sanitizeRelative(trimmed);
}
function sanitizeRelative(path) {
  const parts = [];
  for (const part of path.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") return null;
    parts.push(part);
  }
  return parts.length > 0 ? parts.join("/") : null;
}
function asRecord(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}
function ToolActivityCard({
  item,
  renderers,
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const navigation = useWorkspaceFileNavigation();
  const workspacePath = toolWorkspacePath(item, navigation?.workspaceRoot());
  const renderer = renderers?.lookup(item.identity);
  const presentation = resolveToolActivityPresentation(item, renderer);
  const input = cleanToolEvidence(item.input);
  const output = cleanToolEvidence(item.output);
  const detail = cleanToolEvidence(item.detail);
  const failure = item.failure?.message;
  const rawTitle = cleanToolEvidence(item.title);
  const title = rawTitle && rawTitle !== workspacePath && text.toolText(rawTitle) !== text.toolText(presentation.label) ? rawTitle : void 0;
  const rawEvidence = presentation.specialized ? void 0 : toolActivityRawEvidence(item);
  const hasActions = Boolean(
    item.actions.length && runtime.executeToolAction && runtime.getSnapshot(sessionId).capabilities.executeToolAction
  );
  const hasEvidence = Boolean(input || output || detail || failure || rawEvidence || hasActions);
  const Icon = presentation.icon;
  const RegisteredSummary = renderer?.summary;
  const RegisteredDetail = renderer?.detail;
  const row = /* @__PURE__ */ jsxs("div", { className: "flex min-h-9 min-w-0 items-center gap-2 rounded-sm px-1 py-1 text-xs transition-colors duration-fast hover:bg-muted/40", children: [
    /* @__PURE__ */ jsx(
      Icon,
      {
        "aria-hidden": "true",
        className: `size-4 shrink-0 ${item.status === "running" ? "animate-pulse text-primary" : item.status === "failed" ? "text-danger" : item.status === "waiting" ? "text-warning" : "text-muted-foreground"}`
      }
    ),
    /* @__PURE__ */ jsx(
      "span",
      {
        className: "min-w-0 max-w-[38%] shrink truncate font-medium text-foreground/80",
        title: presentation.label,
        children: text.toolText(presentation.label)
      }
    ),
    title ? /* @__PURE__ */ jsx("span", { "aria-hidden": "true", className: "text-muted-foreground/50", children: "·" }) : null,
    title ? /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1 truncate text-muted-foreground", title, children: title }) : null,
    workspacePath && navigation ? /* @__PURE__ */ jsx(
      "button",
      {
        className: "max-w-[30%] shrink-0 truncate text-xs text-primary underline-offset-2 hover:underline",
        onClick: (event) => {
          event.stopPropagation();
          navigation.reveal({ path: workspacePath, line: toolWorkspaceLine(item) });
        },
        type: "button",
        children: workspacePath
      }
    ) : null,
    hasEvidence || RegisteredDetail ? /* @__PURE__ */ jsx(ChevronRight, { "aria-hidden": "true", className: "size-3.5 shrink-0 text-muted-foreground transition-transform group-open/tool-item:rotate-90" }) : null,
    item.progress?.fraction !== void 0 ? /* @__PURE__ */ jsx(
      "span",
      {
        "aria-label": item.progress.message ?? text.activityStatus(item.status),
        "aria-valuemax": 1,
        "aria-valuemin": 0,
        "aria-valuenow": Math.max(0, Math.min(1, item.progress.fraction)),
        className: "h-1 w-16 shrink-0 overflow-hidden rounded-full bg-muted",
        role: "progressbar",
        children: /* @__PURE__ */ jsx(
          "span",
          {
            className: "block h-full bg-primary",
            style: { width: `${Math.max(0, Math.min(100, item.progress.fraction * 100))}%` }
          }
        )
      }
    ) : null,
    item.status !== "completed" ? /* @__PURE__ */ jsx(ActivityStatus, { label: text.activityStatus(item.status), status: item.status }) : null
  ] });
  if (!hasEvidence && !RegisteredSummary && !RegisteredDetail) return /* @__PURE__ */ jsx("article", { children: row });
  return /* @__PURE__ */ jsxs("details", { className: "group/tool-item", children: [
    /* @__PURE__ */ jsx("summary", { className: "cursor-pointer list-none [&::-webkit-details-marker]:hidden", children: row }),
    RegisteredSummary ? /* @__PURE__ */ jsx("div", { className: "ml-7 border-t border-border bg-muted/15 px-3 py-2", "data-testid": "registered-tool-summary", children: /* @__PURE__ */ jsx(RegisteredSummary, { item }) }) : null,
    /* @__PURE__ */ jsxs("div", { className: "ml-7 space-y-3 border-l border-border bg-muted/20 px-3 py-3", children: [
      RegisteredDetail && /* @__PURE__ */ jsx(RegisteredDetail, { item }),
      input && /* @__PURE__ */ jsx(Evidence, { label: text.toolText(presentation.inputLabel), value: input }),
      output && /* @__PURE__ */ jsx(Evidence, { label: text.toolText(presentation.outputLabel), value: output }),
      detail && /* @__PURE__ */ jsx(Evidence, { label: text.details, value: detail }),
      failure && /* @__PURE__ */ jsx(Evidence, { label: text.taskFailed, value: failure }),
      rawEvidence && /* @__PURE__ */ jsx(Evidence, { label: text.rawToolEvidence, value: rawEvidence }),
      /* @__PURE__ */ jsx(ToolActionButtons, { item, runtime, sessionId })
    ] })
  ] });
}
function ActivityStatus({
  label,
  status
}) {
  const Icon = status === "running" ? Loader2 : status === "waiting" ? Loader2 : status === "pending" ? Clock3 : status === "failed" ? AlertTriangle : status === "cancelled" ? CircleStop : CheckCircle2;
  return /* @__PURE__ */ jsxs("span", { className: "flex shrink-0 items-center gap-1 text-xs text-muted-foreground", children: [
    /* @__PURE__ */ jsx(
      Icon,
      {
        className: `size-3.5 ${status === "running" ? "animate-spin text-primary" : status === "pending" ? "text-muted-foreground" : status === "failed" ? "text-danger" : status === "waiting" ? "text-warning" : status === "cancelled" ? "text-muted-foreground" : "text-success"}`
      }
    ),
    label
  ] });
}
function Evidence({ label, value }) {
  return /* @__PURE__ */ jsxs("section", { children: [
    /* @__PURE__ */ jsx("div", { className: "mb-1 text-[11px] font-medium uppercase text-muted-foreground", children: label }),
    /* @__PURE__ */ jsx("pre", { className: "overflow-x-auto whitespace-pre-wrap font-mono text-xs leading-5", children: value })
  ] });
}
function ToolActivityGroup({
  renderers,
  tools,
  runtime,
  sessionId
}) {
  return /* @__PURE__ */ jsx("div", { className: "space-y-1", children: tools.map((tool) => /* @__PURE__ */ jsx(ToolActivityCard, { item: tool, renderers, runtime, sessionId }, tool.id)) });
}
function groupToolActivity(items) {
  const grouped = [];
  let tools = [];
  const flushTools = () => {
    if (tools.length === 0) return;
    grouped.push({
      id: `tool-run:${tools[0].id}`,
      kind: "tool-run",
      tools
    });
    tools = [];
  };
  for (const item of items) {
    if (isToolActivity(item)) {
      tools.push(item);
      continue;
    }
    flushTools();
    grouped.push(item);
  }
  flushTools();
  return grouped;
}
function isToolActivity(item) {
  return item.kind === "tool";
}
const CONVERSATION_CONTENT_WIDTH = "mx-auto w-full max-w-3xl";
function ActivityTimeline({
  items,
  runtime,
  sessionId,
  contentRenderers,
  toolRenderers
}) {
  const text = useAgentWorkspaceText();
  if (items.length === 0) {
    return /* @__PURE__ */ jsxs("div", { className: "flex min-h-48 flex-col items-center justify-center gap-1 px-6 text-center", children: [
      /* @__PURE__ */ jsx("div", { className: "text-sm font-medium", children: text.readyForTask }),
      /* @__PURE__ */ jsx("div", { className: "text-xs text-muted-foreground", children: text.startSession })
    ] });
  }
  return /* @__PURE__ */ jsx(
    "div",
    {
      "aria-label": text.conversation,
      "aria-live": "polite",
      "aria-relevant": "additions text",
      className: `${CONVERSATION_CONTENT_WIDTH} flex flex-col gap-3 px-4 py-4`,
      role: "log",
      children: groupToolActivity(items).map(
        (item) => item.kind === "tool-run" ? /* @__PURE__ */ jsx(
          ToolActivityGroup,
          {
            renderers: toolRenderers,
            runtime,
            sessionId,
            tools: item.tools
          },
          item.id
        ) : item.kind === "message" ? /* @__PURE__ */ jsx(ConversationMessageRow, { contentRenderers, item, runtime, sessionId }, item.id) : item.kind === "attachment" ? /* @__PURE__ */ jsx(
          AttachmentPreview,
          {
            item,
            runtime,
            sessionId
          },
          item.id
        ) : item.kind === "artifact" ? /* @__PURE__ */ jsx(
          ArtifactCard,
          {
            item,
            runtime,
            sessionId,
            contentRenderers
          },
          item.id
        ) : /* @__PURE__ */ jsx(ActivityRow, { item }, item.id)
      )
    }
  );
}
function ActivityRow({
  item
}) {
  const text = useAgentWorkspaceText();
  const isReasoning = item.kind === "reasoning";
  const Icon = isReasoning && item.status !== "running" ? Atom : item.status === "running" ? Loader2 : item.status === "failed" || item.kind === "error" ? AlertTriangle : CheckCircle2;
  const iconTone = item.status === "running" ? "animate-spin text-primary" : item.status === "failed" || item.kind === "error" ? "text-danger" : "text-muted-foreground";
  const preview = item.detail?.replace(/\s+/g, " ").trim();
  const status = item.status === "completed" ? null : text.activityStatus(item.status);
  const row = /* @__PURE__ */ jsxs("div", { className: "flex min-h-9 min-w-0 items-center gap-2 rounded-sm px-1 py-1 text-xs transition-colors duration-fast hover:bg-muted/40", children: [
    /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: `size-4 shrink-0 ${iconTone}` }),
    /* @__PURE__ */ jsx(
      "span",
      {
        className: "min-w-0 max-w-[42%] shrink truncate font-medium text-foreground/80",
        title: item.title,
        children: text.activityTitle(item.title)
      }
    ),
    preview ? /* @__PURE__ */ jsx("span", { "aria-hidden": "true", className: "text-muted-foreground/50", children: "·" }) : null,
    preview ? /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1 truncate text-muted-foreground", title: item.detail, children: preview }) : null,
    status ? /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: status }) : null,
    item.detail ? /* @__PURE__ */ jsx(ChevronRight, { "aria-hidden": "true", className: "size-3.5 shrink-0 text-muted-foreground transition-transform group-open/activity:rotate-90" }) : null
  ] });
  const detail = item.detail ? /* @__PURE__ */ jsx("pre", { className: "ml-7 max-w-full overflow-x-auto whitespace-pre-wrap break-words border-l border-border py-2 pl-3 text-xs leading-5 text-muted-foreground [overflow-wrap:anywhere]", children: item.detail }) : null;
  if (detail) {
    return /* @__PURE__ */ jsxs("details", { className: "group/activity", children: [
      /* @__PURE__ */ jsx("summary", { className: "cursor-pointer list-none [&::-webkit-details-marker]:hidden", children: row }),
      detail
    ] });
  }
  return /* @__PURE__ */ jsx("article", { children: row });
}
function InteractionTrace({
  interaction,
  text
}) {
  const Icon = interaction.status === "failed" || interaction.status === "rejected" ? AlertTriangle : interaction.status === "pending" || interaction.status === "running" ? Loader2 : interaction.status === "cancelled" ? CircleStop : CheckCircle2;
  const tone = interaction.status === "failed" || interaction.status === "rejected" ? "text-danger" : interaction.status === "pending" || interaction.status === "running" ? "animate-spin text-primary" : interaction.status === "cancelled" ? "text-muted-foreground" : "text-success";
  return /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 items-center gap-2 rounded-sm px-1 py-1 text-xs", children: [
    /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: `size-3.5 shrink-0 ${tone}` }),
    /* @__PURE__ */ jsx("span", { className: "min-w-0 max-w-[42%] shrink truncate font-medium", title: interaction.kind, children: text.interactionKind(interaction.kind) }),
    /* @__PURE__ */ jsx(
      "span",
      {
        className: "min-w-0 flex-1 truncate text-muted-foreground",
        title: interaction.sourceId ?? interaction.commandId,
        children: interaction.sourceId ?? interaction.commandId
      }
    ),
    /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: text.interactionStatus(interaction.status) })
  ] });
}
function TraceGroup({
  group,
  text
}) {
  const Icon = group.status === "failed" ? AlertTriangle : group.status === "running" ? Loader2 : CheckCircle2;
  const label = group.turnId ? text.turn(group.turnId) : text.unscoped;
  const itemSummary = group.items.map(itemLabel).join(" · ");
  return /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 items-center gap-2 text-xs", children: [
    /* @__PURE__ */ jsx(
      Icon,
      {
        "aria-hidden": "true",
        className: `size-3.5 shrink-0 ${group.status === "failed" ? "text-danger" : group.status === "running" ? "animate-spin text-primary" : "text-success"}`
      }
    ),
    /* @__PURE__ */ jsx("span", { className: "min-w-0 max-w-[42%] shrink truncate font-medium", title: label, children: label }),
    /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1 truncate text-muted-foreground", title: itemSummary, children: itemSummary }),
    /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: text[group.status] })
  ] });
}
function itemLabel(item) {
  if (item.kind === "tool") return item.title;
  if (item.kind === "artifact" || item.kind === "attachment") return item.filename;
  if (item.kind === "message") return item.role;
  return item.title;
}
const executionTraceText = {
  "en-US": {
    cancelled: "Cancelled",
    completed: "Completed",
    failed: "Failed",
    queued: "Queued",
    running: "Running",
    title: "Execution trace",
    unscoped: "Unscoped execution",
    waiting: "Waiting",
    turn: (id) => `Turn ${id}`,
    interactionKind: (kind) => ({
      artifact_action: "Artifact action",
      extension: "Extension",
      permission: "Permission",
      tool_action: "Tool action"
    })[kind],
    interactionStatus: (status) => ({
      pending: "Pending",
      running: "Running",
      succeeded: "Completed",
      failed: "Failed",
      rejected: "Rejected",
      cancelled: "Cancelled"
    })[status]
  },
  "zh-CN": {
    cancelled: "已取消",
    completed: "已完成",
    failed: "失败",
    queued: "排队中",
    running: "执行中",
    title: "执行轨迹",
    unscoped: "未归属执行",
    waiting: "等待中",
    turn: (id) => `回合 ${id}`,
    interactionKind: (kind) => ({
      artifact_action: "成果操作",
      extension: "扩展能力",
      permission: "权限请求",
      tool_action: "工具操作"
    })[kind],
    interactionStatus: (status) => ({
      pending: "等待中",
      running: "执行中",
      succeeded: "已完成",
      failed: "失败",
      rejected: "已拒绝",
      cancelled: "已取消"
    })[status]
  }
};
function executionTraceGroups(items) {
  const groups = /* @__PURE__ */ new Map();
  for (const item of items) {
    if (!item.execution) continue;
    const id = item.execution.turnId ?? "unscoped";
    const group = groups.get(id) ?? {
      id,
      items: [],
      status: "completed",
      ...item.execution.turnId ? { turnId: item.execution.turnId } : {}
    };
    group.items.push(item);
    group.status = nextStatus(group.status, itemStatus(item));
    groups.set(id, group);
  }
  return [...groups.values()];
}
function itemStatus(item) {
  if (item.kind === "attachment") return "completed";
  if (item.kind === "artifact") {
    if (item.status === "failed") return "failed";
    if (item.status === "queued" || item.status === "processing") return "running";
    return "completed";
  }
  if (item.status === "failed") return "failed";
  if (item.status === "running" || item.status === "pending") return "running";
  return "completed";
}
function nextStatus(current, next) {
  if (current === "failed" || next === "failed") return "failed";
  if (current === "running" || next === "running") return "running";
  return "completed";
}
function ExecutionTrace({
  interactions = [],
  items,
  execution
}) {
  const text = executionTraceText[useAgentWorkspaceLocale()];
  const groups = executionTraceGroups(items);
  const active = (execution ? Boolean(execution.activeRunId) : groups.some((group) => group.status === "running")) || interactions.some(
    (interaction) => interaction.status === "pending" || interaction.status === "running"
  );
  const disclosure = useTraceDisclosure(active);
  if ((execution?.runs.length ?? groups.length) === 0 && interactions.length === 0) return null;
  const count = (execution?.runs.length ?? groups.length) + interactions.length;
  return /* @__PURE__ */ jsx(
    "section",
    {
      "aria-label": text.title,
      className: "shrink-0 border-b border-border px-4 py-2",
      "data-execution-trace": true,
      children: /* @__PURE__ */ jsxs("details", { className: "group/trace", ...disclosure, children: [
        /* @__PURE__ */ jsxs("summary", { className: "flex min-w-0 cursor-pointer list-none items-center gap-2 text-xs font-medium text-muted-foreground [&::-webkit-details-marker]:hidden", children: [
          /* @__PURE__ */ jsx(ChevronRight, { className: "size-3 shrink-0 transition-transform group-open/trace:rotate-90" }),
          text.title,
          " · ",
          count
        ] }),
        /* @__PURE__ */ jsxs("div", { className: "mt-2 space-y-1.5", children: [
          execution ? execution.runs.map((run) => /* @__PURE__ */ jsx(
            RunTrace,
            {
              active: run.id === execution.activeRunId,
              run,
              text
            },
            run.id
          )) : groups.map((group) => /* @__PURE__ */ jsx(TraceGroup, { group, text }, group.id)),
          interactions.map((interaction) => /* @__PURE__ */ jsx(InteractionTrace, { interaction, text }, interaction.id))
        ] })
      ] })
    }
  );
}
function RunTrace({
  active,
  run,
  text
}) {
  const disclosure = useTraceDisclosure(active);
  const Icon = run.status === "failed" ? AlertTriangle : run.status === "cancelled" || run.status === "waiting" ? CircleStop : run.status === "running" ? Loader2 : CheckCircle2;
  const tone = run.status === "failed" ? "text-danger" : run.status === "cancelled" ? "text-muted-foreground" : run.status === "waiting" ? "text-warning" : run.status === "running" ? "animate-spin text-primary" : run.status === "queued" ? "text-muted-foreground" : "text-success";
  const activeStep = run.steps.find((step2) => step2.status === "running" || step2.status === "waiting");
  return /* @__PURE__ */ jsxs("details", { className: "group/execution", ...disclosure, children: [
    /* @__PURE__ */ jsxs("summary", { className: "flex min-w-0 cursor-pointer list-none items-center gap-2 rounded-sm px-1 py-1 text-xs hover:bg-muted/40 [&::-webkit-details-marker]:hidden", children: [
      /* @__PURE__ */ jsx(ChevronRight, { className: "size-3 shrink-0 text-muted-foreground transition-transform group-open/execution:rotate-90" }),
      /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: `size-3.5 shrink-0 ${tone}` }),
      /* @__PURE__ */ jsx("span", { className: "min-w-0 max-w-[42%] shrink truncate font-medium", title: run.title, children: run.turnId ? text.turn(run.turnId) : text.unscoped }),
      /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1 truncate text-muted-foreground", title: activeStep?.title ?? run.steps.at(-1)?.title, children: activeStep?.title ?? run.steps.at(-1)?.title }),
      /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: text[run.status] })
    ] }),
    /* @__PURE__ */ jsx("div", { className: "ml-8 mt-1 space-y-1 border-l border-border pl-3", children: run.steps.map((step2) => /* @__PURE__ */ jsxs("div", { className: "min-w-0 text-xs", children: [
      /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-2", children: [
        /* @__PURE__ */ jsx(
          "span",
          {
            className: `min-w-0 flex-1 break-words ${step2.status === "running" || step2.status === "waiting" ? "text-primary" : "text-muted-foreground"}`,
            children: step2.title
          }
        ),
        /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: text[step2.status] })
      ] }),
      step2.detail ? /* @__PURE__ */ jsx("div", { className: "break-words text-muted-foreground", title: step2.detail, children: step2.detail }) : null,
      step2.failure?.message ? /* @__PURE__ */ jsx("div", { className: "break-words text-danger", children: step2.failure.message }) : null,
      step2.progress?.message || step2.progress?.stage ? /* @__PURE__ */ jsx("div", { className: "break-words text-muted-foreground", children: step2.progress.stage ? `${step2.progress.stage}${step2.progress.message ? ` · ${step2.progress.message}` : ""}` : step2.progress.message }) : null,
      step2.progress?.fraction !== void 0 ? /* @__PURE__ */ jsx(
        "div",
        {
          "aria-label": step2.progress.message ?? text.running,
          "aria-valuemax": 1,
          "aria-valuemin": 0,
          "aria-valuenow": Math.max(0, Math.min(1, step2.progress.fraction)),
          className: "mt-1 h-1 overflow-hidden rounded-full bg-muted",
          role: "progressbar",
          children: /* @__PURE__ */ jsx(
            "div",
            {
              className: "h-full bg-primary transition-[width] duration-fast",
              style: {
                width: `${Math.max(0, Math.min(100, step2.progress.fraction * 100))}%`
              }
            }
          )
        }
      ) : null
    ] }, step2.id)) })
  ] });
}
function useTraceDisclosure(active) {
  const [open, setOpen] = useState(active);
  useEffect(() => {
    if (active) setOpen(true);
  }, [active]);
  return {
    open,
    onToggle: (event) => {
      setOpen(event.currentTarget.open);
    }
  };
}
function PermissionQuestionField({
  custom,
  disabled,
  onCustomChange,
  onOptionChange,
  question,
  selected
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs("fieldset", { className: "space-y-3", disabled, children: [
    /* @__PURE__ */ jsx("legend", { className: "sr-only", children: question.prompt }),
    question.header ? /* @__PURE__ */ jsx("p", { className: "text-xs font-medium uppercase tracking-wide text-muted-foreground", children: question.header }) : null,
    /* @__PURE__ */ jsx("p", { className: "text-base font-semibold leading-6 text-foreground", children: question.prompt }),
    /* @__PURE__ */ jsx("div", { className: "space-y-2", children: question.options.map((option, index) => {
      const checked = selected.includes(option.label);
      return /* @__PURE__ */ jsxs(
        "button",
        {
          "aria-checked": checked,
          className: `flex min-h-11 w-full items-start gap-3 rounded-md border px-3 py-2.5 text-left transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60 ${checked ? "border-primary bg-primary/5" : "border-border bg-background hover:border-primary/50 hover:bg-muted/40"}`,
          onClick: () => onOptionChange(option.label),
          role: question.multiple ? "checkbox" : "radio",
          type: "button",
          children: [
            /* @__PURE__ */ jsx(
              "span",
              {
                className: `mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-sm border text-xs font-semibold ${checked ? "border-primary bg-primary text-primary-foreground" : "border-border-strong text-muted-foreground"}`,
                children: checked ? /* @__PURE__ */ jsx(Check, { "aria-hidden": "true", className: "size-3.5" }) : index + 1
              }
            ),
            /* @__PURE__ */ jsxs("span", { className: "min-w-0 flex-1", children: [
              /* @__PURE__ */ jsx("span", { className: "block text-sm font-medium text-foreground", children: option.label }),
              option.description ? /* @__PURE__ */ jsx("span", { className: "mt-0.5 block text-xs leading-5 text-muted-foreground", children: option.description }) : null
            ] })
          ]
        },
        option.label
      );
    }) }),
    question.allowCustom ? /* @__PURE__ */ jsxs("label", { className: "block space-y-1.5 text-xs font-medium text-muted-foreground", children: [
      /* @__PURE__ */ jsx("span", { children: text.customAnswer }),
      /* @__PURE__ */ jsx(
        "input",
        {
          "aria-label": text.customAnswerFor(question.prompt),
          autoComplete: "off",
          className: "h-11 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground outline-none transition-colors duration-fast placeholder:text-muted-foreground/70 focus:border-ring focus-visible:ring-2 focus-visible:ring-ring",
          onChange: (event) => onCustomChange(event.target.value),
          placeholder: text.customAnswer,
          type: question.secret ? "password" : "text",
          value: custom
        }
      )
    ] }) : null
  ] });
}
function questionAnswers(question, selected, custom) {
  const customValue = question.allowCustom ? custom.trim() : "";
  if (!question.multiple) {
    if (customValue) return [customValue];
    return selected.slice(0, 1);
  }
  return customValue ? [.../* @__PURE__ */ new Set([...selected, customValue])] : selected;
}
function questionAnswered(question, selected, custom) {
  return questionAnswers(question, selected, custom).length > 0;
}
function PermissionQuestionForm({
  disabled,
  permission,
  onReject,
  onSubmit
}) {
  const text = useAgentWorkspaceText();
  const [activeIndex, setActiveIndex] = useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const [selected, setSelected] = useState({});
  const [custom, setCustom] = useState({});
  useEffect(() => {
    setActiveIndex((current) => Math.min(current, Math.max(permission.questions.length - 1, 0)));
  }, [permission.questions.length]);
  const question = permission.questions[activeIndex];
  if (!question) return null;
  const answers = permission.questions.reduce(
    (result, item) => {
      result[item.id] = questionAnswers(item, selected[item.id] ?? [], custom[item.id] ?? "");
      return result;
    },
    {}
  );
  const currentAnswered = questionAnswered(
    question,
    selected[question.id] ?? [],
    custom[question.id] ?? ""
  );
  const allAnswered = permission.questions.every((item) => answers[item.id].length > 0);
  const isLast = activeIndex === permission.questions.length - 1;
  const selectOption = (item, label) => {
    setSelected((current) => {
      if (!item.multiple) return { ...current, [item.id]: [label] };
      const next = new Set(current[item.id] ?? []);
      if (next.has(label)) next.delete(label);
      else next.add(label);
      return { ...current, [item.id]: [...next] };
    });
    if (!item.multiple) setCustom((current) => ({ ...current, [item.id]: "" }));
  };
  const updateCustom = (item, value) => {
    setCustom((current) => ({ ...current, [item.id]: value }));
    if (!item.multiple && value.trim()) setSelected((current) => ({ ...current, [item.id]: [] }));
  };
  return /* @__PURE__ */ jsx("section", { className: "border-t border-border bg-surface px-4 py-4 sm:px-6", children: /* @__PURE__ */ jsxs("div", { className: `${CONVERSATION_CONTENT_WIDTH} rounded-lg border border-border bg-surface-raised shadow-soft`, children: [
    /* @__PURE__ */ jsxs("div", { className: "flex items-start gap-3 border-b border-border px-4 py-3 sm:px-5", children: [
      /* @__PURE__ */ jsx("span", { className: "mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary", children: /* @__PURE__ */ jsx(HelpCircle, { "aria-hidden": "true", className: "size-4" }) }),
      /* @__PURE__ */ jsxs("div", { className: "min-w-0 flex-1", children: [
        /* @__PURE__ */ jsx("p", { className: "text-xs font-medium text-muted-foreground", children: text.approvals }),
        /* @__PURE__ */ jsx("h2", { className: "mt-0.5 truncate text-sm font-semibold text-foreground", children: permission.title })
      ] }),
      /* @__PURE__ */ jsx(
        "button",
        {
          "aria-expanded": !collapsed,
          "aria-label": collapsed ? text.expandQuestion : text.collapseQuestion,
          className: "flex size-9 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors duration-fast hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          onClick: () => setCollapsed((current) => !current),
          type: "button",
          children: /* @__PURE__ */ jsx(ChevronDown, { className: `size-4 transition-transform duration-fast ${collapsed ? "-rotate-90" : ""}` })
        }
      ),
      /* @__PURE__ */ jsx(
        "button",
        {
          "aria-label": text.reject,
          className: "flex size-9 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors duration-fast hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          disabled,
          onClick: onReject,
          type: "button",
          children: /* @__PURE__ */ jsx(X, { "aria-hidden": "true", className: "size-4" })
        }
      )
    ] }),
    !collapsed ? /* @__PURE__ */ jsxs(
      "form",
      {
        className: "px-4 py-4 sm:px-5",
        onSubmit: (event) => {
          event.preventDefault();
          if (isLast && allAnswered) onSubmit(answers);
          else if (currentAnswered) setActiveIndex((current) => Math.min(current + 1, permission.questions.length - 1));
        },
        children: [
          /* @__PURE__ */ jsx(
            PermissionQuestionField,
            {
              custom: custom[question.id] ?? "",
              disabled,
              onCustomChange: (value) => updateCustom(question, value),
              onOptionChange: (label) => selectOption(question, label),
              question,
              selected: selected[question.id] ?? []
            }
          ),
          /* @__PURE__ */ jsxs("div", { className: "mt-4 flex flex-col gap-3 border-t border-border pt-3 sm:flex-row sm:items-center sm:justify-between", children: [
            /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-1", children: [
              /* @__PURE__ */ jsx(
                "button",
                {
                  "aria-label": text.previousQuestion,
                  className: "flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors duration-fast hover:bg-muted hover:text-foreground disabled:opacity-40",
                  disabled: disabled || activeIndex === 0,
                  onClick: () => setActiveIndex((current) => Math.max(current - 1, 0)),
                  type: "button",
                  children: /* @__PURE__ */ jsx(ChevronLeft, { "aria-hidden": "true", className: "size-4" })
                }
              ),
              /* @__PURE__ */ jsxs("span", { "aria-live": "polite", className: "min-w-12 text-center text-xs font-medium text-muted-foreground", children: [
                activeIndex + 1,
                " / ",
                permission.questions.length
              ] }),
              /* @__PURE__ */ jsx(
                "button",
                {
                  "aria-label": text.nextQuestion,
                  className: "flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors duration-fast hover:bg-muted hover:text-foreground disabled:opacity-40",
                  disabled: disabled || isLast,
                  onClick: () => setActiveIndex((current) => Math.min(current + 1, permission.questions.length - 1)),
                  type: "button",
                  children: /* @__PURE__ */ jsx(ChevronRight, { "aria-hidden": "true", className: "size-4" })
                }
              )
            ] }),
            /* @__PURE__ */ jsxs("div", { className: "flex flex-wrap justify-end gap-2", children: [
              /* @__PURE__ */ jsxs(
                "button",
                {
                  className: "inline-flex min-h-11 items-center gap-1.5 rounded-md border border-border px-3 text-xs font-medium text-muted-foreground transition-colors duration-fast hover:bg-muted hover:text-foreground",
                  disabled,
                  onClick: onReject,
                  type: "button",
                  children: [
                    /* @__PURE__ */ jsx(X, { "aria-hidden": "true", className: "size-3.5" }),
                    text.skipQuestion
                  ]
                }
              ),
              /* @__PURE__ */ jsxs(
                "button",
                {
                  className: "inline-flex min-h-11 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors duration-fast hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50",
                  disabled: disabled || !currentAnswered || isLast && !allAnswered,
                  type: "submit",
                  children: [
                    /* @__PURE__ */ jsx(Check, { "aria-hidden": "true", className: "size-3.5" }),
                    isLast ? text.submitAnswers : text.nextQuestion
                  ]
                }
              )
            ] })
          ] })
        ]
      }
    ) : null
  ] }) });
}
function ApprovalDock({
  disabled = false,
  onError,
  permissions,
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  const resolvingRef = useRef(null);
  const [resolvingId, setResolvingId] = useState(null);
  if (permissions.length === 0) return null;
  const permission = permissions[0];
  const isResolving = disabled || resolvingId === permission.id;
  const resolve = (result) => {
    if (resolvingRef.current === permission.id) return;
    resolvingRef.current = permission.id;
    setResolvingId(permission.id);
    void runtime.resolvePermission(
      sessionId,
      crypto.randomUUID(),
      permission.id,
      result
    ).catch((error) => {
      if (resolvingRef.current === permission.id) {
        resolvingRef.current = null;
        setResolvingId(null);
      }
      onError(error);
    });
  };
  if (permission.kind === "question") {
    return /* @__PURE__ */ jsx(
      PermissionQuestionForm,
      {
        disabled: isResolving,
        onReject: () => resolve({ action: "decline" }),
        onSubmit: (answers) => resolve({ action: "accept", content: { answers } }),
        permission
      },
      permission.id
    );
  }
  return /* @__PURE__ */ jsx("section", { className: "border-t border-border bg-surface px-4 py-4 text-foreground sm:px-6", children: /* @__PURE__ */ jsxs("div", { className: `${CONVERSATION_CONTENT_WIDTH} rounded-lg border border-warning/40 bg-warning-bg p-4 shadow-soft`, children: [
    /* @__PURE__ */ jsxs("div", { className: "flex items-start gap-3", children: [
      /* @__PURE__ */ jsx(ShieldAlert, { className: "mt-0.5 size-4 shrink-0 text-warning" }),
      /* @__PURE__ */ jsxs("div", { className: "min-w-0 flex-1", children: [
        /* @__PURE__ */ jsx("div", { className: "text-sm font-semibold", children: permission.title }),
        /* @__PURE__ */ jsx("div", { className: "mt-1 text-xs leading-5 text-muted-foreground", children: permission.description })
      ] })
    ] }),
    /* @__PURE__ */ jsxs("div", { className: "mt-3 flex justify-end gap-2", children: [
      /* @__PURE__ */ jsx(
        "button",
        {
          className: "min-h-11 rounded-md border border-border px-3 text-xs font-medium transition-colors duration-fast hover:bg-background disabled:opacity-50",
          disabled: isResolving,
          onClick: () => resolve({ action: "decline" }),
          type: "button",
          children: text.reject
        }
      ),
      /* @__PURE__ */ jsx(
        "button",
        {
          className: "min-h-11 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors duration-fast hover:bg-primary/90 disabled:opacity-50",
          disabled: isResolving,
          onClick: () => resolve({ action: "accept", content: { answers: {} } }),
          type: "button",
          children: text.approve
        }
      )
    ] })
  ] }) });
}
function ComposerAttachments({
  draft,
  children,
  disabled
}) {
  const inputRef = useRef(null);
  const { attachments, uploadError, uploading } = draft;
  const text = useAgentWorkspaceText();
  const handleFile = (event) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (!disabled) void draft.uploadFiles(files);
  };
  if (!draft.supported) return /* @__PURE__ */ jsx(Fragment, { children });
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    /* @__PURE__ */ jsx(
      "button",
      {
        "aria-label": text.addAttachment,
        className: "flex size-9 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40",
        disabled: disabled || uploading,
        onClick: () => inputRef.current?.click(),
        title: text.addAttachment,
        type: "button",
        children: uploading ? /* @__PURE__ */ jsx(LoaderCircle, { className: "size-4 animate-spin" }) : /* @__PURE__ */ jsx(Paperclip, { className: "size-4" })
      }
    ),
    /* @__PURE__ */ jsx(
      "input",
      {
        className: "hidden",
        "data-testid": "agent-attachment-input",
        disabled: disabled || uploading,
        multiple: true,
        onChange: handleFile,
        ref: inputRef,
        type: "file"
      }
    ),
    children,
    attachments.length > 0 && /* @__PURE__ */ jsx("div", { className: "flex basis-full flex-wrap gap-1.5 pt-1", children: attachments.map((attachment) => /* @__PURE__ */ jsxs(
      "span",
      {
        className: "flex h-10 max-w-56 items-center gap-1.5 rounded-md border border-border bg-muted/50 px-2 text-xs",
        children: [
          attachment.mediaType.startsWith("image/") && draft.previewUrl(attachment.id) ? /* @__PURE__ */ jsx(
            "img",
            {
              alt: attachment.name,
              className: "size-8 shrink-0 rounded-sm object-cover",
              src: draft.previewUrl(attachment.id)
            }
          ) : /* @__PURE__ */ jsx(FileText, { className: "size-3.5 shrink-0 text-muted-foreground" }),
          /* @__PURE__ */ jsx("span", { className: "truncate", children: attachment.name }),
          /* @__PURE__ */ jsx(
            "button",
            {
              "aria-label": `${text.removeAttachment}: ${attachment.name}`,
              className: "inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40",
              disabled,
              onClick: () => draft.remove(attachment.id),
              type: "button",
              children: /* @__PURE__ */ jsx(X, { className: "size-3.5" })
            }
          )
        ]
      },
      attachment.id
    )) }),
    uploadError && /* @__PURE__ */ jsx("span", { className: "basis-full text-xs text-destructive", role: "alert", children: uploadError })
  ] });
}
function ConfigurationSelect({
  disabled,
  label,
  loading = false,
  onChange,
  options,
  optionsLabel,
  placeholder,
  value
}) {
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef(null);
  const triggerRef = useRef(null);
  const optionRefs = useRef([]);
  const listboxId = useId();
  const selectedIndex = options.findIndex((option) => option.value === value);
  const focusIndex = selectedIndex >= 0 ? selectedIndex : 0;
  const selected = selectedIndex >= 0 ? options[selectedIndex] : void 0;
  const hasChoice = options.some((option) => option.value !== value);
  useEffect(() => {
    if (!open) return;
    const close = (event) => {
      if (!rootRef.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", close, true);
    return () => document.removeEventListener("pointerdown", close, true);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    setActiveIndex(focusIndex);
    requestAnimationFrame(() => optionRefs.current[focusIndex]?.focus());
  }, [focusIndex, open]);
  const move = (offset) => {
    const next = (activeIndex + offset + options.length) % options.length;
    setActiveIndex(next);
    optionRefs.current[next]?.focus();
  };
  const choose = (nextValue) => {
    setOpen(false);
    requestAnimationFrame(() => triggerRef.current?.focus());
    if (nextValue !== value) onChange(nextValue);
  };
  const handleOptionKeyDown = (event, index) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      move(event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Home" || event.key === "End") {
      event.preventDefault();
      const next = event.key === "Home" ? 0 : options.length - 1;
      setActiveIndex(next);
      optionRefs.current[next]?.focus();
    } else if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      choose(options[index].value);
    }
  };
  return /* @__PURE__ */ jsxs(
    "div",
    {
      className: "relative min-w-0",
      onKeyDown: (event) => {
        if (!open || event.key !== "Escape" || event.defaultPrevented) return;
        event.preventDefault();
        event.stopPropagation();
        setOpen(false);
        triggerRef.current?.focus();
      },
      onBlur: (event) => {
        if (open && !event.currentTarget.contains(event.relatedTarget)) {
          setOpen(false);
        }
      },
      ref: rootRef,
      children: [
        /* @__PURE__ */ jsxs(
          "button",
          {
            "aria-busy": loading || void 0,
            "aria-controls": listboxId,
            "aria-expanded": open,
            "aria-haspopup": "listbox",
            "aria-label": label,
            className: "flex h-8 max-w-40 items-center gap-1.5 rounded-md px-2 text-xs text-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:text-muted-foreground",
            disabled: disabled || loading || !hasChoice,
            onClick: () => setOpen((current) => !current),
            onKeyDown: (event) => {
              if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
              event.preventDefault();
              setOpen(true);
            },
            role: "combobox",
            ref: triggerRef,
            title: label,
            type: "button",
            children: [
              /* @__PURE__ */ jsx("span", { className: "truncate", children: selected?.label ?? placeholder }),
              loading ? /* @__PURE__ */ jsx(LoaderCircle, { className: "size-3.5 shrink-0 animate-spin" }) : /* @__PURE__ */ jsx(
                ChevronDown,
                {
                  className: `size-3.5 shrink-0 transition-transform ${open ? "rotate-180" : ""}`
                }
              )
            ]
          }
        ),
        open && /* @__PURE__ */ jsx(
          "div",
          {
            "aria-label": optionsLabel,
            className: "absolute bottom-full left-0 z-30 mb-2 max-h-[min(20rem,calc(100dvh-8rem))] w-max min-w-52 max-w-72 overflow-y-auto overflow-x-hidden overscroll-contain rounded-md border border-border bg-popover p-1 shadow-lg",
            id: listboxId,
            role: "listbox",
            children: options.map((option, index) => {
              const selectedOption = option.value === value;
              return /* @__PURE__ */ jsxs(
                "button",
                {
                  "aria-selected": selectedOption,
                  className: "flex min-h-10 w-full items-start gap-2 rounded px-2.5 py-2 text-left text-sm outline-none hover:bg-muted focus:bg-muted",
                  onClick: () => choose(option.value),
                  onFocus: () => setActiveIndex(index),
                  onKeyDown: (event) => handleOptionKeyDown(event, index),
                  ref: (element) => {
                    optionRefs.current[index] = element;
                  },
                  role: "option",
                  tabIndex: index === activeIndex ? 0 : -1,
                  type: "button",
                  children: [
                    /* @__PURE__ */ jsx(
                      Check,
                      {
                        className: `mt-0.5 size-4 shrink-0 ${selectedOption ? "opacity-100" : "opacity-0"}`
                      }
                    ),
                    /* @__PURE__ */ jsxs("span", { className: "min-w-0", children: [
                      /* @__PURE__ */ jsx("span", { className: "block font-medium", children: option.label }),
                      option.description && /* @__PURE__ */ jsx("span", { className: "mt-0.5 block text-xs text-muted-foreground", children: option.description })
                    ] })
                  ]
                },
                option.value
              );
            })
          }
        )
      ]
    }
  );
}
function ComposerConfigurationBar({
  modelControl,
  onError,
  runtime,
  snapshot
}) {
  const [pending, setPending] = useState(null);
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    modelControl,
    (snapshot.configuration ?? []).map((control) => {
      if (control.id === "model" && modelControl !== void 0) return null;
      const label = text.configurationLabel(control.id, control.label);
      const options = control.options.map((option) => ({
        ...option,
        label: text.configurationOption(control.id, option.value, option.label)
      }));
      return /* @__PURE__ */ jsx(
        ConfigurationSelect,
        {
          disabled: !snapshot.capabilities.updateConfiguration,
          label,
          loading: pending === control.id,
          onChange: (value) => {
            setPending(control.id);
            void runtime.updateConfiguration(
              snapshot.sessionId,
              crypto.randomUUID(),
              { [control.id]: value }
            ).catch(onError).finally(() => setPending(null));
          },
          options,
          optionsLabel: text.configurationOptions(label),
          placeholder: label,
          value: control.value
        },
        control.id
      );
    })
  ] });
}
function ComposerCapabilityBar({
  modelControl,
  onError,
  runtime,
  snapshot
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    /* @__PURE__ */ jsx(
      CapabilityLabel,
      {
        icon: /* @__PURE__ */ jsx(Sparkles, { className: "size-3.5" }),
        label: snapshot.interactionMode === "acp" ? text.agentic : text.terminalMode
      }
    ),
    /* @__PURE__ */ jsx(
      CapabilityLabel,
      {
        icon: /* @__PURE__ */ jsx(Bot, { className: "size-3.5" }),
        label: snapshot.agentLabel
      }
    ),
    /* @__PURE__ */ jsx(
      ComposerConfigurationBar,
      {
        modelControl,
        onError,
        runtime,
        snapshot
      }
    ),
    snapshot.capabilities.resolvePermission && /* @__PURE__ */ jsx(
      CapabilityLabel,
      {
        icon: /* @__PURE__ */ jsx(ShieldCheck, { className: "size-3.5" }),
        label: text.approvals
      }
    ),
    snapshot.capabilities.terminal && snapshot.terminals.length > 0 && /* @__PURE__ */ jsx(
      CapabilityLabel,
      {
        icon: /* @__PURE__ */ jsx(SquareTerminal, { className: "size-3.5" }),
        label: text.terminal
      }
    )
  ] });
}
function CapabilityLabel({
  icon,
  label
}) {
  return /* @__PURE__ */ jsxs(
    "span",
    {
      className: "flex h-8 min-w-0 items-center gap-1.5 px-2 text-xs",
      title: label,
      children: [
        /* @__PURE__ */ jsx("span", { className: "shrink-0", children: icon }),
        /* @__PURE__ */ jsx("span", { className: "max-w-28 truncate", children: label })
      ]
    }
  );
}
function composerRecoveryActions(input) {
  if (input.sendMessage) return [];
  if (input.readOnlyReason === "disconnected") return input.canReconnect ? ["reconnect"] : [];
  if (input.readOnlyReason && input.readOnlyReason !== "ended") return [];
  const actions = [];
  if (input.canWake) actions.push("wake");
  if (input.canReconnect) actions.push("reconnect");
  return actions;
}
function workerComposerRecovery(input) {
  if (input.readOnlyReason === "paused") return { readOnlyReason: "paused" };
  if (input.readOnlyReason === "disconnected") {
    return { readOnlyReason: "disconnected", onReconnect: input.onReconnect };
  }
  if (input.readOnlyReason !== "ended") return {};
  return { readOnlyReason: "ended", onReconnect: input.onReconnect, onWake: input.onWake };
}
const hints = {
  "zh-CN": {
    paused: "Worker 已暂停，当前可以查看历史记录。恢复运行后即可继续输入。",
    disconnected: "Worker 已断开，历史记录仍可查看。正在等待连接恢复，也可以重新连接。"
  },
  "en-US": {
    paused: "This Worker is paused. You can view its history; input becomes available after it resumes.",
    disconnected: "This Worker is disconnected. History is still available while the connection recovers. You can also reconnect."
  }
};
function workerSessionRecoveryHint(reason, locale) {
  return reason === "paused" || reason === "disconnected" ? hints[locale][reason] : null;
}
function ComposerSessionRecovery({
  sendMessage,
  sessionRecovery
}) {
  const text = useAgentWorkspaceText();
  const locale = useAgentWorkspaceLocale();
  const actions = composerRecoveryActions({
    canReconnect: Boolean(sessionRecovery?.onReconnect),
    canWake: Boolean(sessionRecovery?.onWake),
    sendMessage,
    readOnlyReason: sessionRecovery?.readOnlyReason
  });
  const hint = !sendMessage ? workerSessionRecoveryHint(sessionRecovery?.readOnlyReason, locale) : null;
  if (actions.length === 0 && !hint) return null;
  return /* @__PURE__ */ jsxs("div", { className: "flex flex-wrap items-center gap-2", children: [
    /* @__PURE__ */ jsx("p", { className: "w-full text-xs text-muted-foreground", children: hint ?? text.sessionNeedsRecovery }),
    actions.includes("wake") ? /* @__PURE__ */ jsx(
      "button",
      {
        className: "inline-flex h-8 items-center rounded-md border bg-background px-2.5 text-xs text-foreground",
        onClick: sessionRecovery?.onWake,
        type: "button",
        children: text.wakeWorker
      }
    ) : null,
    actions.includes("reconnect") ? /* @__PURE__ */ jsx(
      "button",
      {
        className: "inline-flex h-8 items-center rounded-md border bg-background px-2.5 text-xs text-foreground",
        onClick: sessionRecovery?.onReconnect,
        type: "button",
        children: text.reconnectSession
      }
    ) : null
  ] });
}
function QueueItemStatusIcon({ status }) {
  const Icon = status === "dispatching" ? LoaderCircle : status === "failed" ? AlertTriangle : status === "cancelled" ? CircleStop : status === "queued" ? Clock3 : CheckCircle2;
  const tone = status === "dispatching" ? "animate-spin text-primary" : status === "failed" ? "text-danger" : "text-muted-foreground";
  return /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: `mt-0.5 size-3.5 shrink-0 ${tone}` });
}
function QueueActions({
  editing,
  item,
  onCancelEdit,
  onEdit,
  onSave,
  runtime,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  if (item.status === "queued" && runtime.cancelQueuedMessage) {
    return /* @__PURE__ */ jsxs(Fragment, { children: [
      /* @__PURE__ */ jsx(
        QueueButton,
        {
          label: editing ? text.saveQueuedMessage : text.editQueuedMessage,
          onClick: editing ? onSave : onEdit,
          children: editing ? /* @__PURE__ */ jsx(Check, { "aria-hidden": "true", className: "size-3.5" }) : /* @__PURE__ */ jsx(Pencil, { "aria-hidden": "true", className: "size-3.5" })
        }
      ),
      editing ? /* @__PURE__ */ jsx(QueueButton, { label: text.cancelQueuedEdit, onClick: onCancelEdit, children: /* @__PURE__ */ jsx(X, { "aria-hidden": "true", className: "size-3.5" }) }) : /* @__PURE__ */ jsx(
        QueueButton,
        {
          label: text.cancelQueuedMessage,
          onClick: () => runtime.cancelQueuedMessage?.(sessionId, item.commandId),
          children: /* @__PURE__ */ jsx(X, { "aria-hidden": "true", className: "size-3.5" })
        }
      )
    ] });
  }
  if (item.status === "failed" && runtime.retryQueuedMessage) {
    return /* @__PURE__ */ jsxs(Fragment, { children: [
      /* @__PURE__ */ jsx(QueueButton, { label: text.retryQueuedMessage, onClick: () => runtime.retryQueuedMessage?.(sessionId, item.commandId), children: /* @__PURE__ */ jsx(RotateCcw, { "aria-hidden": "true", className: "size-3.5" }) }),
      runtime.removeQueuedMessage ? /* @__PURE__ */ jsx(QueueButton, { label: text.removeQueuedMessage, onClick: () => runtime.removeQueuedMessage?.(sessionId, item.commandId), children: /* @__PURE__ */ jsx(Trash2, { "aria-hidden": "true", className: "size-3.5" }) }) : null
    ] });
  }
  if (item.status === "cancelled" && runtime.removeQueuedMessage) {
    return /* @__PURE__ */ jsx(QueueButton, { label: text.removeQueuedMessage, onClick: () => runtime.removeQueuedMessage?.(sessionId, item.commandId), children: /* @__PURE__ */ jsx(Trash2, { "aria-hidden": "true", className: "size-3.5" }) });
  }
  return null;
}
function QueueButton({
  children,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": label,
      className: "inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
      onClick,
      title: label,
      type: "button",
      children
    }
  );
}
function queueStatus(status, text) {
  if (status === "dispatching") return text.queueDispatching;
  if (status === "failed") return text.messageFailed;
  if (status === "cancelled") return text.queueCancelled;
  return text.queuePending;
}
function TaskQueuePreview({
  runtime,
  sessionId,
  queue
}) {
  const text = useAgentWorkspaceText();
  const [editingId, setEditingId] = useState(null);
  const [editingText, setEditingText] = useState("");
  if (!queue || queue.items.length === 0) return null;
  const startEdit = (commandId, value) => {
    setEditingId(commandId);
    setEditingText(value);
  };
  const cancelEdit = () => {
    setEditingId(null);
    setEditingText("");
  };
  const saveEdit = (commandId) => {
    if (!runtime.updateQueuedMessage?.(sessionId, commandId, { text: editingText })) return;
    cancelEdit();
  };
  return /* @__PURE__ */ jsxs(
    "section",
    {
      "aria-label": text.queuedMessages,
      "aria-live": "polite",
      className: "mb-1 rounded-md border border-border bg-muted/20 px-2.5 py-2",
      "data-testid": "agent-task-queue",
      children: [
        /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-2 text-xs font-medium text-foreground", children: [
          /* @__PURE__ */ jsx(ListOrdered, { "aria-hidden": "true", className: "size-3.5 text-primary" }),
          /* @__PURE__ */ jsx("span", { children: text.queuedMessages }),
          /* @__PURE__ */ jsx("span", { className: "text-muted-foreground", children: queue.items.length })
        ] }),
        /* @__PURE__ */ jsx("div", { className: "mt-1 space-y-1", children: queue.items.map((item) => /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 items-start gap-2 text-xs", children: [
          /* @__PURE__ */ jsx(QueueItemStatusIcon, { status: item.status }),
          editingId === item.commandId ? /* @__PURE__ */ jsx(
            "input",
            {
              "aria-label": text.editQueuedMessage,
              autoFocus: true,
              className: "min-w-0 flex-1 rounded-sm border border-ring bg-background px-1.5 py-1 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring",
              onChange: (event) => setEditingText(event.target.value),
              onKeyDown: (event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  saveEdit(item.commandId);
                }
                if (event.key === "Escape") cancelEdit();
              },
              value: editingText
            }
          ) : /* @__PURE__ */ jsx(
            "span",
            {
              className: "max-h-10 min-w-0 flex-1 overflow-y-auto whitespace-pre-wrap break-words text-muted-foreground [overflow-wrap:anywhere]",
              title: item.text,
              children: item.text || text.addAttachment
            }
          ),
          /* @__PURE__ */ jsx("span", { className: "shrink-0 text-muted-foreground", children: queueStatus(item.status, text) }),
          /* @__PURE__ */ jsx(
            QueueActions,
            {
              editing: editingId === item.commandId,
              item,
              onCancelEdit: cancelEdit,
              onEdit: () => startEdit(item.commandId, item.text),
              onSave: () => saveEdit(item.commandId),
              runtime,
              sessionId
            }
          )
        ] }, item.commandId)) }),
        queue.items.some((item) => item.error) ? /* @__PURE__ */ jsx("div", { className: "mt-1 text-[11px] text-danger", role: "status", children: queue.items.find((item) => item.error)?.error }) : null
      ]
    }
  );
}
function ComposerActiveBar({
  attachmentDraft,
  canQueue,
  canCompose,
  hasDraft,
  isRunning,
  onError,
  presentation,
  runtime,
  renderModelControl,
  sending,
  sessionRecovery,
  showInterrupt,
  snapshot
}) {
  const text = useAgentWorkspaceText();
  const modelControl = renderModelControl?.({ snapshot, disabled: !canCompose || isRunning });
  return /* @__PURE__ */ jsxs("div", { className: "flex min-h-11 items-end justify-between gap-2 px-2 pb-2", children: [
    /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 flex-1 flex-col gap-1", children: [
      /* @__PURE__ */ jsx(
        TaskQueuePreview,
        {
          queue: snapshot.queue,
          runtime,
          sessionId: snapshot.sessionId
        }
      ),
      /* @__PURE__ */ jsx("div", { className: "flex min-w-0 flex-wrap items-center gap-0.5 text-muted-foreground", "data-testid": "agent-composer-controls", children: /* @__PURE__ */ jsx(
        ComposerAttachments,
        {
          draft: attachmentDraft,
          disabled: !canCompose || isRunning && !canQueue,
          children: presentation === "developer" ? /* @__PURE__ */ jsx(
            ComposerCapabilityBar,
            {
              modelControl,
              onError,
              runtime,
              snapshot
            }
          ) : /* @__PURE__ */ jsx(
            ComposerConfigurationBar,
            {
              modelControl,
              onError,
              runtime,
              snapshot
            }
          )
        }
      ) }),
      /* @__PURE__ */ jsx(ComposerSessionRecovery, { sendMessage: snapshot.capabilities.sendMessage, sessionRecovery })
    ] }),
    /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 items-center gap-1.5", children: [
      showInterrupt ? /* @__PURE__ */ jsx(
        "button",
        {
          "aria-label": text.stopAgent,
          className: "flex size-11 items-center justify-center rounded-full border border-destructive/50 text-destructive outline-none hover:bg-destructive/10 focus-visible:ring-2 focus-visible:ring-ring",
          onClick: () => void runtime.interrupt(snapshot.sessionId, crypto.randomUUID()).catch(onError),
          title: text.stopAgent,
          type: "button",
          children: /* @__PURE__ */ jsx(Square, { "aria-hidden": "true", className: "size-3.5 fill-current" })
        }
      ) : null,
      /* @__PURE__ */ jsx(
        "button",
        {
          "aria-label": canQueue ? text.queueMessage : text.sendMessage,
          className: "flex size-11 items-center justify-center rounded-full bg-primary text-primary-foreground outline-none hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-30",
          disabled: !hasDraft || !canCompose || isRunning && !canQueue || attachmentDraft.uploading,
          title: canQueue ? text.queueMessage : text.sendMessage,
          type: "submit",
          children: sending ? /* @__PURE__ */ jsx(LoaderCircle, { className: "size-4 animate-spin" }) : /* @__PURE__ */ jsx(ArrowUp, { "aria-hidden": "true", className: "size-4" })
        }
      )
    ] })
  ] });
}
const BY_HARNESS = /* @__PURE__ */ new Map([
  ["claude-native", { key: "claude" }],
  ["codex-native", { key: "codex" }],
  ["codex", { key: "codex" }],
  ["cursor-native", { key: "cursor" }],
  ["pi-native", { key: "pi" }],
  ["opencode-native", { key: "opencode" }],
  ["kiro-native", { key: "kiro" }],
  ["antigravity-native", { key: "antigravity" }],
  ["goose-native", { key: "goose" }],
  ["qwen-native", { key: "qwen" }],
  ["kimi-native", { key: "kimi" }],
  ["hermes-native", { key: "hermes" }]
]);
const HARNESS_ALIASES = {
  "native-pi": "pi-native",
  "native-cursor": "cursor-native",
  "native-kiro": "kiro-native",
  "native-antigravity": "antigravity-native",
  "native-goose": "goose-native",
  "native-qwen": "qwen-native",
  "native-kimi": "kimi-native",
  "native-hermes": "hermes-native",
  "native-codex": "codex-native",
  "native-opencode": "opencode-native"
};
function nativeCodingAgentForHarness(harness) {
  if (harness == null) return void 0;
  return BY_HARNESS.get(HARNESS_ALIASES[harness] ?? harness);
}
function mentionItemKey(item) {
  const range = item.lineRange ? `${item.lineRange.start}-${item.lineRange.end}` : "";
  return `${item.path}|${item.isDir ? "1" : "0"}|${range}`;
}
function mentionItemPath(item) {
  if (item.lineRange) {
    return `${item.path}:${item.lineRange.start}-${item.lineRange.end}`;
  }
  return item.isDir ? `${item.path}/` : item.path;
}
function mentionMarkerFor(harness, path) {
  return nativeCodingAgentForHarness(harness)?.key === "codex" ? `[Attached file: ${path}]` : `[Attached: ${path}]`;
}
function buildMentionPreamble(items, harness) {
  if (items.length === 0) return "";
  return items.map((i) => mentionMarkerFor(harness, mentionItemPath(i))).join("\n") + "\n\n";
}
function MentionChips({
  items,
  disabled,
  onRemove
}) {
  if (items.length === 0) return null;
  return /* @__PURE__ */ jsx("div", { className: "flex flex-wrap gap-1 px-4 pb-1", children: items.map((item, index) => /* @__PURE__ */ jsxs(
    "span",
    {
      className: "inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-muted px-2 py-0.5 text-xs",
      children: [
        /* @__PURE__ */ jsx("span", { className: "truncate", children: mentionItemPath(item) }),
        /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": `Remove ${mentionItemPath(item)}`,
            className: "shrink-0 opacity-60 hover:opacity-100 disabled:opacity-30",
            disabled,
            onClick: () => onRemove(index),
            type: "button",
            children: /* @__PURE__ */ jsx(X, { className: "size-3" })
          }
        )
      ]
    },
    `${item.path}:${item.isDir}:${index}`
  )) });
}
function MentionMenu({
  entries,
  selectedIndex,
  onSelect,
  onOpenDir
}) {
  if (entries.length === 0) return null;
  return /* @__PURE__ */ jsx(
    "ul",
    {
      className: "absolute inset-x-0 bottom-full z-20 mb-1 max-h-56 overflow-y-auto rounded-md border border-border bg-popover py-1 text-sm shadow-md",
      role: "listbox",
      children: entries.map((entry, index) => {
        const selected = index === selectedIndex;
        const isDir = entry.type === "directory";
        return /* @__PURE__ */ jsx("li", { role: "option", "aria-selected": selected, children: /* @__PURE__ */ jsxs(
          "button",
          {
            className: `flex w-full items-center gap-2 px-3 py-1.5 text-left ${selected ? "bg-accent text-accent-foreground" : ""}`,
            onMouseDown: (event) => event.preventDefault(),
            onClick: () => isDir ? onOpenDir(entry.path) : onSelect(entry.path, false),
            type: "button",
            children: [
              isDir ? /* @__PURE__ */ jsx(Folder, { className: "size-3.5 shrink-0" }) : /* @__PURE__ */ jsx(File, { className: "size-3.5 shrink-0" }),
              /* @__PURE__ */ jsx("span", { className: "truncate", children: entry.name })
            ]
          }
        ) }, `${entry.type}:${entry.path}`);
      })
    }
  );
}
function ComposerMentionField({
  mentions,
  value,
  onChange,
  onKeyDown,
  onPaste,
  disabled,
  ariaLabel,
  placeholder,
  textareaRef,
  mobile = false
}) {
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    mentions.enabled && /* @__PURE__ */ jsx(
      MentionMenu,
      {
        entries: mentions.ranked,
        onOpenDir: mentions.browser.openMentionDir,
        onSelect: mentions.browser.attachMention,
        selectedIndex: mentions.browser.mentionIndex
      }
    ),
    /* @__PURE__ */ jsx(
      MentionChips,
      {
        disabled,
        items: mentions.browser.mentionedItems,
        onRemove: mentions.browser.removeMentionedItem
      }
    ),
    /* @__PURE__ */ jsx(
      "textarea",
      {
        "aria-label": ariaLabel,
        className: `w-full resize-none bg-transparent px-4 pb-2 pt-3 leading-6 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-60 ${mobile ? "min-h-20 max-h-44 text-base" : "min-h-24 max-h-56 text-sm"}`,
        disabled,
        enterKeyHint: mobile ? "enter" : "send",
        name: "message",
        onBlur: () => mentions.browser.dismiss(),
        onChange: (event) => onChange(event.target.value, event.target.selectionStart ?? 0),
        onKeyDown,
        onPaste,
        onSelect: (event) => onChange(
          event.currentTarget.value,
          event.currentTarget.selectionStart ?? 0
        ),
        placeholder,
        ref: textareaRef,
        rows: mobile ? 2 : 3,
        value
      }
    )
  ] });
}
function ComposerCommandMenu({
  commands,
  onSelect,
  query
}) {
  if (query === null) return null;
  const matches = commands.filter(
    (command) => command.name.toLowerCase().startsWith(query.toLowerCase())
  );
  if (matches.length === 0) return null;
  return /* @__PURE__ */ jsx("div", { className: "absolute inset-x-0 bottom-full z-20 mb-2 overflow-hidden rounded-md border border-border bg-popover shadow-lg", children: matches.map((command) => /* @__PURE__ */ jsxs(
    "button",
    {
      "aria-label": command.label,
      className: "flex w-full items-start gap-3 px-3 py-2 text-left hover:bg-muted focus-visible:bg-muted focus-visible:outline-none",
      onClick: () => onSelect(command),
      type: "button",
      children: [
        /* @__PURE__ */ jsx("span", { className: "w-20 shrink-0 font-mono text-xs font-medium", children: command.label }),
        /* @__PURE__ */ jsx("span", { className: "text-xs text-muted-foreground", children: command.description })
      ]
    },
    command.name
  )) });
}
function commandQuery(value) {
  const match = /^\/([A-Za-z0-9_-]*)$/.exec(value);
  return match ? match[1] : null;
}
function parseAgentCommand(value, commands) {
  const trimmed = value.trim();
  const match = /^\/([A-Za-z0-9_-]+)(?:\s+(.*))?$/.exec(trimmed);
  if (!match) return null;
  const command = commands.find((candidate) => candidate.name === match[1]);
  if (!command) return null;
  return { command, arguments: match[2]?.trim() ?? "" };
}
function codeReferencePreamble(references) {
  return references.map((reference) => {
    const range = `${reference.startLine}-${reference.endLine}`;
    const source = reference.source === "workspace" ? "workspace" : reference.source;
    return [
      "[Code reference]",
      `path=${reference.path}`,
      `lines=${range}`,
      `source=${source}`,
      `digest=${reference.contentDigest}`,
      "```",
      reference.text,
      "```",
      "[/Code reference]"
    ].join("\n");
  }).join("\n\n");
}
function selectionLineRange(text, from, to) {
  const start = Math.max(0, Math.min(from, text.length));
  const end = Math.max(start, Math.min(to, text.length));
  if (start === end) return null;
  const selected = text.slice(start, end);
  const startLine = lineAt(text, start);
  const endLine = lineAt(text, Math.max(start, end - 1));
  return { endLine: startLine + selected.split("\n").length - 1 > endLine ? endLine + 1 : endLine, startLine };
}
function lineAt(text, offset) {
  let line = 1;
  for (let index = 0; index < offset; index += 1) {
    if (text[index] === "\n") line += 1;
  }
  return line;
}
async function submitComposerMessage(options) {
  const {
    message,
    attachments,
    mentioned,
    outboundText,
    codeReferences,
    runtime,
    snapshot,
    queueWhileActive = false
  } = options;
  if (!message && attachments.length === 0 && mentioned.length === 0 && codeReferences.length === 0 || !snapshot.capabilities.sendMessage) {
    return "blocked";
  }
  const parsedCommand = parseAgentCommand(message, snapshot.commands ?? []);
  if (parsedCommand?.command.requiresArgument && !parsedCommand.arguments) {
    throw new Error(options.requiresArgument(parsedCommand.command.label));
  }
  if (parsedCommand && (attachments.length > 0 || mentioned.length > 0 || codeReferences.length > 0)) {
    throw new Error(options.slashAttachmentsUnsupported);
  }
  if (parsedCommand && runtime.sendSlashCommand) {
    if (queueWhileActive) {
      if (!runtime.enqueueSlashCommand) {
        throw new Error("agent_queue_slash_command_unsupported");
      }
      await runtime.enqueueSlashCommand(snapshot.sessionId, crypto.randomUUID(), {
        name: parsedCommand.command.name,
        arguments: parsedCommand.arguments
      });
      return "queued";
    }
    await runtime.sendSlashCommand(snapshot.sessionId, crypto.randomUUID(), {
      name: parsedCommand.command.name,
      arguments: parsedCommand.arguments
    });
    return "sent";
  }
  const input = {
    text: [codeReferencePreamble(codeReferences), outboundText].filter(Boolean).join("\n\n"),
    ...attachments.length > 0 ? { attachments } : {}
  };
  if (queueWhileActive && runtime.enqueueMessage) {
    await runtime.enqueueMessage(snapshot.sessionId, crypto.randomUUID(), input);
    return "queued";
  }
  await runtime.sendMessage(snapshot.sessionId, crypto.randomUUID(), input);
  return "sent";
}
const MENTION_MATCH_CAP = 50;
function rankMentionEntries(entries, filter, cap = MENTION_MATCH_CAP) {
  const needle = filter.toLowerCase();
  return entries.filter((e) => e.name.toLowerCase().includes(needle)).sort(
    (a, b) => a.type !== b.type ? a.type === "directory" ? -1 : 1 : a.name.localeCompare(b.name)
  ).slice(0, cap);
}
const MENTION_RE = /(?:^|\s)@([^\s@]*)$/;
function detectMentionAt(text, caret) {
  const before = text.slice(0, caret);
  const m = MENTION_RE.exec(before);
  if (!m) return null;
  const query = m[1] ?? "";
  return { query, start: caret - query.length - 1, end: caret };
}
function parseMentionToken(query) {
  const slash = query.lastIndexOf("/");
  return slash >= 0 ? { dir: query.slice(0, slash), filter: query.slice(slash + 1) } : { dir: "", filter: query };
}
function useMentionBrowser(params) {
  const {
    mention,
    setMention,
    mentionEntries,
    text,
    setText,
    textareaRef,
    isMobile = false
  } = params;
  const [mentionIndex, setMentionIndex] = useState(-1);
  const [mentionedItems, setMentionedItems] = useState([]);
  const mentionOpen = mentionEntries.length > 0;
  const prevKeys = useRef([]);
  const entryKeys = mentionEntries.map((e) => `${e.type}:${e.path}`);
  if (entryKeys.length !== prevKeys.current.length || entryKeys.some((k, i) => k !== prevKeys.current[i])) {
    prevKeys.current = entryKeys;
    setMentionIndex(entryKeys.length > 0 ? 0 : -1);
  }
  const attachMention = (path, isDir) => {
    if (!mention) return;
    setText(text.slice(0, mention.start) + text.slice(mention.end));
    const item = { path, isDir };
    const key = mentionItemKey(item);
    setMentionedItems(
      (prev) => prev.some((it) => mentionItemKey(it) === key) ? prev : [...prev, item]
    );
    setMention(null);
    setMentionIndex(-1);
    queueMicrotask(() => {
      const ta = textareaRef.current;
      if (ta) ta.setSelectionRange(mention.start, mention.start);
      ta?.focus();
    });
  };
  const openMentionDir = (path) => {
    if (!mention) return;
    const inserted = `@${path}/`;
    const next = text.slice(0, mention.start) + inserted + text.slice(mention.end);
    setText(next);
    const caret = mention.start + inserted.length;
    setMention({ query: `${path}/`, start: mention.start, end: caret });
    setMentionIndex(0);
    queueMicrotask(() => {
      const ta = textareaRef.current;
      if (ta) ta.setSelectionRange(caret, caret);
      ta?.focus();
    });
  };
  const removeMentionedItem = (index) => setMentionedItems((prev) => prev.filter((_, i) => i !== index));
  const dismiss = () => {
    if (!mention) return;
    setMention(null);
    setMentionIndex(-1);
  };
  const handleKeyDown = (e) => {
    if (!mentionOpen) return false;
    const active = mentionIndex >= 0 ? mentionEntries[mentionIndex] : void 0;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setMentionIndex((i) => (i + 1) % mentionEntries.length);
      return true;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setMentionIndex((i) => i <= 0 ? mentionEntries.length - 1 : i - 1);
      return true;
    }
    if (e.key === "Enter" && !e.shiftKey && !isMobile && active) {
      e.preventDefault();
      if (active.type === "directory") openMentionDir(active.path);
      else attachMention(active.path, false);
      return true;
    }
    if (e.key === "Tab" && active) {
      e.preventDefault();
      attachMention(active.path, active.type === "directory");
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      dismiss();
      return true;
    }
    return false;
  };
  return {
    mentionIndex,
    mentionOpen,
    mentionedItems,
    setMentionedItems,
    attachMention,
    openMentionDir,
    removeMentionedItem,
    handleKeyDown,
    dismiss
  };
}
function useComposerMentions(options) {
  const { value, setValue, textareaRef, sessionId, harness, workspaceFiles, isMobile = false } = options;
  const enabled = workspaceFiles != null;
  const [mention, setMention] = useState(null);
  const [entries, setEntries] = useState([]);
  const refreshMention = (text, caret) => {
    setMention(enabled ? detectMentionAt(text, caret) : null);
  };
  const { dir, filter } = parseMentionToken(mention?.query ?? "");
  useEffect(() => {
    if (!enabled || !workspaceFiles || !mention) {
      setEntries([]);
      return;
    }
    let cancelled = false;
    void workspaceFiles.list(sessionId, dir).then((listed) => {
      if (!cancelled) setEntries(listed);
    });
    return () => {
      cancelled = true;
    };
  }, [dir, enabled, mention, sessionId, workspaceFiles]);
  const ranked = mention ? rankMentionEntries(entries, filter) : [];
  const browser = useMentionBrowser({
    mention,
    setMention,
    mentionEntries: ranked,
    text: value,
    setText: setValue,
    textareaRef,
    isMobile
  });
  return {
    enabled,
    ranked,
    browser,
    refreshMention,
    composeOutboundText: (message) => `${buildMentionPreamble(browser.mentionedItems, harness)}${message}`,
    clearMentions: () => browser.setMentionedItems([])
  };
}
function useComposerAttachments({
  runtime,
  sessionId,
  disabled
}) {
  const [attachments, setAttachments] = useState([]);
  const [uploadError, setUploadError] = useState(null);
  const [uploading, setUploading] = useState(false);
  const [previewUrls, setPreviewUrls] = useState({});
  const previewUrlsRef = useRef({});
  const pending = useRef(0);
  const mounted = useRef(true);
  const text = useAgentWorkspaceText();
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      Object.values(previewUrlsRef.current).forEach((url) => URL.revokeObjectURL(url));
    };
  }, []);
  const uploadFiles = async (files) => {
    if (disabled || !runtime.uploadAttachment || files.length === 0) return;
    pending.current += files.length;
    setUploading(true);
    setUploadError(null);
    const uploaded = await Promise.all(
      files.map(async (file) => {
        try {
          const attachment = await runtime.uploadAttachment(sessionId, file);
          return { attachment, file };
        } catch (cause) {
          if (mounted.current) {
            setUploadError(
              cause instanceof Error && cause.message === "agent_attachment_upload_unsupported" ? text.attachmentUploadUnsupported : text.attachmentUploadFailed
            );
          }
          return null;
        }
      })
    );
    if (!mounted.current) return;
    const successful = uploaded.filter((item) => item !== null);
    setAttachments((current) => [
      ...current,
      ...successful.map((item) => item.attachment)
    ]);
    setPreviewUrls((current) => {
      const next = { ...current };
      for (const { attachment, file } of successful) {
        if (attachment.mediaType.startsWith("image/")) {
          next[attachment.id] = URL.createObjectURL(file);
        }
      }
      previewUrlsRef.current = next;
      return next;
    });
    pending.current -= files.length;
    setUploading(pending.current > 0);
  };
  const onPaste = (event) => {
    if (disabled || !runtime.uploadAttachment) return;
    const clipboard = event.clipboardData;
    const files = Array.from(clipboard.files ?? []);
    if (files.length === 0) {
      for (const item of Array.from(clipboard.items ?? [])) {
        if (item.kind !== "file") continue;
        const file = item.getAsFile();
        if (file) files.push(file);
      }
    }
    if (files.length === 0) return;
    if (!clipboard.getData("text/plain")) event.preventDefault();
    void uploadFiles(files);
  };
  const onDragOver = (event) => {
    if (disabled || !runtime.uploadAttachment || !event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  };
  const onDrop = (event) => {
    if (disabled || !runtime.uploadAttachment || event.dataTransfer.files.length === 0) return;
    event.preventDefault();
    void uploadFiles(Array.from(event.dataTransfer.files));
  };
  const remove = (id) => {
    setAttachments((items) => items.filter((item) => item.id !== id));
    setPreviewUrls((current) => {
      const url = current[id];
      if (url) URL.revokeObjectURL(url);
      const next = { ...current };
      delete next[id];
      previewUrlsRef.current = next;
      return next;
    });
  };
  const clear = () => {
    Object.values(previewUrls).forEach((url) => URL.revokeObjectURL(url));
    setAttachments([]);
    previewUrlsRef.current = {};
    setPreviewUrls({});
  };
  return {
    attachments,
    uploadError,
    uploading,
    onPaste,
    onDragOver,
    onDrop,
    uploadFiles,
    previewUrl: (id) => previewUrls[id],
    supported: Boolean(runtime.uploadAttachment),
    isUploading: () => pending.current > 0,
    clear,
    remove
  };
}
function WorkspaceCodeReferenceChips({
  onRemove,
  references
}) {
  if (references.length === 0) return null;
  return /* @__PURE__ */ jsx("div", { className: "flex flex-wrap gap-1 px-3 pt-2", children: references.map((reference, index) => /* @__PURE__ */ jsxs(
    "button",
    {
      className: "max-w-full truncate rounded border border-border bg-muted/50 px-2 py-1 text-left text-[11px]",
      onClick: () => onRemove?.(index),
      title: `${reference.path}:${reference.startLine}-${reference.endLine}`,
      type: "button",
      children: [
        reference.path,
        ":",
        reference.startLine,
        "-",
        reference.endLine
      ]
    },
    `${reference.path}:${reference.startLine}:${reference.endLine}:${reference.contentDigest}`
  )) });
}
const IME_COMMIT_KEY_CODE = 229;
function SessionConversationComposer({
  onError,
  onSendingChange,
  presentation,
  mobile = false,
  runtime,
  renderModelControl,
  snapshot,
  workspaceFiles,
  mentionHarness = null,
  sessionRecovery,
  codeReferences = [],
  onCodeReferencesSent,
  onRemoveCodeReference
}) {
  const [value, setValue] = useState("");
  const [sending, setSending] = useState(false);
  const textareaRef = useRef(null);
  const text = useAgentWorkspaceText();
  const mentions = useComposerMentions({
    value,
    setValue,
    textareaRef,
    sessionId: snapshot.sessionId,
    harness: mentionHarness,
    workspaceFiles,
    isMobile: mobile
  });
  const hasActiveTool = snapshot.items.some(
    (item) => item.kind === "tool" && (item.status === "pending" || item.status === "running" || item.status === "waiting")
  );
  const isRunning = snapshot.status === "running" || snapshot.status === "waiting" || hasActiveTool;
  const parsedCommand = parseAgentCommand(value.trim(), snapshot.commands ?? []);
  const canQueue = isRunning && runtime.enqueueMessage !== void 0 && (!parsedCommand || runtime.enqueueSlashCommand !== void 0);
  const canCompose = snapshot.capabilities.sendMessage && !sending;
  const attachmentDraft = useComposerAttachments({
    runtime,
    sessionId: snapshot.sessionId,
    disabled: !canCompose
  });
  const { attachments } = attachmentDraft;
  const hasDraft = value.trim().length > 0 || attachments.length > 0 || mentions.browser.mentionedItems.length > 0 || codeReferences.length > 0;
  const showInterrupt = isRunning && snapshot.capabilities.interrupt;
  const submit = async (event) => {
    event.preventDefault();
    if (sending || attachmentDraft.isUploading() || isRunning && !canQueue) return;
    setSending(true);
    onSendingChange?.(snapshot.sessionId, true);
    try {
      const result = await submitComposerMessage({
        message: value.trim(),
        attachments,
        mentioned: mentions.browser.mentionedItems,
        outboundText: mentions.composeOutboundText(value.trim()),
        codeReferences,
        runtime,
        snapshot,
        queueWhileActive: canQueue,
        requiresArgument: text.requiresArgument,
        slashAttachmentsUnsupported: text.slashCommandAttachmentsUnsupported
      });
      if (result === "sent" || result === "queued") {
        setValue("");
        attachmentDraft.clear();
        mentions.clearMentions();
        mentions.refreshMention("", 0);
        onCodeReferencesSent?.();
      }
    } catch (cause) {
      onError(cause);
    } finally {
      setSending(false);
      onSendingChange?.(snapshot.sessionId, false);
    }
  };
  const handleKeyDown = (event) => {
    if (mentions.browser.handleKeyDown(event)) return;
    if (mobile || event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || // Safari and several Windows IME bridges commit a composition as a plain
    // Enter carrying keyCode 229 with isComposing already back to false.
    event.nativeEvent.keyCode === IME_COMMIT_KEY_CODE) {
      return;
    }
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  };
  return /* @__PURE__ */ jsx(
    "form",
    {
      className: "shrink-0 px-4 pb-3 pt-2",
      onDragOver: attachmentDraft.onDragOver,
      onDrop: attachmentDraft.onDrop,
      onSubmit: submit,
      children: /* @__PURE__ */ jsxs(
        "div",
        {
          className: `${CONVERSATION_CONTENT_WIDTH} relative rounded-lg border border-border bg-card shadow-sm transition-colors focus-within:border-ring`,
          children: [
            /* @__PURE__ */ jsx(
              WorkspaceCodeReferenceChips,
              {
                onRemove: onRemoveCodeReference,
                references: codeReferences
              }
            ),
            presentation === "developer" && /* @__PURE__ */ jsx(
              ComposerCommandMenu,
              {
                commands: snapshot.commands ?? [],
                onSelect: (command) => setValue(`${command.label}${command.requiresArgument ? " " : ""}`),
                query: commandQuery(value)
              }
            ),
            /* @__PURE__ */ jsx(
              ComposerMentionField,
              {
                ariaLabel: text.messageAgent,
                mobile,
                disabled: !canCompose,
                mentions,
                onChange: (next, caret) => {
                  setValue(next);
                  mentions.refreshMention(next, caret);
                },
                onKeyDown: handleKeyDown,
                onPaste: attachmentDraft.onPaste,
                placeholder: canCompose ? text.composerPlaceholder(snapshot.agentLabel) : text.readOnly,
                textareaRef,
                value
              }
            ),
            /* @__PURE__ */ jsx(
              ComposerActiveBar,
              {
                attachmentDraft,
                canCompose,
                hasDraft,
                isRunning,
                onError,
                presentation,
                runtime,
                renderModelControl,
                sending,
                sessionRecovery,
                showInterrupt,
                snapshot,
                canQueue
              }
            )
          ]
        }
      )
    }
  );
}
function ConversationComposer(props) {
  return /* @__PURE__ */ jsx(SessionConversationComposer, { ...props }, props.snapshot.sessionId);
}
function ConversationProgress({ progress }) {
  const text = useAgentWorkspaceText();
  if (!progress) return null;
  const Icon = progress.kind === "busy" ? Loader2 : progress.kind === "waiting" ? CirclePause : WifiOff;
  return /* @__PURE__ */ jsx("div", { className: "shrink-0 px-4", "data-testid": "agent-conversation-progress", role: "status", "aria-live": "polite", "aria-atomic": "true", children: /* @__PURE__ */ jsxs("div", { className: `${CONVERSATION_CONTENT_WIDTH} flex min-h-8 items-center gap-2 px-4 py-1 text-sm text-muted-foreground`, children: [
    /* @__PURE__ */ jsx(Icon, { "aria-hidden": "true", className: `size-4 shrink-0 ${progress.kind === "busy" ? "animate-spin motion-reduce:animate-none" : ""}` }),
    /* @__PURE__ */ jsx("span", { children: text.activityTitle(progress.title) })
  ] }) });
}
function conversationProgress(snapshot, sending = false) {
  if (snapshot.status === "failed" || snapshot.status === "completed") return null;
  const pending = sending || snapshot.items.some((item) => item.kind === "message" && item.role === "user" && item.status === "pending");
  const active = snapshot.status === "running" || snapshot.status === "waiting" || snapshot.status === "launching";
  if (!pending && !active) return null;
  if (snapshot.connection === "disconnected") return { kind: "offline", title: "Disconnected; execution status is unavailable" };
  if (snapshot.connection !== "connected") return { kind: "busy", title: "Reconnecting and syncing execution status…" };
  if (pending) return { kind: "busy", title: "Sending task…" };
  if (snapshot.status === "waiting" || snapshot.permissions.length > 0) return { kind: "waiting", title: "Waiting for your confirmation…" };
  if (snapshot.status === "launching") return { kind: "busy", title: "Starting agent…" };
  return { kind: "busy", title: activityTitle(snapshot.items) };
}
function activityTitle(items) {
  for (let index = items.length - 1; index >= 0; index--) {
    const item = items[index];
    if (item.kind === "message" && item.role === "user") break;
    if (item.kind === "tool" && (item.status === "running" || item.status === "pending")) return "Using tools…";
    if (item.kind === "reasoning" && item.status === "running") return "Thinking…";
    if (item.kind === "message" && item.role === "assistant" && item.status === "streaming") return "Writing response…";
  }
  return "Processing task…";
}
const AgentTenantBrandingContext = createContext(null);
function AgentTenantBrandingProvider({
  children,
  value
}) {
  return /* @__PURE__ */ jsx(AgentTenantBrandingContext.Provider, { value, children });
}
function useAgentTenantBranding() {
  return useContext(AgentTenantBrandingContext);
}
function resolveWorkerBranding(spec, amp) {
  if (!spec?.showLogo) return null;
  return {
    ...spec.accentColor && /^#[0-9a-f]{6}$/i.test(spec.accentColor) ? { accentColor: spec.accentColor } : {},
    showLogo: true,
    logoUrl: spec.useTenantLogo ? trim(amp?.logoUrl) : trim(spec.logoUrl),
    title: trim(spec.title) || trim(amp?.brandName),
    subtitle: trim(spec.subtitle) || trim(amp?.tenantName) || trim(amp?.brandSubtitle)
  };
}
function trim(value) {
  return String(value ?? "").trim();
}
function ConversationEmptyState({
  agentLabel,
  partner
}) {
  const text = useAgentWorkspaceText();
  const branding = resolveWorkerBranding(
    partner?.branding,
    useAgentTenantBranding()
  );
  const name = branding?.title || partner?.name.trim() || agentLabel;
  if (branding) {
    return /* @__PURE__ */ jsxs("div", { className: `${CONVERSATION_CONTENT_WIDTH} shrink-0 px-4 text-center`, children: [
      branding.logoUrl ? /* @__PURE__ */ jsx(
        "img",
        {
          alt: name,
          className: "mx-auto mb-4 size-24 object-contain",
          src: branding.logoUrl
        }
      ) : null,
      /* @__PURE__ */ jsx("h2", { className: "text-2xl font-medium leading-tight", children: name }),
      branding.subtitle ? /* @__PURE__ */ jsx("p", { className: "mt-2 text-sm text-muted-foreground", children: branding.subtitle }) : null
    ] });
  }
  return /* @__PURE__ */ jsx("div", { className: `${CONVERSATION_CONTENT_WIDTH} shrink-0 px-4 text-center`, children: /* @__PURE__ */ jsx("h2", { className: "text-2xl font-medium leading-tight", children: text.emptyHeading(name) }) });
}
function JumpToLatest({
  label,
  onClick,
  visible
}) {
  if (!visible) return null;
  return /* @__PURE__ */ jsx("div", { className: "pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center", children: /* @__PURE__ */ jsx(
    "button",
    {
      className: "pointer-events-auto flex h-8 items-center rounded-full border border-border bg-background px-3 text-xs shadow-sm",
      onClick,
      type: "button",
      children: label
    }
  ) });
}
function useHistoryAnchor(el, itemCount, loadingOlder) {
  const before = useRef(null);
  if (loadingOlder && el.current && !before.current) {
    before.current = {
      height: el.current.scrollHeight,
      top: el.current.scrollTop
    };
  }
  useLayoutEffect(() => {
    const node = el.current;
    const prev = before.current;
    if (!node || !prev) return;
    node.scrollTop = prev.top + (node.scrollHeight - prev.height);
    before.current = null;
  }, [el, itemCount]);
}
const NEAR_BOTTOM_PX = 48;
function useJumpToLatest(el, itemCount, sessionId) {
  const [showJump, setShowJump] = useState(false);
  const awayRef = useRef(false);
  const countRef = useRef(itemCount);
  const sessionRef = useRef(sessionId);
  useEffect(() => {
    if (sessionRef.current !== sessionId) {
      sessionRef.current = sessionId;
      countRef.current = itemCount;
      awayRef.current = false;
      setShowJump(false);
      const node = el.current;
      if (node) node.scrollTop = node.scrollHeight;
      return;
    }
    if (itemCount > countRef.current) {
      const node = el.current;
      if (node && !awayRef.current) {
        const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
        if (distance <= NEAR_BOTTOM_PX) {
          node.scrollTop = node.scrollHeight;
        }
      } else if (awayRef.current) {
        setShowJump(true);
      }
    }
    countRef.current = itemCount;
  }, [el, itemCount, sessionId]);
  useEffect(() => {
    const node = el.current;
    if (!node) return;
    const onScroll = () => {
      const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
      const away = distance > node.clientHeight;
      awayRef.current = away;
      if (!away) setShowJump(false);
    };
    node.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
    return () => node.removeEventListener("scroll", onScroll);
  }, [el, sessionId]);
  return {
    showJump,
    jumpToLatest: () => {
      const node = el.current;
      if (!node) return;
      node.scrollTo({ top: node.scrollHeight, behavior: "smooth" });
      awayRef.current = false;
      setShowJump(false);
    }
  };
}
const LOAD_OLDER_TRIGGER_PX = 300;
function useLoadOlderTrigger(el, hasOlder, loadOlder, itemCount = 0) {
  const inFlight = useRef(false);
  const loadOlderRef = useRef(loadOlder);
  const progress = useRef(itemCount);
  const checkPosition = useRef(() => {
  });
  useLayoutEffect(() => {
    loadOlderRef.current = loadOlder;
    progress.current = itemCount;
  }, [loadOlder, itemCount]);
  useEffect(() => {
    const node = el.current;
    if (!node || !hasOlder) return;
    const onScroll = () => {
      if (inFlight.current) return;
      if (node.scrollTop > LOAD_OLDER_TRIGGER_PX) return;
      inFlight.current = true;
      const previous = progress.current;
      let succeeded = false;
      void loadOlderRef.current().then(() => {
        succeeded = true;
      }).catch(() => void 0).finally(() => {
        inFlight.current = false;
        setTimeout(() => {
          if (succeeded && progress.current !== previous) checkPosition.current();
        }, 0);
      });
    };
    checkPosition.current = onScroll;
    node.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
    return () => {
      checkPosition.current = () => {
      };
      node.removeEventListener("scroll", onScroll);
    };
  }, [el, hasOlder, itemCount]);
}
function useConversationHistoryScroll(options) {
  const scrollRef = useRef(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const { hasOlderItems, itemCount, loadOlder, sessionId } = options;
  const loadOlderRef = useRef(loadOlder);
  loadOlderRef.current = loadOlder;
  const runLoadOlder = useCallback(async () => {
    if (loadingOlder || !hasOlderItems) return;
    setLoadingOlder(true);
    try {
      await loadOlderRef.current();
    } finally {
      setLoadingOlder(false);
    }
  }, [hasOlderItems, loadingOlder]);
  useHistoryAnchor(scrollRef, itemCount, loadingOlder);
  useLoadOlderTrigger(scrollRef, hasOlderItems, runLoadOlder, options.historyItemCount ?? itemCount);
  const { showJump, jumpToLatest } = useJumpToLatest(
    scrollRef,
    itemCount,
    sessionId
  );
  return { loadingOlder, scrollRef, showJump, jumpToLatest };
}
function AgentConversationSurface({
  composer: hostComposer,
  contentRenderers,
  executionTrace,
  items,
  onError,
  presentation,
  mobile = false,
  runtime,
  renderModelControl,
  snapshot,
  toolRenderers,
  workspaceFiles,
  mentionHarness = null,
  sessionRecovery,
  codeReferences = [],
  onCodeReferencesSent,
  onRemoveCodeReference
}) {
  const text = useAgentWorkspaceText();
  const [sendingSessionId, setSendingSessionId] = useState(null);
  const onSendingChange = useCallback((sessionId, sending) => {
    setSendingSessionId((current) => sending ? sessionId : current === sessionId ? null : current);
  }, []);
  const progress = conversationProgress(snapshot, sendingSessionId === snapshot.sessionId);
  const composer = hostComposer === void 0 ? /* @__PURE__ */ jsx(
    ConversationComposer,
    {
      mentionHarness,
      onError,
      onSendingChange,
      presentation,
      mobile,
      runtime,
      renderModelControl,
      sessionRecovery,
      snapshot,
      workspaceFiles,
      codeReferences,
      onCodeReferencesSent,
      onRemoveCodeReference
    }
  ) : hostComposer;
  const executionTimeline = presentation === "developer" ? /* @__PURE__ */ jsx(ExecutionTrace, { interactions: snapshot.interactions, items: [] }) : null;
  const trace = executionTrace || executionTimeline;
  const isEmpty = !trace && items.length === 0 && snapshot.permissions.length === 0 && !snapshot.hasOlderItems;
  const { loadingOlder, scrollRef, showJump, jumpToLatest } = useConversationHistoryScroll({
    hasOlderItems: snapshot.hasOlderItems,
    itemCount: items.length,
    historyItemCount: snapshot.historyWindowSize,
    loadOlder: () => runtime.loadOlder(snapshot.sessionId).catch((cause) => {
      onError(cause);
      throw cause;
    }),
    sessionId: snapshot.sessionId
  });
  if (isEmpty) {
    return /* @__PURE__ */ jsx("main", { className: "h-full min-h-0 flex-1 overflow-y-auto", children: /* @__PURE__ */ jsxs("div", { className: "flex min-h-full flex-col justify-center gap-5 py-6", children: [
      /* @__PURE__ */ jsx(
        ConversationEmptyState,
        {
          agentLabel: snapshot.agentLabel,
          partner: snapshot.partner
        }
      ),
      /* @__PURE__ */ jsx(ConversationProgress, { progress }),
      composer
    ] }) });
  }
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-1 flex-col", children: [
    trace,
    /* @__PURE__ */ jsxs("div", { className: "relative min-h-0 flex-1", children: [
      /* @__PURE__ */ jsxs("main", { className: "h-full min-h-0 overflow-y-auto", ref: scrollRef, children: [
        loadingOlder && /* @__PURE__ */ jsx("div", { className: "flex justify-center px-3 pt-3 text-muted-foreground", children: /* @__PURE__ */ jsx(
          Loader2,
          {
            "aria-label": text.loadEarlierActivity,
            className: "size-3.5 animate-spin"
          }
        ) }),
        items.length > 0 && /* @__PURE__ */ jsx(MarkdownArtifactProvider, { runtime, snapshot, children: /* @__PURE__ */ jsx(
          ActivityTimeline,
          {
            contentRenderers,
            items,
            runtime,
            sessionId: snapshot.sessionId,
            toolRenderers
          }
        ) })
      ] }),
      /* @__PURE__ */ jsx(
        JumpToLatest,
        {
          label: text.jumpToLatest,
          onClick: jumpToLatest,
          visible: showJump
        }
      )
    ] }),
    /* @__PURE__ */ jsx(ConversationProgress, { progress }),
    /* @__PURE__ */ jsx(
      ApprovalDock,
      {
        disabled: !snapshot.capabilities.resolvePermission,
        onError,
        permissions: snapshot.permissions,
        runtime,
        sessionId: snapshot.sessionId
      }
    ),
    composer
  ] });
}
const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
function isUserVisibleArtifact(item) {
  if (!isVideoArtifact(item)) return false;
  if (item.status === "completed") return isVerifiedReadyVideoArtifact(item);
  if (item.manifest?.kind !== "video") return false;
  if (item.status === "failed") return item.manifest.stage === "failed";
  if (item.status === "queued") return item.manifest.stage === "queued";
  return item.manifest.stage === "rendering" || item.manifest.stage === "transcoding";
}
function isVerifiedReadyVideoArtifact(item) {
  if (item.status !== "completed" || item.manifest?.kind !== "video") {
    return false;
  }
  if (item.manifest.stage !== "ready" || !item.manifest.playableRepresentationId || !item.provenance?.publicationToolExecutionId) {
    return false;
  }
  const playableRepresentationId = item.manifest.playableRepresentationId;
  const playable = item.representations.find(
    (representation) => representation.representationId === playableRepresentationId
  );
  return Boolean(
    playable && playable.status === "ready" && playable.mediaType.startsWith("video/") && playable.byteSize !== void 0 && playable.byteSize > BigInt(0) && playable.digest && SHA256_DIGEST.test(playable.digest)
  );
}
function isVideoArtifact(item) {
  return item.manifest?.kind === "video" || artifactPresentation(item.mimeType, item.filename).kind === "video";
}
function resultScopeKey(scope) {
  return [
    scope.workerId ?? "",
    scope.sessionId,
    scope.turnId ?? "",
    scope.producerId ?? "",
    scope.workspaceKey ?? ""
  ].join("/");
}
function collectWorkbenchResults(artifacts, tools, toolRenderers, verifiedArtifactsOnly = false, workspaceArtifacts = [], scope) {
  const visibleArtifacts = artifacts.filter((item) => !verifiedArtifactsOnly || isUserVisibleArtifact(item));
  const existingArtifactIDs = new Set(
    visibleArtifacts.map((item) => item.artifactId)
  );
  const results = [
    ...visibleArtifacts,
    ...workspaceArtifacts.filter(
      (item) => !existingArtifactIDs.has(item.artifactId)
    )
  ].map((item) => ({
    id: `artifact:${resultScopeKey(scope ?? { sessionId: "unknown" })}:${item.id}`,
    item,
    kind: "artifact"
  }));
  for (const item of tools) {
    const Renderer = toolRenderers?.lookup(item.identity)?.workbench;
    if (Renderer) {
      results.push({
        id: `tool:${resultScopeKey(scope ?? { sessionId: "unknown" })}:${item.id}`,
        item,
        kind: "tool",
        Renderer
      });
    }
  }
  return results;
}
function WorkbenchResultsPane({
  compact = false,
  contentRenderers,
  onSelect,
  presentation,
  results,
  runtime,
  selected,
  sessionId
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col bg-background", children: [
    /* @__PURE__ */ jsxs("header", { className: "flex h-10 shrink-0 items-center border-b border-border px-3 text-xs font-medium", children: [
      text.results,
      /* @__PURE__ */ jsx("span", { className: "ml-2 text-muted-foreground", children: results.length })
    ] }),
    /* @__PURE__ */ jsxs("div", { className: `flex min-h-0 flex-1 ${compact ? "flex-col" : ""}`, children: [
      /* @__PURE__ */ jsx(
        "nav",
        {
          "aria-label": text.results,
          className: compact ? "flex h-14 shrink-0 gap-1 overflow-x-auto border-b border-border px-2 py-1.5" : "flex w-14 shrink-0 flex-col items-center gap-1 overflow-y-auto border-r border-border py-2",
          children: results.map((result) => /* @__PURE__ */ jsx(
            ResultTab,
            {
              active: result.id === selected?.id,
              compact,
              onClick: () => onSelect(result.id),
              result
            },
            result.id
          ))
        }
      ),
      /* @__PURE__ */ jsx("main", { className: "flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden p-3", children: selected?.kind === "artifact" ? /* @__PURE__ */ jsx("div", { className: "flex min-h-0 flex-1 flex-col", children: /* @__PURE__ */ jsx(
        ArtifactCard,
        {
          contentRenderers,
          fill: true,
          item: selected.item,
          presentation,
          runtime,
          sessionId
        }
      ) }) : selected?.kind === "tool" ? /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1 overflow-y-auto", children: /* @__PURE__ */ jsx(
        selected.Renderer,
        {
          item: selected.item,
          runtime,
          sessionId
        }
      ) }) : null })
    ] })
  ] });
}
function ResultTab({
  active,
  compact,
  onClick,
  result
}) {
  const label = result.kind === "artifact" ? result.item.filename : result.item.title;
  const Icon = result.kind === "tool" ? Wrench : artifactKindIcon(
    artifactPresentation(
      result.item.mimeType,
      result.item.filename
    ).kind
  );
  return /* @__PURE__ */ jsxs(
    "button",
    {
      "aria-label": label,
      "aria-pressed": active,
      className: `shrink-0 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring ${compact ? "flex h-11 max-w-44 items-center gap-2 px-3 text-xs" : "grid size-11 place-items-center"} ${active ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/60"}`,
      onClick,
      title: label,
      type: "button",
      children: [
        /* @__PURE__ */ jsx(Icon, { className: "size-4" }),
        compact && /* @__PURE__ */ jsx("span", { className: "truncate", children: label })
      ]
    }
  );
}
function focusAdjacentTab(event) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  const tabs = Array.from(
    event.currentTarget.querySelectorAll('[role="tab"]')
  ).filter((tab) => !tab.hasAttribute("disabled"));
  if (tabs.length === 0) return;
  const current = tabs.indexOf(document.activeElement);
  const next = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : (Math.max(current, 0) + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
  event.preventDefault();
  tabs[next]?.focus();
}
function AgentWorkbenchLayout({
  conversation,
  conversationLabel,
  forcePane,
  mode,
  pane,
  paneLabel
}) {
  const text = useAgentWorkspaceText();
  const showPane = forcePane;
  const [narrowPane, setNarrowPane] = useState(
    forcePane ? "pane" : "conversation"
  );
  const [paneActivated, setPaneActivated] = useState(forcePane);
  const tabId = useId();
  const conversationTabId = `${tabId}-conversation-tab`;
  const conversationPanelId = `${tabId}-conversation-panel`;
  const paneTabId = `${tabId}-pane-tab`;
  const panePanelId = `${tabId}-pane-panel`;
  useEffect(() => {
    if (!forcePane) {
      setNarrowPane("conversation");
      return;
    }
    setPaneActivated(true);
    setNarrowPane("pane");
  }, [forcePane]);
  const conversationHidden = mode === "narrow" && showPane && narrowPane !== "conversation";
  const paneHiddenWide = mode !== "narrow" && !showPane;
  return /* @__PURE__ */ jsxs(
    "div",
    {
      className: wideClass(mode, showPane),
      style: wideGrid(mode, showPane),
      children: [
        mode === "narrow" ? /* @__PURE__ */ jsx(
          "nav",
          {
            "aria-hidden": !showPane,
            "aria-label": text.workspaceViews,
            className: `flex h-12 shrink-0 items-center gap-1 border-b border-border px-2 ${showPane ? "" : "hidden"}`,
            hidden: !showPane,
            onKeyDown: showPane ? focusAdjacentTab : void 0,
            role: showPane ? "tablist" : void 0,
            children: showPane ? /* @__PURE__ */ jsxs(Fragment, { children: [
              /* @__PURE__ */ jsx(
                PaneTab,
                {
                  active: narrowPane === "conversation",
                  id: conversationTabId,
                  label: conversationLabel,
                  onClick: () => setNarrowPane("conversation"),
                  panelId: conversationPanelId
                }
              ),
              /* @__PURE__ */ jsx(
                PaneTab,
                {
                  active: narrowPane === "pane",
                  id: paneTabId,
                  label: paneLabel,
                  onClick: () => {
                    setPaneActivated(true);
                    setNarrowPane("pane");
                  },
                  panelId: panePanelId
                }
              )
            ] }) : null
          }
        ) : null,
        /* @__PURE__ */ jsx(
          "section",
          {
            "aria-hidden": conversationHidden,
            "aria-labelledby": mode === "narrow" && showPane ? conversationTabId : void 0,
            className: `flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden ${conversationHidden ? "hidden" : ""}`,
            id: conversationPanelId,
            role: mode === "narrow" && showPane ? "tabpanel" : void 0,
            children: conversation
          }
        ),
        mode !== "narrow" && pane != null ? /* @__PURE__ */ jsx(
          "aside",
          {
            "aria-hidden": paneHiddenWide,
            "aria-label": paneHiddenWide ? void 0 : paneLabel,
            className: paneHiddenWide ? "hidden" : "min-h-0 min-w-0 overflow-hidden border-l border-border",
            children: pane
          }
        ) : null,
        mode === "narrow" ? /* @__PURE__ */ jsx(
          "section",
          {
            "aria-hidden": !showPane || narrowPane !== "pane",
            "aria-labelledby": paneTabId,
            className: `min-h-0 flex-1 ${showPane && narrowPane === "pane" ? "" : "hidden"}`,
            id: panePanelId,
            role: showPane ? "tabpanel" : void 0,
            children: paneActivated ? pane : null
          }
        ) : null
      ]
    }
  );
}
function wideClass(mode, showPane) {
  if (mode === "narrow") return "flex min-h-0 flex-1 flex-col";
  return `min-h-0 flex-1 grid-rows-1 ${showPane ? "grid" : "flex"}`;
}
function wideGrid(mode, showPane) {
  if (mode === "narrow" || !showPane) return void 0;
  return {
    gridTemplateColumns: mode === "wide" ? "minmax(360px, 2fr) minmax(480px, 3fr)" : "minmax(0, 1fr) minmax(360px, 1fr)"
  };
}
function PaneTab({
  active,
  id,
  label,
  onClick,
  panelId
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-controls": panelId,
      "aria-selected": active,
      className: `h-11 rounded-md px-3 text-xs ${active ? "bg-muted font-medium" : "text-muted-foreground"}`,
      id,
      onClick,
      role: "tab",
      tabIndex: active ? 0 : -1,
      type: "button",
      children: label
    }
  );
}
function WorkbenchModePanes({
  code: code2,
  codeVisible,
  results
}) {
  return /* @__PURE__ */ jsxs("div", { className: "relative h-full min-h-0", children: [
    /* @__PURE__ */ jsx(
      "div",
      {
        "aria-hidden": !codeVisible,
        className: codeVisible ? "h-full min-h-0" : "hidden",
        children: code2
      }
    ),
    results ? /* @__PURE__ */ jsx(
      "div",
      {
        "aria-hidden": codeVisible,
        className: codeVisible ? "hidden" : "h-full min-h-0",
        children: results
      }
    ) : null
  ] });
}
function ResultWorkbench({
  artifacts,
  contentRenderers,
  conversation,
  forcePane = false,
  mode,
  pane,
  goalPane,
  paneLabel,
  presentation = "developer",
  runtime,
  sessionId,
  workspaceArtifacts = [],
  toolRenderers,
  tools = [],
  verifiedArtifactsOnly = false,
  workbenchMode = "work",
  resultScope
}) {
  const text = useAgentWorkspaceText();
  const [selectedId, setSelectedId] = useState(null);
  const workbenchResults = collectWorkbenchResults(
    artifacts,
    tools,
    toolRenderers,
    verifiedArtifactsOnly,
    workspaceArtifacts,
    resultScope
  );
  const selected = selectedResult(workbenchResults, selectedId);
  const resultsPane = workbenchResults.length === 0 ? null : /* @__PURE__ */ jsx(
    WorkbenchResultsPane,
    {
      compact: mode === "narrow",
      contentRenderers,
      onSelect: setSelectedId,
      presentation,
      results: workbenchResults,
      runtime,
      selected,
      sessionId
    }
  );
  const codeVisible = workbenchMode === "code";
  const goalVisible = workbenchMode === "goal" && goalPane != null;
  const columnVisible = codeVisible || goalVisible || forcePane || resultsPane != null;
  const layoutPane = goalVisible ? goalPane : pane != null ? /* @__PURE__ */ jsx(
    WorkbenchModePanes,
    {
      code: pane,
      codeVisible,
      results: resultsPane
    }
  ) : resultsPane;
  return /* @__PURE__ */ jsx(
    AgentWorkbenchLayout,
    {
      conversation,
      conversationLabel: text.conversation,
      forcePane: columnVisible,
      mode,
      pane: layoutPane,
      paneLabel: paneLabel ?? (codeVisible ? text.workbench.codePane : text.workbench.resultsPane)
    }
  );
}
function selectedResult(results, selectedId) {
  return results.find((result) => result.id === selectedId) ?? results.at(-1);
}
function PlanStrip({ steps }) {
  const text = useAgentWorkspaceText();
  if (steps.length === 0) return null;
  return /* @__PURE__ */ jsxs("section", { className: "shrink-0 border-b border-border px-3 py-2", "aria-label": text.agentPlan, children: [
    /* @__PURE__ */ jsx("div", { className: "mb-1.5 text-xs font-medium text-muted-foreground", children: text.plan }),
    /* @__PURE__ */ jsx("ol", { className: "flex flex-wrap gap-x-4 gap-y-1", children: steps.map((step2) => /* @__PURE__ */ jsxs("li", { className: "flex min-w-0 items-center gap-1.5 text-xs", children: [
      /* @__PURE__ */ jsx(PlanIcon, { status: step2.status }),
      /* @__PURE__ */ jsx("span", { className: step2.status === "completed" ? "text-muted-foreground" : "", children: step2.title })
    ] }, step2.id)) })
  ] });
}
function PlanIcon({ status }) {
  if (status === "completed") return /* @__PURE__ */ jsx(CheckCircle2, { className: "size-3.5 text-success" });
  if (status === "running") return /* @__PURE__ */ jsx(Loader2, { className: "size-3.5 animate-spin text-primary" });
  if (status === "failed") return /* @__PURE__ */ jsx(CircleAlert, { className: "size-3.5 text-danger" });
  return /* @__PURE__ */ jsx(Circle, { className: "size-3.5 text-muted-foreground" });
}
function userConversationItems(items) {
  return items.filter(userVisibleConversationItem);
}
function userVisibleConversationItem(item) {
  return item.kind === "message" && (item.role === "user" || item.role === "assistant") || item.kind === "attachment";
}
function userVisibleArtifacts(items) {
  return items.filter(isUserVisibleArtifact);
}
function userVideoTaskArtifacts(snapshot, artifacts) {
  return artifacts.filter(
    (artifact) => isVideoArtifact(artifact) && (!snapshot.latestUserCommandId || artifact.provenance?.commandId === snapshot.latestUserCommandId)
  );
}
function userVideoTaskState(snapshot, artifacts) {
  const allVideos = artifacts.filter(isVideoArtifact);
  const videos = userVideoTaskArtifacts(snapshot, artifacts);
  if (videos.length === 0) {
    if (snapshot.status === "completed" && snapshot.latestUserCommandId && allVideos.length > 0) {
      return "verification_failed";
    }
    if (snapshot.status !== "failed") return null;
    return userVideoFailureState(snapshot.error) ?? "task_failed";
  }
  const verified = videos.some(isVerifiedReadyVideoArtifact);
  if (snapshot.status === "failed" && verified) return "partial";
  if (snapshot.status === "failed") return "failed";
  if (verified) return "verified";
  if (videos.some(
    (artifact) => artifact.status === "completed" && !isVerifiedReadyVideoArtifact(artifact)
  )) {
    return "verification_failed";
  }
  if (videos.some((artifact) => artifact.status === "failed")) return "failed";
  if (snapshot.status === "completed") return "verification_failed";
  return "processing";
}
function userVideoFailureState(error) {
  const normalized = error?.toLowerCase() ?? "";
  if (!normalized) return null;
  if (normalized.includes("creative_no_account_available") || normalized.includes("no_account_available")) {
    return "provider_unavailable";
  }
  if (normalized.includes("invalid_api_key") || normalized.includes("invalid api key")) {
    return "provider_auth_failed";
  }
  if (normalized.includes("api_key_quota_exhausted")) {
    return "model_quota_exhausted";
  }
  return null;
}
function UserTaskStatus({
  artifacts,
  snapshot
}) {
  const state = userVideoTaskState(snapshot, artifacts);
  const text = useAgentWorkspaceText();
  if (!state) return null;
  const failed = state !== "processing" && state !== "verified";
  const Icon = state === "processing" ? LoaderCircle : state === "verified" ? CheckCircle2 : AlertTriangle;
  return /* @__PURE__ */ jsx(
    "section",
    {
      "aria-live": failed ? "assertive" : "polite",
      className: `shrink-0 border-b px-4 py-2.5 ${failed ? "border-danger/40 bg-danger-bg text-foreground" : state === "verified" ? "border-success/40 bg-success-bg text-foreground" : "border-border bg-muted/30"}`,
      role: failed ? "alert" : "status",
      children: /* @__PURE__ */ jsxs(
        "div",
        {
          className: `${CONVERSATION_CONTENT_WIDTH} flex items-start gap-2.5 sm:items-center`,
          children: [
            /* @__PURE__ */ jsx(
              Icon,
              {
                "aria-hidden": "true",
                className: `mt-0.5 size-4 shrink-0 ${state === "processing" ? "animate-spin motion-reduce:animate-none" : ""}`
              }
            ),
            /* @__PURE__ */ jsxs("div", { className: "sm:flex sm:items-baseline sm:gap-2", children: [
              /* @__PURE__ */ jsx("div", { className: "text-sm font-medium", children: text.videoTaskStatus[state].title }),
              /* @__PURE__ */ jsx("div", { className: "mt-0.5 text-xs opacity-80 sm:mt-0", children: text.videoTaskStatus[state].detail })
            ] })
          ]
        }
      )
    }
  );
}
function UserVideoExecutionTrace({
  steps
}) {
  const text = useAgentWorkspaceText().videoExecutionTrace;
  if (steps.length === 0) return null;
  return /* @__PURE__ */ jsx(
    "section",
    {
      "aria-label": text.label,
      "aria-live": "polite",
      className: "border-b border-border bg-muted/20 px-4 py-3",
      children: /* @__PURE__ */ jsxs("div", { className: CONVERSATION_CONTENT_WIDTH, children: [
        /* @__PURE__ */ jsx("div", { className: "mb-2 text-xs font-medium text-foreground", children: text.label }),
        /* @__PURE__ */ jsx("ol", { className: "grid gap-2 sm:grid-cols-4", children: steps.map((step2) => {
          const Icon = stepIcon(step2.status);
          return /* @__PURE__ */ jsxs("li", { className: "flex min-w-0 gap-2", children: [
            /* @__PURE__ */ jsx(
              Icon,
              {
                "aria-hidden": "true",
                className: `mt-0.5 size-4 shrink-0 ${step2.status === "running" ? "animate-spin motion-reduce:animate-none" : ""}`
              }
            ),
            /* @__PURE__ */ jsxs("div", { className: "min-w-0", children: [
              /* @__PURE__ */ jsx("div", { className: "text-xs font-medium leading-5", children: text.step[step2.id] }),
              /* @__PURE__ */ jsx("div", { className: "text-xs leading-5 text-muted-foreground", children: step2.detail === "rendering" ? text.rendering(step2.progress) : text.detail[step2.detail] })
            ] })
          ] }, step2.id);
        }) })
      ] })
    }
  );
}
function stepIcon(status) {
  if (status === "completed") return CheckCircle2;
  if (status === "running") return LoaderCircle;
  if (status === "failed") return AlertTriangle;
  return Circle;
}
function changesListState(changes) {
  if (changes.gitStatus === "unavailable") {
    return { kind: "unavailable" };
  }
  if (!changes.gitAvailable || changes.gitStatus === "no_repository") {
    return { kind: "unsupported" };
  }
  const baselineEmpty = changes.gitStatus === "no_head";
  if (changes.changes.length === 0) {
    return { kind: "empty", baselineEmpty };
  }
  return { kind: "list", changes: changes.changes, baselineEmpty };
}
function WorkspaceChangesList({
  changes,
  onOpenDiff,
  selectedPath
}) {
  const text = useAgentWorkspaceText().workbench;
  if (!changes) {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.changes });
  }
  const state = changesListState(changes);
  if (state.kind === "unsupported" || state.kind === "unavailable") {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.gitUnsupported });
  }
  if (state.kind === "empty") {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.selectFile });
  }
  return /* @__PURE__ */ jsxs("ul", { className: "min-h-0 flex-1 overflow-auto py-1", children: [
    state.baselineEmpty ? /* @__PURE__ */ jsx("li", { className: "px-3 pb-2 text-[11px] text-muted-foreground", children: text.emptyBaseline }) : null,
    state.changes.map((change) => /* @__PURE__ */ jsx(
      ChangeRow,
      {
        change,
        onOpen: () => onOpenDiff(change.newPath ?? change.path),
        selected: (change.newPath ?? change.path) === selectedPath
      },
      change.path
    ))
  ] });
}
function ChangeRow({
  change,
  onOpen,
  selected
}) {
  return /* @__PURE__ */ jsx("li", { children: /* @__PURE__ */ jsxs(
    "button",
    {
      className: `flex w-full items-center gap-2 truncate px-3 py-1.5 text-left text-xs ${selected ? "bg-muted font-medium" : "hover:bg-muted/60"}`,
      onClick: onOpen,
      type: "button",
      children: [
        /* @__PURE__ */ jsx("span", { className: "w-4 shrink-0 font-mono text-[10px] uppercase text-muted-foreground", children: change.status[0] }),
        /* @__PURE__ */ jsx("span", { className: "truncate", children: change.newPath ?? change.path })
      ]
    }
  ) });
}
function workspaceKeyForSession(source, sessionId) {
  return source.workspaceKeyForSession?.(sessionId) ?? source.workspaceKey;
}
function workspaceRootForSession(source, sessionId) {
  return source.workspaceRootForSession?.(sessionId) ?? source.workspaceRoot;
}
const workspaceDirectoryTimeoutMs = 12e3;
class WorkspaceDirectoryStore {
  constructor(source, sessionId) {
    this.source = source;
    this.sessionId = sessionId;
  }
  source;
  sessionId;
  listeners = /* @__PURE__ */ new Set();
  entries = /* @__PURE__ */ new Map();
  states = /* @__PURE__ */ new Map();
  snapshot = { entries: this.entries, states: this.states };
  requests = /* @__PURE__ */ new Map();
  disposed = false;
  getSnapshot() {
    return this.snapshot;
  }
  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  async load(dir) {
    if (this.disposed || this.entries.has(dir)) return;
    const pending = this.requests.get(dir);
    if (pending) return pending.promise;
    const abort = new AbortController();
    let timedOut = false;
    this.states.set(dir, "loading");
    this.notify();
    const timeout = setTimeout(() => {
      timedOut = true;
      abort.abort();
    }, workspaceDirectoryTimeoutMs);
    const promise = this.source.list(this.sessionId, dir, abort.signal).then(
      (entries) => {
        if (this.disposed || abort.signal.aborted && !timedOut) return;
        if (timedOut) {
          this.states.set(dir, "failed");
          this.notify();
          return;
        }
        this.entries.set(dir, entries);
        this.states.set(dir, "ready");
        this.notify();
      },
      () => {
        if (this.disposed || abort.signal.aborted && !timedOut) return;
        this.states.set(dir, "failed");
        this.notify();
      }
    ).finally(() => {
      clearTimeout(timeout);
      if (this.requests.get(dir)?.promise === promise) this.requests.delete(dir);
    });
    this.requests.set(dir, { abort, promise });
    return promise;
  }
  invalidate(dir) {
    this.requests.get(dir)?.abort.abort();
    this.requests.delete(dir);
    this.entries.delete(dir);
    this.states.set(dir, "idle");
    this.notify();
  }
  invalidateAll() {
    const dirs = /* @__PURE__ */ new Set([...this.entries.keys(), ...this.states.keys(), ...this.requests.keys()]);
    for (const dir of dirs) this.invalidate(dir);
  }
  dispose() {
    this.disposed = true;
    for (const request of this.requests.values()) request.abort.abort();
    this.requests.clear();
    this.listeners.clear();
  }
  notify() {
    this.snapshot = { entries: this.entries, states: this.states };
    for (const listener of this.listeners) listener();
  }
}
function WorkspaceFileTreeRow({ depth, entry, expanded, onOpen, selected, top }) {
  const Icon = entry.type === "directory" ? Folder : File;
  return /* @__PURE__ */ jsxs("button", { className: `absolute left-0 flex h-8 w-full items-center gap-1 truncate px-2 text-left text-xs ${selected ? "bg-muted font-medium" : "hover:bg-muted/60"}`, onClick: onOpen, style: { paddingLeft: 8 + depth * 12, transform: `translateY(${top}px)` }, type: "button", children: [
    entry.type === "directory" ? /* @__PURE__ */ jsx(ChevronRight, { className: `size-3 ${expanded ? "rotate-90" : ""}` }) : /* @__PURE__ */ jsx("span", { className: "w-3" }),
    /* @__PURE__ */ jsx(Icon, { className: "size-3.5 shrink-0" }),
    /* @__PURE__ */ jsx("span", { className: "truncate", children: entry.name })
  ] });
}
function toggleWorkspaceDirectory(path, expanded, setExpanded, loadDirectory) {
  const next = new Set(expanded);
  if (next.has(path)) {
    next.delete(path);
    setExpanded(next);
    return;
  }
  next.add(path);
  setExpanded(next);
  void loadDirectory(path);
}
function sortWorkspaceEntries(entries) {
  return [...entries].sort((left, right) => {
    if (left.type !== right.type) {
      return left.type === "directory" ? -1 : 1;
    }
    return left.name.localeCompare(right.name);
  });
}
function ancestorWorkspaceDirs(path) {
  const parts = path.split("/").filter(Boolean);
  const dirs = [];
  for (let index = 1; index < parts.length; index += 1) {
    dirs.push(parts.slice(0, index).join("/"));
  }
  return dirs;
}
function flattenExpandedTree(roots, expanded, childrenByDir) {
  const rows = [];
  walk(sortWorkspaceEntries(roots), 0, expanded, childrenByDir, rows);
  return rows;
}
function walk(entries, depth, expanded, childrenByDir, rows) {
  for (const entry of entries) {
    rows.push({ depth, entry });
    if (entry.type !== "directory" || !expanded.has(entry.path)) continue;
    walk(
      sortWorkspaceEntries(childrenByDir.get(entry.path) ?? []),
      depth + 1,
      expanded,
      childrenByDir,
      rows
    );
  }
}
function WorkspaceFileTree({ onOpenFile, revealPath, selectedPath, sessionId, source, directoryStore }) {
  const text = useAgentWorkspaceText().workbench;
  const ownedStore = useMemo(() => new WorkspaceDirectoryStore(source, sessionId), [sessionId, source]);
  const store = directoryStore ?? ownedStore;
  const subscribe = useMemo(() => store.subscribe.bind(store), [store]);
  const getSnapshot = useMemo(() => store.getSnapshot.bind(store), [store]);
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const [expanded, setExpanded] = useState(() => /* @__PURE__ */ new Set());
  const navigation = useWorkspaceFileNavigation();
  const parentRef = useRef(null);
  const workspaceRoot = workspaceRootForSession(source, sessionId);
  const roots = snapshot.entries.get("") ?? [];
  const rows = flattenExpandedTree(roots, expanded, snapshot.entries);
  const virtualizer = useVirtualizer({ count: rows.length, getScrollElement: () => parentRef.current, estimateSize: () => 32, overscan: 12 });
  const rootState = snapshot.states.get("") ?? "idle";
  useEffect(() => {
    navigation?.setWorkspaceRoot(workspaceRoot);
    return () => {
      if (!directoryStore) store.dispose();
    };
  }, [directoryStore, navigation, sessionId, source, store, workspaceRoot]);
  useEffect(() => {
    if (rootState === "idle") void store.load("");
  }, [rootState, store]);
  useEffect(() => {
    if (!revealPath) return;
    const abort = new AbortController();
    void (async () => {
      const next = new Set(expanded);
      for (const dir of ancestorWorkspaceDirs(revealPath)) {
        if (abort.signal.aborted) return;
        next.add(dir);
        await store.load(dir);
      }
      if (!abort.signal.aborted) setExpanded(next);
    })();
    return () => abort.abort();
  }, [expanded, revealPath, store]);
  useEffect(() => {
    for (const dir of expanded) {
      if (snapshot.states.get(dir) === "idle") void store.load(dir);
    }
  }, [expanded, snapshot.states, store]);
  if (rootState === "loading" || rootState === "idle") {
    return /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-2 px-3 py-6 text-xs text-muted-foreground", children: [
      /* @__PURE__ */ jsx(Loader2, { "aria-hidden": "true", className: "size-3.5 animate-spin" }),
      /* @__PURE__ */ jsx("span", { children: text.workspaceLoading })
    ] });
  }
  if (rootState === "failed") {
    return /* @__PURE__ */ jsxs(
      "button",
      {
        className: "flex w-full items-center gap-2 px-3 py-6 text-left text-xs text-muted-foreground hover:text-foreground",
        onClick: () => void store.load(""),
        type: "button",
        children: [
          /* @__PURE__ */ jsx(FolderTree, { "aria-hidden": "true", className: "size-3.5 shrink-0" }),
          /* @__PURE__ */ jsx("span", { children: text.workspaceLoadFailed })
        ]
      }
    );
  }
  if (roots.length === 0) return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.emptyWorkspace });
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col", children: [
    workspaceRoot ? /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 items-center gap-1.5 border-b border-border px-3 py-1.5 text-[11px] text-muted-foreground", title: workspaceRoot, children: [
      /* @__PURE__ */ jsx(FolderTree, { "aria-hidden": "true", className: "size-3.5 shrink-0" }),
      /* @__PURE__ */ jsxs("span", { className: "shrink-0", children: [
        text.workspaceRoot,
        ":"
      ] }),
      /* @__PURE__ */ jsx("span", { className: "truncate", children: workspaceRoot })
    ] }) : null,
    /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1 overflow-auto", ref: parentRef, children: /* @__PURE__ */ jsx("div", { className: "relative w-full", style: { height: virtualizer.getTotalSize() }, children: virtualizer.getVirtualItems().map((item) => {
      const row = rows[item.index];
      if (!row) return null;
      return /* @__PURE__ */ jsx(
        WorkspaceFileTreeRow,
        {
          depth: row.depth,
          entry: row.entry,
          expanded: expanded.has(row.entry.path),
          onOpen: () => {
            if (row.entry.type === "file") onOpenFile(row.entry.path);
            else toggleWorkspaceDirectory(row.entry.path, expanded, setExpanded, (dir) => store.load(dir));
          },
          selected: row.entry.path === selectedPath,
          top: item.start
        },
        row.entry.path
      );
    }) }) })
  ] });
}
function normalizeWorkspaceSearchQuery(value) {
  return value.trim().slice(0, 256);
}
const workspaceSearchTimeoutMs = 1e4;
function WorkspaceFileSearch({
  children,
  onOpenFile,
  sessionId,
  source
}) {
  const text = useAgentWorkspaceText().workbench;
  const [query, setQuery] = useState("");
  const [result, setResult] = useState(null);
  const [state, setState] = useState("idle");
  const normalized = normalizeWorkspaceSearchQuery(query);
  useEffect(() => {
    if (!normalized || !source.search) {
      setResult(null);
      setState("idle");
      return;
    }
    setResult(null);
    setState("idle");
    const controller = new AbortController();
    let timedOut = false;
    const timer = window.setTimeout(() => {
      setState("loading");
      void source.search(sessionId, normalized, controller.signal).then(
        (next) => {
          if (controller.signal.aborted) return;
          window.clearTimeout(timeout);
          setResult(next);
          setState("ready");
        },
        () => {
          if (controller.signal.aborted) return;
          window.clearTimeout(timeout);
          setResult(null);
          setState(timedOut ? "timeout" : "failed");
        }
      );
    }, 250);
    const timeout = window.setTimeout(() => {
      timedOut = true;
      controller.abort();
      setResult(null);
      setState("timeout");
    }, 250 + workspaceSearchTimeoutMs);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [normalized, sessionId, source]);
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col", children: [
    /* @__PURE__ */ jsxs("label", { className: "flex h-9 shrink-0 items-center gap-1.5 border-b border-border px-2 text-muted-foreground", children: [
      /* @__PURE__ */ jsx(Search, { className: "size-3.5" }),
      /* @__PURE__ */ jsx(
        "input",
        {
          "aria-label": text.searchFiles,
          className: "min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground",
          onChange: (event) => setQuery(event.target.value),
          placeholder: text.searchFiles,
          value: query
        }
      ),
      state === "loading" ? /* @__PURE__ */ jsx(Loader2, { "aria-label": text.searching, className: "size-3.5 animate-spin" }) : null
    ] }),
    normalized ? /* @__PURE__ */ jsx(
      SearchResults,
      {
        state,
        onOpenFile,
        result,
        text
      }
    ) : children
  ] });
}
function SearchResults({
  onOpenFile,
  result,
  state,
  text
}) {
  if (state === "timeout") {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.searchTimedOut });
  }
  if (state === "failed") {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.refreshFailed });
  }
  if (!result) return null;
  const incomplete = result.truncated || Boolean(result.incompleteReason);
  if (result.entries.length === 0 && !incomplete) {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.noSearchResults });
  }
  return /* @__PURE__ */ jsxs("div", { className: "min-h-0 flex-1 overflow-y-auto", children: [
    result.entries.map((entry) => /* @__PURE__ */ jsx(SearchResultRow, { entry, onOpenFile }, `${entry.type}:${entry.path}`)),
    incomplete ? /* @__PURE__ */ jsx("p", { className: "border-t border-border px-3 py-2 text-[11px] text-muted-foreground", children: text.searchIncomplete }) : null
  ] });
}
function SearchResultRow({
  entry,
  onOpenFile
}) {
  return /* @__PURE__ */ jsxs(
    "button",
    {
      className: "flex w-full items-start gap-2 px-3 py-2 text-left text-xs hover:bg-muted/60",
      onClick: () => entry.type === "file" && onOpenFile(entry.path),
      type: "button",
      children: [
        /* @__PURE__ */ jsx(File, { className: "mt-0.5 size-3.5 shrink-0" }),
        /* @__PURE__ */ jsxs("span", { className: "min-w-0", children: [
          /* @__PURE__ */ jsx("span", { className: "block truncate", children: entry.name }),
          /* @__PURE__ */ jsx("span", { className: "block truncate text-[11px] text-muted-foreground", children: entry.path })
        ] })
      ]
    }
  );
}
function WorkspaceGitPanel({
  sessionId,
  source
}) {
  const text = useAgentWorkspaceText().workbench;
  const [summary, setSummary] = useState(null);
  const [log, setLog] = useState(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [selectedRef, setSelectedRef] = useState(null);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    void source.summary(sessionId, controller.signal).then(
      (next) => {
        if (controller.signal.aborted) return;
        setSummary(next);
        setSelectedRef(next.branch);
        setLoading(false);
      },
      () => {
        if (!controller.signal.aborted) {
          setFailed(true);
          setLoading(false);
        }
      }
    );
    return () => controller.abort();
  }, [sessionId, source]);
  useEffect(() => {
    if (!selectedRef) {
      setLog(null);
      return;
    }
    const controller = new AbortController();
    void source.log(sessionId, selectedRef, controller.signal).then(
      (next) => {
        if (!controller.signal.aborted) setLog(next);
      },
      () => {
        if (!controller.signal.aborted) setLog(null);
      }
    );
    return () => controller.abort();
  }, [selectedRef, sessionId, source]);
  if (loading) {
    return /* @__PURE__ */ jsx(Loader2, { "aria-label": text.searching, className: "m-4 size-4 animate-spin" });
  }
  if (failed || !summary) {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.gitUnavailable });
  }
  if (summary.status === "no_repository") {
    return /* @__PURE__ */ jsx("p", { className: "px-3 py-6 text-xs text-muted-foreground", children: text.noGitRepository });
  }
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col overflow-y-auto", children: [
    /* @__PURE__ */ jsx(GitSummary, { summary, text }),
    /* @__PURE__ */ jsx("div", { className: "border-t border-border px-3 py-2 text-[11px] font-medium text-muted-foreground", children: text.history }),
    summary.refs.length > 0 ? /* @__PURE__ */ jsx("div", { className: "flex gap-1 overflow-x-auto px-2 pb-2", children: summary.refs.map((ref) => /* @__PURE__ */ jsx(
      "button",
      {
        className: `shrink-0 rounded px-2 py-1 text-[11px] ${selectedRef === ref.name ? "bg-muted font-medium" : "text-muted-foreground"}`,
        onClick: () => setSelectedRef(ref.name),
        type: "button",
        children: ref.name
      },
      `${ref.kind}:${ref.name}`
    )) }) : null,
    log?.commits.length ? /* @__PURE__ */ jsx("div", { className: "flex flex-col", children: log.commits.map((commit) => /* @__PURE__ */ jsxs("div", { className: "border-t border-border/60 px-3 py-2", children: [
      /* @__PURE__ */ jsxs("div", { className: "flex items-start gap-2 text-xs", children: [
        /* @__PURE__ */ jsx(GitCommitHorizontal, { className: "mt-0.5 size-3.5 shrink-0" }),
        /* @__PURE__ */ jsx("span", { className: "min-w-0 truncate font-medium", children: commit.subject })
      ] }),
      /* @__PURE__ */ jsxs("div", { className: "mt-1 pl-5 text-[11px] text-muted-foreground", children: [
        commit.oid.slice(0, 8),
        " · ",
        commit.author
      ] })
    ] }, commit.oid)) }) : /* @__PURE__ */ jsx("p", { className: "px-3 py-4 text-xs text-muted-foreground", children: text.noCommits })
  ] });
}
function GitSummary({
  summary,
  text
}) {
  return /* @__PURE__ */ jsxs("div", { className: "space-y-1 px-3 py-3 text-xs", children: [
    /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-2 font-medium", children: [
      /* @__PURE__ */ jsx(GitBranch, { className: "size-3.5" }),
      summary.branch ?? text.detachedHead
    ] }),
    /* @__PURE__ */ jsxs("div", { className: "pl-5 text-[11px] text-muted-foreground", children: [
      summary.head?.slice(0, 10) ?? text.noCommits,
      " · ",
      summary.clean ? text.cleanWorkspace : text.dirtyWorkspace
    ] })
  ] });
}
function CodeWorkbenchSidebar({
  changes,
  collapsed,
  nav,
  onNav,
  onOpenDiff,
  onOpenFile,
  revealPath,
  selectedPath,
  sessionId,
  source,
  directoryStore
}) {
  const text = useAgentWorkspaceText().workbench;
  if (collapsed) return null;
  return /* @__PURE__ */ jsxs("aside", { className: "flex w-56 shrink-0 flex-col border-r border-border", children: [
    /* @__PURE__ */ jsxs("div", { className: "flex h-10 items-center gap-1 border-b border-border px-2", children: [
      /* @__PURE__ */ jsx(
        NavButton,
        {
          active: nav === "files",
          label: text.files,
          onClick: () => onNav("files")
        }
      ),
      source.git ? /* @__PURE__ */ jsx(
        NavButton,
        {
          active: nav === "git",
          label: text.git,
          onClick: () => onNav("git")
        }
      ) : null,
      /* @__PURE__ */ jsx(
        NavButton,
        {
          active: nav === "changes",
          label: text.changes,
          onClick: () => onNav("changes")
        }
      )
    ] }),
    /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1", children: nav === "files" ? source.capabilities.search && source.search ? /* @__PURE__ */ jsx(
      WorkspaceFileSearch,
      {
        onOpenFile,
        sessionId,
        source,
        children: /* @__PURE__ */ jsx(
          WorkspaceFileTree,
          {
            onOpenFile,
            revealPath,
            selectedPath,
            sessionId,
            source,
            directoryStore
          }
        )
      }
    ) : /* @__PURE__ */ jsx(
      WorkspaceFileTree,
      {
        onOpenFile,
        revealPath,
        selectedPath,
        sessionId,
        source,
        directoryStore
      }
    ) : nav === "changes" ? /* @__PURE__ */ jsx(
      WorkspaceChangesList,
      {
        changes,
        onOpenDiff,
        selectedPath
      }
    ) : source.git ? /* @__PURE__ */ jsx(WorkspaceGitPanel, { sessionId, source: source.git }) : null })
  ] });
}
function NavButton({
  active,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      className: `h-7 rounded px-2 text-xs ${active ? "bg-muted font-medium" : "text-muted-foreground"}`,
      onClick,
      type: "button",
      children: label
    }
  );
}
function WorkspaceFileTabs({
  activePath,
  onClose,
  onSelect,
  stalePaths,
  tabs
}) {
  if (tabs.length === 0) return null;
  return /* @__PURE__ */ jsx("div", { className: "flex min-w-0 flex-1 items-center gap-1 overflow-x-auto", children: tabs.map((tab) => /* @__PURE__ */ jsxs(
    "div",
    {
      className: `flex h-7 shrink-0 items-center rounded-md border border-border ${tab.path === activePath ? "bg-muted" : ""}`,
      children: [
        /* @__PURE__ */ jsxs(
          "button",
          {
            className: "max-w-40 truncate px-2 text-xs",
            onClick: () => onSelect(tab.path),
            type: "button",
            children: [
              fileName(tab.path),
              stalePaths.has(tab.path) ? " •" : "",
              tab.view === "diff" ? " Δ" : ""
            ]
          }
        ),
        /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": tab.path,
            className: "px-1 text-xs text-muted-foreground",
            onClick: () => onClose(tab.path),
            type: "button",
            children: "×"
          }
        )
      ]
    },
    tab.path
  )) });
}
function fileName(path) {
  return path.split("/").pop() || path;
}
function CodeWorkbenchToolbar({
  activePath,
  canCopy,
  collapsed,
  onClose,
  onCopy,
  onFind,
  onRefresh,
  onReference,
  onSelect,
  onToggleFiles,
  stalePaths,
  tabs,
  workspaceLabel
}) {
  const text = useAgentWorkspaceText().workbench;
  return /* @__PURE__ */ jsxs("div", { className: "flex h-10 shrink-0 items-center gap-1 border-b border-border px-2", children: [
    /* @__PURE__ */ jsx(
      IconButton,
      {
        label: text.files,
        onClick: onToggleFiles,
        pressed: !collapsed,
        children: /* @__PURE__ */ jsx(PanelLeft, { className: "size-3.5" })
      }
    ),
    /* @__PURE__ */ jsx(
      WorkspaceFileTabs,
      {
        activePath,
        onClose,
        onSelect,
        stalePaths,
        tabs
      }
    ),
    /* @__PURE__ */ jsx("span", { className: "hidden min-w-0 truncate text-[11px] text-muted-foreground sm:inline", children: activePath ?? workspaceLabel }),
    /* @__PURE__ */ jsx(IconButton, { label: text.findInFile, onClick: onFind, children: /* @__PURE__ */ jsx(Search, { className: "size-3.5" }) }),
    /* @__PURE__ */ jsx(IconButton, { label: text.referenceCode, onClick: onReference, children: /* @__PURE__ */ jsx("span", { className: "text-[10px] font-medium", children: "@" }) }),
    /* @__PURE__ */ jsx(IconButton, { disabled: !canCopy, label: text.copy, onClick: onCopy, children: /* @__PURE__ */ jsx(Copy, { className: "size-3.5" }) }),
    /* @__PURE__ */ jsx(IconButton, { label: text.refresh, onClick: onRefresh, children: /* @__PURE__ */ jsx(RefreshCw, { className: "size-3.5" }) }),
    /* @__PURE__ */ jsx(
      IconButton,
      {
        disabled: !activePath,
        label: text.closeTab,
        onClick: () => activePath && onClose(activePath),
        children: /* @__PURE__ */ jsx(X, { className: "size-3.5" })
      }
    )
  ] });
}
function IconButton({
  children,
  disabled,
  label,
  onClick,
  pressed
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": label,
      "aria-pressed": pressed,
      className: "flex size-7 items-center justify-center rounded-md hover:bg-muted disabled:opacity-40",
      disabled,
      onClick,
      type: "button",
      children
    }
  );
}
function codeWorkspaceFailure(error) {
  if (error && typeof error === "object" && "code" in error) {
    const code2 = error.code;
    if (code2 === "codeWorkspaceAccess") return "access";
    if (code2 === "not_found" || code2 === "workspace_missing") return "not_found";
    if (code2 === "refresh") return "refresh";
  }
  return "refresh";
}
const MAX_CACHED_FILES = 20;
const MAX_CACHED_BYTES = 20 * 1024 * 1024;
function workspaceCacheKey(tenantId, userId, sessionId, workspaceKey, path) {
  return `${tenantId}\0${userId}\0${sessionId}\0${workspaceKey}\0${path}`;
}
function decodedFileBytes(file) {
  if (file.encoding === "base64") {
    return Math.floor(file.content.length * 3 / 4);
  }
  return new TextEncoder().encode(file.content).length;
}
class WorkspaceDocumentCache {
  files = /* @__PURE__ */ new Map();
  order = [];
  get(key) {
    return this.files.get(key);
  }
  set(key, file, retainKey) {
    this.files.set(key, file);
    this.touch(key);
    this.evict(retainKey);
  }
  delete(key) {
    this.files.delete(key);
    const index = this.order.indexOf(key);
    if (index >= 0) this.order.splice(index, 1);
  }
  clearScope(prefix) {
    for (const key of [...this.files.keys()]) {
      if (key.startsWith(prefix)) this.delete(key);
    }
  }
  size() {
    return this.files.size;
  }
  touch(key) {
    const index = this.order.indexOf(key);
    if (index >= 0) this.order.splice(index, 1);
    this.order.push(key);
  }
  evict(retainKey) {
    while ((this.files.size > MAX_CACHED_FILES || this.totalBytes() > MAX_CACHED_BYTES) && this.order.length > 0) {
      const victim = this.order.find((key) => key !== retainKey) ?? (retainKey && this.order.length === 1 ? void 0 : this.order[0]);
      if (!victim) break;
      this.delete(victim);
    }
  }
  totalBytes() {
    let total = 0;
    for (const file of this.files.values()) {
      total += decodedFileBytes(file);
    }
    return total;
  }
}
class WorkspaceDocumentStore {
  constructor(source, scope, sessionId, cache) {
    this.source = source;
    this.scope = scope;
    this.sessionId = sessionId;
    this.cache = cache ?? new WorkspaceDocumentCache();
  }
  source;
  scope;
  sessionId;
  cache;
  tabs = [];
  active = null;
  generations = /* @__PURE__ */ new Map();
  inflight = /* @__PURE__ */ new Map();
  get workspaceKey() {
    return workspaceKeyForSession(this.source, this.sessionId);
  }
  open(path, view = "source") {
    const tab = this.tabFor(path, view);
    const existing = this.tabs.findIndex((item) => item.path === path);
    if (existing >= 0) this.tabs[existing] = tab;
    else this.tabs.push(tab);
    this.active = tab;
  }
  activate(path) {
    const tab = this.tabs.find((item) => item.path === path);
    if (tab) this.active = tab;
  }
  close(path) {
    const index = this.tabs.findIndex((item) => item.path === path);
    if (index < 0) return;
    const wasActive = this.active?.path === path;
    this.tabs.splice(index, 1);
    if (!wasActive) return;
    this.active = this.tabs[index] ?? this.tabs[index - 1] ?? null;
  }
  invalidate(path) {
    this.cache.delete(this.fileKey(path));
    this.bump(path);
  }
  invalidateDir(dir) {
    const prefix = dir ? `${dir}/` : "";
    for (const tab of this.tabs) {
      if (!prefix || tab.path === dir || tab.path.startsWith(prefix)) {
        this.invalidate(tab.path);
      }
    }
  }
  clearScope() {
    this.cache.clearScope(
      `${this.scope.tenantId}\0${this.scope.userId}\0${this.sessionId}\0${this.workspaceKey}\0`
    );
    this.tabs = [];
    this.active = null;
    this.generations.clear();
    this.inflight.clear();
  }
  async load(path, signal) {
    const key = this.fileKey(path);
    const pending = this.inflight.get(key);
    if (pending) {
      const generation2 = this.generations.get(path);
      const file = await pending;
      if (this.generations.get(path) !== generation2) {
        return this.cache.get(key) ?? null;
      }
      return this.cache.get(key) ?? file;
    }
    const generation = this.bump(path);
    const request = this.source.read(this.sessionId, path, signal);
    this.inflight.set(key, request);
    try {
      const file = await request;
      if (this.generations.get(path) !== generation) return null;
      this.cache.set(
        key,
        file,
        this.active ? this.fileKey(this.active.path) : key
      );
      return file;
    } finally {
      if (this.inflight.get(key) === request) this.inflight.delete(key);
    }
  }
  tabFor(path, view) {
    return { workspaceKey: this.workspaceKey, path, view };
  }
  fileKey(path) {
    return workspaceCacheKey(
      this.scope.tenantId,
      this.scope.userId,
      this.sessionId,
      this.workspaceKey,
      path
    );
  }
  bump(path) {
    const next = (this.generations.get(path) ?? 0) + 1;
    this.generations.set(path, next);
    return next;
  }
}
const localScope = {
  tenantId: "local",
  userId: "local"
};
function useCodeWorkbenchDocuments(input) {
  const scope = input.preferenceScope ?? localScope;
  const store = useMemo(
    () => new WorkspaceDocumentStore(input.source, scope, input.sessionId),
    [input.sessionId, input.source, scope.tenantId, scope.userId]
  );
  const [, setEpoch] = useState(0);
  const bump = () => setEpoch((value) => value + 1);
  const [file, setFile] = useState(null);
  const [diff, setDiff] = useState(null);
  const [deleted, setDeleted] = useState(false);
  const [stalePaths, setStalePaths] = useState(() => /* @__PURE__ */ new Set());
  const [failure, setFailure] = useState(null);
  const generation = useRef(0);
  const activeAbort = useRef(null);
  useEffect(() => {
    return () => {
      activeAbort.current?.abort();
      store.clearScope();
    };
  }, [store]);
  const open = (path, view = "source") => {
    store.open(path, view);
    bump();
    void loadActive(path, view);
  };
  const activate = (path) => {
    store.activate(path);
    bump();
    const tab = store.active;
    if (tab) void loadActive(tab.path, tab.view);
  };
  const close = (path) => {
    store.close(path);
    bump();
    const tab = store.active;
    if (!tab) {
      setFile(null);
      setDiff(null);
      setDeleted(false);
      setFailure(null);
      return;
    }
    void loadActive(tab.path, tab.view);
  };
  const refresh = async () => {
    const tab = store.active;
    if (!tab) return;
    store.invalidate(tab.path);
    await loadActive(tab.path, tab.view);
  };
  const markStale = (path) => {
    if (store.active?.path === path) {
      void loadActive(path, store.active.view);
      return;
    }
    setStalePaths((current) => new Set(current).add(path));
  };
  async function loadActive(path, view) {
    const token = ++generation.current;
    activeAbort.current?.abort();
    const abort = new AbortController();
    activeAbort.current = abort;
    setDeleted(false);
    setFailure(null);
    setStalePaths((current) => {
      if (!current.has(path)) return current;
      const next = new Set(current);
      next.delete(path);
      return next;
    });
    try {
      if (view === "diff") {
        const next2 = await input.source.diff(input.sessionId, path, abort.signal);
        if (token !== generation.current) return;
        setDiff(next2);
        setFile(null);
        return;
      }
      const next = await store.load(path, abort.signal);
      if (token !== generation.current) return;
      setFile(next);
      setDiff(null);
    } catch (error) {
      if (token !== generation.current) return;
      const kind = codeWorkspaceFailure(error);
      if (kind === "access") {
        store.clearScope();
        bump();
        setDeleted(false);
        setFile(null);
        setDiff(null);
        setFailure("access");
        return;
      }
      if (kind === "not_found") {
        setDeleted(true);
        setFile(null);
        setDiff(null);
        return;
      }
      setFailure("refresh");
    } finally {
      if (activeAbort.current === abort) activeAbort.current = null;
    }
  }
  return {
    activate,
    close,
    deleted,
    diff,
    failure,
    file,
    markStale,
    open,
    refresh,
    stalePaths,
    store
  };
}
function nextRefresh(input) {
  const seen = new Map(input.seen);
  const invalidatePaths = [];
  let refreshChangesAndCurrent = false;
  for (const tool of input.tools) {
    const previous = seen.get(tool.id);
    seen.set(tool.id, tool.status);
    if (previous === tool.status) continue;
    if (tool.status !== "completed" && tool.status !== "failed") continue;
    if (tool.path) invalidatePaths.push(tool.path);
    if (!tool.path || tool.semanticKey === "shell.execute") {
      refreshChangesAndCurrent = true;
    }
  }
  return { invalidatePaths, refreshChangesAndCurrent, seen };
}
function parentWorkspaceDir(path) {
  const index = path.lastIndexOf("/");
  return index <= 0 ? "" : path.slice(0, index);
}
function useCodeWorkbenchExecutionRefresh(input) {
  const seenRef = useRef(/* @__PURE__ */ new Map());
  const inflight = useRef(false);
  const refreshRef = useRef(input.onRefreshChanges);
  refreshRef.current = input.onRefreshChanges;
  const invalidateFilesRef = useRef(input.onInvalidateFiles);
  invalidateFilesRef.current = input.onInvalidateFiles;
  useEffect(() => {
    const result = nextRefresh({
      seen: seenRef.current,
      tools: input.tools.map((tool) => ({
        id: tool.id,
        status: tool.status,
        semanticKey: tool.identity.semanticKey,
        path: toolWorkspacePath(
          tool,
          workspaceRootForSession(input.source, input.sessionId)
        )
      }))
    });
    seenRef.current = result.seen;
    for (const path of result.invalidatePaths) {
      input.store.invalidate(path);
      input.store.invalidateDir(parentWorkspaceDir(path));
    }
    if (result.invalidatePaths.length > 0 || result.refreshChangesAndCurrent) {
      invalidateFilesRef.current();
      refreshRef.current();
    }
  }, [input.sessionId, input.source, input.store, input.tools]);
  useEffect(() => {
    const active = input.visible && typeof document !== "undefined" && document.visibilityState === "visible" && (input.status === "running" || input.status === "waiting");
    if (!active) return;
    const tick = async () => {
      if (inflight.current) return;
      inflight.current = true;
      try {
        await refreshRef.current();
      } finally {
        inflight.current = false;
      }
    };
    const timer = window.setInterval(() => {
      void tick();
    }, 2e3);
    const onFocus = () => {
      void tick();
    };
    window.addEventListener("focus", onFocus);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", onFocus);
    };
  }, [input.connection, input.status, input.visible]);
  const previousConnection = useRef(input.connection);
  useEffect(() => {
    if (previousConnection.current !== "connected" && input.connection === "connected") {
      invalidateFilesRef.current();
      refreshRef.current();
    }
    previousConnection.current = input.connection;
  }, [input.connection]);
}
function WorkspaceCodeEmptyState({
  kind
}) {
  const text = useAgentWorkspaceText().workbench;
  const message = kind === "workspace" ? text.emptyWorkspace : kind === "unavailable" ? text.unavailable : kind === "deleted" ? text.deletedFile : kind === "access" ? text.accessDenied : text.selectFile;
  return /* @__PURE__ */ jsx("div", { className: "flex h-full min-h-0 items-center justify-center p-6 text-sm text-muted-foreground", children: message });
}
const maxHighlightBytes = 1 << 20;
const extensionLanguage = {
  bash: "shell",
  cjs: "javascript",
  css: "css",
  go: "go",
  htm: "html",
  html: "html",
  js: "javascript",
  json: "json",
  jsx: "jsx",
  markdown: "markdown",
  md: "markdown",
  mjs: "javascript",
  py: "python",
  sh: "shell",
  ts: "typescript",
  tsx: "tsx",
  yaml: "yaml",
  yml: "yaml",
  zsh: "shell"
};
const mimeLanguage = {
  "application/javascript": "javascript",
  "application/json": "json",
  "application/typescript": "typescript",
  "application/x-sh": "shell",
  "application/x-shellscript": "shell",
  "application/x-yaml": "yaml",
  "application/yaml": "yaml",
  "text/css": "css",
  "text/html": "html",
  "text/javascript": "javascript",
  "text/json": "json",
  "text/markdown": "markdown",
  "text/x-go": "go",
  "text/x-markdown": "markdown",
  "text/x-python": "python",
  "text/x-sh": "shell",
  "text/x-shellscript": "shell",
  "text/x-yaml": "yaml",
  "text/yaml": "yaml"
};
function languageForPath(path, mime) {
  const name = path.split("/").pop() ?? path;
  const dot = name.lastIndexOf(".");
  if (dot > 0) {
    const mapped = extensionLanguage[name.slice(dot + 1).toLowerCase()];
    if (mapped) return mapped;
  }
  if (!mime) return "plaintext";
  return mimeLanguage[mime.split(";")[0]?.trim().toLowerCase() ?? ""] ?? "plaintext";
}
function shouldHighlightContent(content, encoding) {
  if (encoding !== "utf-8") return false;
  return new TextEncoder().encode(content).length <= maxHighlightBytes;
}
function WorkspaceCodePreview({
  file,
  onDownload,
  path,
  previewRef,
  revealLine,
  showToolbar = true,
  onReference
}) {
  const text = useAgentWorkspaceText().workbench;
  const hostRef = useRef(null);
  const editorRef = useRef(null);
  const [ready, setReady] = useState(false);
  const binary = isBinaryFile(file);
  const highlight = file != null && shouldHighlightContent(file.content, file.encoding);
  useImperativeHandle(previewRef, () => ({
    openFind: () => editorRef.current?.openFind(),
    revealLine: (line) => editorRef.current?.revealLine(line),
    reference: () => {
      if (!file || !onReference) return;
      const selection = editorRef.current?.selection();
      if (!selection) return;
      const range = selectionLineRange(file.content, selection.from, selection.to);
      if (!range) return;
      onReference({
        contentDigest: digestText(file.content),
        endLine: range.endLine,
        path,
        source: "workspace",
        startLine: range.startLine,
        text: selection.text
      });
    }
  }), [file, onReference, path]);
  useEffect(() => {
    if (binary || !file || !hostRef.current) return;
    const parent = hostRef.current;
    let cancelled = false;
    void import("./workspaceCodeMirrorRuntime-DIOsKQAZ.js").then(async ({ mountReadonlyEditor }) => {
      if (cancelled || !parent.isConnected) return;
      const editor = await mountReadonlyEditor({
        parent,
        doc: file.content,
        language: languageForPath(path, file.contentType),
        highlight
      });
      if (cancelled) {
        editor.destroy();
        return;
      }
      editorRef.current = editor;
      setReady(true);
      if (revealLine != null) editor.revealLine(revealLine);
    });
    return () => {
      cancelled = true;
      editorRef.current?.destroy();
      editorRef.current = null;
      setReady(false);
    };
  }, [binary, file?.path, path]);
  useEffect(() => {
    if (!file || binary) return;
    editorRef.current?.setDoc(file.content);
  }, [binary, file, file?.content]);
  useEffect(() => {
    if (!file || binary) return;
    void editorRef.current?.setLanguage(
      languageForPath(path, file.contentType),
      highlight
    );
  }, [binary, file, highlight, path]);
  useEffect(() => {
    if (revealLine == null) return;
    editorRef.current?.revealLine(revealLine);
  }, [revealLine]);
  if (!file) return null;
  if (binary) {
    return /* @__PURE__ */ jsxs("div", { className: "flex h-full flex-col items-center justify-center gap-3 p-6 text-sm text-muted-foreground", children: [
      /* @__PURE__ */ jsx("p", { children: text.binaryFile }),
      onDownload ? /* @__PURE__ */ jsx(
        "button",
        {
          className: "rounded-md border border-border px-3 py-1.5 text-foreground",
          onClick: () => onDownload(path),
          type: "button",
          children: text.downloadFile
        }
      ) : null
    ] });
  }
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col", children: [
    file.truncated || !highlight ? /* @__PURE__ */ jsx("p", { className: "shrink-0 border-b border-border px-3 py-1.5 text-xs text-muted-foreground", children: file.truncated ? text.truncated : text.tooLargeToHighlight }) : null,
    showToolbar ? /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 items-center gap-1 border-b border-border px-2 py-1", children: [
      /* @__PURE__ */ jsx(
        "button",
        {
          className: "h-7 rounded px-2 text-xs",
          disabled: !ready,
          onClick: () => editorRef.current?.openFind(),
          type: "button",
          children: text.findInFile
        }
      ),
      onReference ? /* @__PURE__ */ jsx(
        "button",
        {
          className: "h-7 rounded px-2 text-xs",
          disabled: !ready,
          onClick: () => {
            const selection = editorRef.current?.selection();
            if (!selection) return;
            const range = selectionLineRange(file.content, selection.from, selection.to);
            if (!range) return;
            onReference({
              contentDigest: digestText(file.content),
              endLine: range.endLine,
              path,
              source: "workspace",
              startLine: range.startLine,
              text: selection.text
            });
          },
          type: "button",
          children: text.referenceCode
        }
      ) : null,
      onDownload ? /* @__PURE__ */ jsx(
        "button",
        {
          className: "h-7 rounded px-2 text-xs",
          onClick: () => onDownload(path),
          type: "button",
          children: text.downloadFile
        }
      ) : null
    ] }) : null,
    /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1", ref: hostRef })
  ] });
}
function isBinaryFile(file) {
  return file != null && (file.binary || file.encoding === "base64");
}
function digestText(value) {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return `fnv1a:${(hash >>> 0).toString(16).padStart(8, "0")}`;
}
function canTextDiff(diff) {
  if (!diff.before.present && !diff.after.present) return false;
  if (!isTextSide(diff.before) || !isTextSide(diff.after)) return false;
  return utf8Bytes(diff.before.content) <= maxHighlightBytes && utf8Bytes(diff.after.content) <= maxHighlightBytes && !diff.before.truncated && !diff.after.truncated;
}
function isTextSide(side) {
  if (!side.present) return true;
  return side.encoding === "utf-8";
}
function utf8Bytes(content) {
  return new TextEncoder().encode(content).length;
}
function diffFallbackFile(diff) {
  const side = diff.after.present ? diff.after : diff.before;
  if (!side.present) return null;
  return {
    path: diff.path,
    content: side.content,
    encoding: side.encoding,
    contentType: side.encoding === "base64" ? "application/octet-stream" : "text/plain",
    bytes: utf8Bytes(side.content),
    truncated: side.truncated,
    binary: side.encoding === "base64"
  };
}
function WorkspaceDiffPreview({
  diff,
  paneWidth
}) {
  const text = useAgentWorkspaceText().workbench;
  const hostRef = useRef(null);
  const unified = paneWidth < 720;
  if (!canTextDiff(diff)) {
    const file = diffFallbackFile(diff);
    return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col", children: [
      /* @__PURE__ */ jsx("p", { className: "shrink-0 border-b border-border px-3 py-1.5 text-xs text-muted-foreground", children: text.gitUnsupported }),
      file ? /* @__PURE__ */ jsx(WorkspaceCodePreview, { file, path: diff.path, showToolbar: false }) : /* @__PURE__ */ jsx("p", { className: "p-6 text-sm text-muted-foreground", children: text.deletedFile })
    ] });
  }
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0 flex-col", children: [
    diff.stale ? /* @__PURE__ */ jsx("p", { className: "shrink-0 border-b border-border px-3 py-1.5 text-xs text-muted-foreground", children: text.refresh }) : null,
    /* @__PURE__ */ jsx(MergeHost, { diff, hostRef, unified })
  ] });
}
function MergeHost({
  diff,
  hostRef,
  unified
}) {
  const before = diff.before.present ? diff.before.content : "";
  const after = diff.after.present ? diff.after.content : "";
  const highlight = shouldHighlightContent(before, "utf-8") && shouldHighlightContent(after, "utf-8");
  useEffect(() => {
    const parent = hostRef.current;
    if (!parent) return;
    let cancelled = false;
    let destroy;
    void import("./workspaceMergeRuntime-fU_NX1w5.js").then(async ({ mountMergeDiff }) => {
      if (cancelled || !parent.isConnected) return;
      const mounted = await mountMergeDiff({
        parent,
        before,
        after,
        unified,
        language: languageForPath(diff.path),
        highlight
      });
      if (cancelled) {
        mounted.destroy();
        return;
      }
      destroy = mounted.destroy;
    });
    return () => {
      cancelled = true;
      destroy?.();
    };
  }, [after, before, diff.path, highlight, hostRef, unified]);
  return /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1 [&_.cm-mergeView]:h-full", ref: hostRef });
}
function CodeWorkbenchSession({
  connection,
  containerMode,
  onDownload,
  preferenceScope,
  sessionId,
  source,
  status,
  tools,
  visible,
  onReference
}) {
  const text = useAgentWorkspaceText().workbench;
  const docs = useCodeWorkbenchDocuments({ preferenceScope, sessionId, source });
  const directoryStore = useMemo(() => new WorkspaceDirectoryStore(source, sessionId), [sessionId, source]);
  const [nav, setNav] = useState("files");
  const [userCollapsed, setUserCollapsed] = useState(null);
  const [changes, setChanges] = useState(null);
  const [paneWidth, setPaneWidth] = useState(960);
  const previewRef = useRef(null);
  const paneRef = useRef(null);
  const autoOpened = useRef(false);
  const collapsed = userCollapsed ?? containerMode !== "wide";
  const active = docs.store.active;
  const openRef = useRef(docs.open);
  openRef.current = docs.open;
  const [revealLine, setRevealLine] = useState();
  const [revealPath, setRevealPath] = useState(null);
  const navigation = useWorkspaceFileNavigation();
  useEffect(() => () => directoryStore.dispose(), [directoryStore]);
  const reloadChanges = async () => {
    await source.changes(sessionId, new AbortController().signal).then(setChanges, () => void 0);
  };
  useCodeWorkbenchExecutionRefresh({
    connection,
    onInvalidateFiles: () => directoryStore.invalidateAll(),
    onRefreshChanges: async () => {
      await Promise.all([docs.refresh(), reloadChanges()]);
    },
    sessionId,
    source,
    status,
    store: docs.store,
    tools,
    visible
  });
  useEffect(() => {
    const node = paneRef.current;
    if (!node) return;
    const measure = () => setPaneWidth(node.getBoundingClientRect().width);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const abort = new AbortController();
    void source.changes(sessionId, abort.signal).then((next) => {
      setChanges(next);
      if (autoOpened.current || next.changes.length === 0) return;
      if (!next.gitAvailable || next.gitStatus === "no_repository") return;
      autoOpened.current = true;
      const first = next.changes[0];
      if (first) openRef.current(first.newPath ?? first.path, "diff");
    });
    return () => abort.abort();
  }, [sessionId, source]);
  useEffect(() => {
    if (!navigation) return;
    navigation.setHandler((target) => {
      openRef.current(target.path, "source");
      setRevealLine(target.line);
      setRevealPath(target.path);
      setNav("files");
      setUserCollapsed(false);
    });
    return () => navigation.setHandler(null);
  }, [navigation]);
  const notice = docs.failure === "access" ? text.accessDenied : docs.failure === "refresh" ? text.refreshFailed : connection !== "connected" && docs.file ? text.cachedContent : null;
  return /* @__PURE__ */ jsxs("div", { className: "flex h-full min-h-0", "data-code-workbench": sessionId, ref: paneRef, children: [
    /* @__PURE__ */ jsx(
      CodeWorkbenchSidebar,
      {
        changes,
        collapsed,
        nav,
        onNav: setNav,
        onOpenDiff: (path) => docs.open(path, "diff"),
        onOpenFile: (path) => docs.open(path, "source"),
        revealPath,
        selectedPath: active?.path ?? null,
        sessionId,
        source,
        directoryStore
      }
    ),
    /* @__PURE__ */ jsxs("div", { className: "flex min-h-0 min-w-0 flex-1 flex-col", children: [
      /* @__PURE__ */ jsx(
        CodeWorkbenchToolbar,
        {
          activePath: active?.path ?? null,
          canCopy: Boolean(docs.file && !docs.file.binary),
          collapsed,
          onClose: docs.close,
          onCopy: () => {
            if (docs.file && !docs.file.binary) {
              void navigator.clipboard.writeText(docs.file.content);
            }
          },
          onFind: () => previewRef.current?.openFind(),
          onRefresh: () => {
            directoryStore.invalidateAll();
            void Promise.all([docs.refresh(), reloadChanges()]);
          },
          onReference: () => previewRef.current?.reference(),
          onSelect: docs.activate,
          onToggleFiles: () => setUserCollapsed(!collapsed),
          stalePaths: docs.stalePaths,
          tabs: docs.store.tabs,
          workspaceLabel: text.currentWorkspace
        }
      ),
      notice ? /* @__PURE__ */ jsx("p", { className: "shrink-0 border-b border-border px-3 py-1.5 text-xs text-muted-foreground", children: notice }) : null,
      docs.deleted || docs.failure === "access" ? /* @__PURE__ */ jsx(
        WorkspaceCodeEmptyState,
        {
          kind: docs.failure === "access" ? "access" : "deleted"
        }
      ) : active?.view === "diff" && docs.diff ? /* @__PURE__ */ jsx(WorkspaceDiffPreview, { diff: docs.diff, paneWidth }) : active && docs.file ? /* @__PURE__ */ jsx(
        WorkspaceCodePreview,
        {
          file: docs.file,
          onDownload,
          path: active.path,
          previewRef,
          revealLine,
          showToolbar: false,
          onReference
        }
      ) : /* @__PURE__ */ jsx(WorkspaceCodeEmptyState, { kind: "select" })
    ] })
  ] });
}
function CodeWorkbench({
  connection = "connected",
  containerMode,
  onDownload,
  preferenceScope,
  sessionId,
  source,
  status = "idle",
  tools = [],
  unavailable = false,
  visible = true,
  onReference
}) {
  if (unavailable || !source) {
    return /* @__PURE__ */ jsx(WorkspaceCodeEmptyState, { kind: "unavailable" });
  }
  return /* @__PURE__ */ jsx(
    CodeWorkbenchSession,
    {
      connection,
      containerMode,
      onDownload,
      preferenceScope,
      sessionId,
      source,
      status,
      tools,
      visible,
      onReference
    }
  );
}
function GoalPanel({ goal }) {
  const text = useAgentWorkspaceText().workbench;
  return /* @__PURE__ */ jsxs("section", { className: "flex h-full min-h-0 flex-col overflow-y-auto bg-background", "data-agent-goal": goal.goalId, children: [
    /* @__PURE__ */ jsxs("header", { className: "shrink-0 border-b border-border px-4 py-3", children: [
      /* @__PURE__ */ jsxs("div", { className: "flex items-center justify-between gap-3", children: [
        /* @__PURE__ */ jsx("h2", { className: "truncate text-sm font-semibold", children: goal.title }),
        /* @__PURE__ */ jsxs("span", { className: "shrink-0 text-xs text-muted-foreground", children: [
          Math.round(goal.progress),
          "%"
        ] })
      ] }),
      goal.description ? /* @__PURE__ */ jsx("p", { className: "mt-1 text-xs text-muted-foreground", children: goal.description }) : null,
      /* @__PURE__ */ jsx("div", { className: "mt-3 h-1.5 overflow-hidden rounded-full bg-muted", children: /* @__PURE__ */ jsx("div", { className: "h-full rounded-full bg-primary transition-[width]", style: { width: `${clamp(goal.progress)}%` } }) }),
      /* @__PURE__ */ jsx(GoalActions, { goal, text })
    ] }),
    /* @__PURE__ */ jsxs("div", { className: "min-h-0 flex-1 space-y-4 p-4", children: [
      /* @__PURE__ */ jsxs("section", { children: [
        /* @__PURE__ */ jsx("h3", { className: "mb-2 text-xs font-medium text-muted-foreground", children: text.goalCriteria }),
        /* @__PURE__ */ jsx("div", { className: "space-y-1.5", children: goal.criteria.map((criterion) => /* @__PURE__ */ jsx(CriterionRow, { title: criterion.title, status: criterion.status }, criterion.id)) })
      ] }),
      goal.evidence.length > 0 ? /* @__PURE__ */ jsxs("section", { children: [
        /* @__PURE__ */ jsx("h3", { className: "mb-2 text-xs font-medium text-muted-foreground", children: text.goalEvidence }),
        /* @__PURE__ */ jsx("div", { className: "space-y-1", children: goal.evidence.map((item) => item.href ? /* @__PURE__ */ jsx("a", { className: "block truncate text-xs text-primary hover:underline", href: item.href, children: item.label }, item.id) : /* @__PURE__ */ jsx("span", { className: "block truncate text-xs", children: item.label }, item.id)) })
      ] }) : null
    ] })
  ] });
}
function GoalActions({ goal, text }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const action = goal.status === "draft" ? goal.actions?.start : goal.status === "running" ? goal.actions?.verify : goal.actions?.cancel;
  const label = goal.status === "draft" ? text.goalStart : goal.status === "running" ? text.goalVerify : text.goalCancel;
  if (!action) return null;
  const runAction = action;
  async function run() {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await runAction();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : text.goalActionFailed);
    } finally {
      setBusy(false);
    }
  }
  return /* @__PURE__ */ jsxs("div", { className: "mt-3 space-y-1.5", children: [
    /* @__PURE__ */ jsx("button", { className: "rounded-md border border-border px-2.5 py-1.5 text-xs font-medium hover:bg-muted disabled:cursor-wait disabled:opacity-60", disabled: busy, onClick: () => void run(), type: "button", children: busy ? text.goalActionRunning : label }),
    error ? /* @__PURE__ */ jsx("p", { className: "text-xs text-danger", role: "alert", children: error }) : null
  ] });
}
function CriterionRow({ title, status }) {
  const Icon = status === "passed" ? Check : status === "failed" ? CircleAlert : status === "running" ? Loader2 : Circle;
  return /* @__PURE__ */ jsxs("div", { className: "flex items-start gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted/50", children: [
    /* @__PURE__ */ jsx(Icon, { className: `mt-0.5 size-3.5 shrink-0 ${status === "running" ? "animate-spin" : status === "passed" ? "text-success" : status === "failed" ? "text-danger" : "text-muted-foreground"}` }),
    /* @__PURE__ */ jsx("span", { children: title })
  ] });
}
function clamp(value) {
  return Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0));
}
function goalWithInteractionEvidence(goal, sessionId, interactions) {
  if (!goal || !goal.activeSessionIds.includes(sessionId)) return goal;
  const existing = new Set(goal.evidence.map((item) => item.id));
  const evidence = interactions.filter((item) => item.status === "succeeded" || item.status === "failed" || item.status === "rejected").filter((item) => !existing.has(`${goal.goalId}:${item.id}`)).map((item) => ({
    id: `${goal.goalId}:${item.id}`,
    kind: item.kind === "artifact_action" ? "change" : "message",
    label: `${item.kind} ${item.sourceId ?? item.commandId}: ${item.status}${item.error ? ` — ${item.error}` : ""}`
  }));
  return evidence.length ? { ...goal, evidence: [...goal.evidence, ...evidence] } : goal;
}
function AgentWorkspaceConversationPanel({
  presentation,
  mobile = false,
  snapshot,
  plan,
  allArtifacts,
  conversationItems,
  artifacts,
  videoExecutionSteps,
  contentRenderers,
  toolRenderers,
  runtime,
  composer,
  renderModelControl,
  sessionId,
  mode,
  workspaceArtifacts,
  workspaceFiles,
  mentionHarness,
  onError,
  sessionRecovery,
  workbenchMode = "work",
  goal,
  workbenchUnavailable = false,
  codeWorkspace,
  preferenceScope
}) {
  const text = useAgentWorkspaceText();
  const [codeReferences, setCodeReferences] = useState([]);
  const addCodeReference = useCallback((reference) => {
    setCodeReferences(
      (current) => current.some(
        (item) => item.path === reference.path && item.startLine === reference.startLine && item.endLine === reference.endLine && item.contentDigest === reference.contentDigest && item.source === reference.source
      ) ? current : [...current, reference]
    );
  }, []);
  const codeMode = workbenchMode === "code";
  const projectedGoal = goalWithInteractionEvidence(goal, sessionId, snapshot.interactions);
  const [codeMounted, setCodeMounted] = useState(codeMode);
  useEffect(() => {
    if (codeMode) setCodeMounted(true);
  }, [codeMode]);
  const codePane = codeMounted && (codeWorkspace || workbenchUnavailable) ? /* @__PURE__ */ jsx(
    CodeWorkbench,
    {
      connection: snapshot.connection,
      containerMode: mode,
      preferenceScope,
      sessionId,
      source: codeWorkspace,
      status: snapshot.status,
      tools: presentation === "developer" ? conversationItems.filter((item) => item.kind === "tool") : [],
      unavailable: workbenchUnavailable,
      visible: codeMode,
      onReference: addCodeReference
    }
  ) : void 0;
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    presentation === "developer" && /* @__PURE__ */ jsx(PlanStrip, { steps: plan }),
    presentation === "user" && /* @__PURE__ */ jsx(UserTaskStatus, { artifacts: allArtifacts, snapshot }),
    /* @__PURE__ */ jsx(
      ResultWorkbench,
      {
        artifacts,
        contentRenderers,
        conversation: /* @__PURE__ */ jsx(
          AgentConversationSurface,
          {
            composer,
            contentRenderers,
            executionTrace: videoExecutionSteps.length > 0 ? /* @__PURE__ */ jsx(UserVideoExecutionTrace, { steps: videoExecutionSteps }) : void 0,
            items: conversationItems,
            mentionHarness,
            onError: (cause) => onError(cause instanceof Error ? cause.message : String(cause)),
            presentation,
            mobile,
            runtime,
            renderModelControl,
            sessionRecovery,
            snapshot,
            toolRenderers,
            workspaceFiles,
            codeReferences,
            onCodeReferencesSent: () => setCodeReferences([]),
            onRemoveCodeReference: (index) => setCodeReferences((current) => current.filter((_, itemIndex) => itemIndex !== index))
          }
        ),
        forcePane: codeMode,
        mode,
        pane: codePane,
        goalPane: projectedGoal ? /* @__PURE__ */ jsx(GoalPanel, { goal: projectedGoal }) : null,
        paneLabel: codeMode ? text.workbench.codePane : void 0,
        presentation,
        runtime,
        sessionId,
        toolRenderers,
        tools: presentation === "developer" ? conversationItems.filter((item) => item.kind === "tool") : [],
        verifiedArtifactsOnly: presentation === "user",
        workbenchMode,
        resultScope: { sessionId },
        workspaceArtifacts
      }
    )
  ] });
}
function WorkspaceFullscreenButton({
  active,
  supported,
  onToggle
}) {
  const text = useAgentWorkspaceText();
  if (!supported) return null;
  const label = active ? text.exitFullscreen : text.enterFullscreen;
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-label": label,
      "aria-pressed": active,
      className: "flex size-9 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring",
      onClick: onToggle,
      title: label,
      type: "button",
      children: active ? /* @__PURE__ */ jsx(Minimize, { className: "size-4" }) : /* @__PURE__ */ jsx(Maximize, { className: "size-4" })
    }
  );
}
function WorkbenchModeToggle({
  goalAvailable = false,
  mode,
  onChange
}) {
  const text = useAgentWorkspaceText().workbench;
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-label": [text.codeMode, text.workMode, goalAvailable ? text.goalMode : null].filter(Boolean).join(" / "),
      className: "flex rounded-md border border-border p-0.5 text-xs",
      role: "group",
      children: [
        /* @__PURE__ */ jsx(
          ModeButton,
          {
            active: mode === "code",
            label: text.codeMode,
            onClick: () => onChange("code")
          }
        ),
        goalAvailable ? /* @__PURE__ */ jsx(ModeButton, { active: mode === "goal", label: text.goalMode, onClick: () => onChange("goal") }) : null,
        /* @__PURE__ */ jsx(
          ModeButton,
          {
            active: mode === "work",
            label: text.workMode,
            onClick: () => onChange("work")
          }
        )
      ]
    }
  );
}
function ModeButton({
  active,
  label,
  onClick
}) {
  return /* @__PURE__ */ jsx(
    "button",
    {
      "aria-pressed": active,
      className: `h-7 rounded px-2 ${active ? "bg-muted font-medium" : "text-muted-foreground"}`,
      onClick,
      type: "button",
      children: label
    }
  );
}
function AgentWorkspaceHeaderActions({
  fullscreen,
  goalAvailable,
  headerActions,
  onWorkbenchModeChange,
  showFullscreen,
  showToggle,
  workbenchMode
}) {
  return /* @__PURE__ */ jsxs(Fragment, { children: [
    showToggle ? /* @__PURE__ */ jsx(
      WorkbenchModeToggle,
      {
        mode: workbenchMode,
        goalAvailable,
        onChange: onWorkbenchModeChange
      }
    ) : null,
    headerActions,
    showFullscreen ? /* @__PURE__ */ jsx(
      WorkspaceFullscreenButton,
      {
        active: fullscreen.active,
        onToggle: fullscreen.toggle,
        supported: fullscreen.supported
      }
    ) : null
  ] });
}
function PartnerIdentityMark({
  logoUrl,
  size
}) {
  const box = size === "lg" ? "size-12 rounded-2xl" : "size-7 rounded-full";
  const icon = size === "lg" ? "size-6" : "size-3.5";
  if (logoUrl) {
    return /* @__PURE__ */ jsx(
      "img",
      {
        alt: "",
        className: `${box} shrink-0 bg-muted object-contain p-1`,
        src: logoUrl
      }
    );
  }
  return /* @__PURE__ */ jsx(
    "div",
    {
      "aria-hidden": "true",
      className: `flex ${box} shrink-0 items-center justify-center bg-primary/10 text-primary`,
      children: /* @__PURE__ */ jsx(Bot, { className: icon })
    }
  );
}
function PartnerCapabilityStrip({
  partner
}) {
  const text = useAgentWorkspaceText();
  const branding = resolveWorkerBranding(
    partner?.branding,
    useAgentTenantBranding()
  );
  const name = branding?.title || partner?.name.trim() || "";
  if (!partner) return null;
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-label": text.partnerIdentity,
      className: "flex h-8 max-w-40 items-center gap-2",
      children: [
        /* @__PURE__ */ jsx(PartnerIdentityMark, { logoUrl: branding?.logoUrl, size: "sm" }),
        name ? /* @__PURE__ */ jsx("span", { className: "hidden truncate text-[13px] font-medium sm:block", children: name }) : null
      ]
    }
  );
}
function WorkspaceHeaderMark({ snapshot }) {
  const branding = resolveWorkerBranding(
    snapshot.partner?.branding,
    useAgentTenantBranding()
  );
  const accentColor = /^#[0-9a-f]{6}$/i.test(snapshot.partner?.branding?.accentColor ?? "") ? snapshot.partner?.branding?.accentColor : void 0;
  const title = branding?.title || snapshot.partner?.name.trim() || snapshot.title;
  if (branding?.logoUrl) {
    return /* @__PURE__ */ jsx(
      "img",
      {
        alt: title,
        className: "size-9 shrink-0 rounded-xl object-contain",
        style: branding.accentColor ? { outline: `2px solid ${branding.accentColor}` } : void 0,
        src: branding.logoUrl
      }
    );
  }
  return /* @__PURE__ */ jsx("div", { className: "flex size-9 shrink-0 items-center justify-center rounded-xl bg-muted", children: /* @__PURE__ */ jsx(Bot, { className: "size-4", style: accentColor ? { color: accentColor } : void 0 }) });
}
const SessionDeckHeaderContext = createContext({
  controls: null,
  inDeck: false
});
function useSessionDeckHeader() {
  return useContext(SessionDeckHeaderContext);
}
function WorkspaceHeader({
  actions,
  identity,
  presentation,
  snapshot,
  tabs
}) {
  const connected = snapshot.connection === "connected";
  const deckHeader = useSessionDeckHeader();
  const text = useAgentWorkspaceText();
  const model = currentModelLabel(snapshot);
  const branding = resolveWorkerBranding(
    snapshot.partner?.branding,
    useAgentTenantBranding()
  );
  const title = branding?.title || snapshot.partner?.name.trim() || snapshot.title;
  const subtitleLabel = branding?.subtitle || (snapshot.partner?.name.trim() && snapshot.partner.name.trim() === snapshot.agentLabel ? null : snapshot.agentLabel);
  const controls = /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 items-center gap-1.5", children: [
    deckHeader.inDeck ? /* @__PURE__ */ jsxs("div", { className: "flex min-w-0 shrink-0 items-center gap-2", children: [
      /* @__PURE__ */ jsx(WorkspaceHeaderMark, { snapshot }),
      /* @__PURE__ */ jsx("span", { className: "truncate text-sm font-medium", children: subtitleLabel || text.agent })
    ] }) : null,
    tabs ? /* @__PURE__ */ jsx("div", { className: "flex min-w-0 items-center gap-1 overflow-x-auto", children: tabs }) : null,
    /* @__PURE__ */ jsxs("div", { className: "flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground", children: [
      snapshot.status === "failed" ? /* @__PURE__ */ jsx(CircleAlert, { className: "size-3.5 text-danger" }) : connected ? /* @__PURE__ */ jsx(Wifi, { className: "size-3.5 text-success" }) : /* @__PURE__ */ jsx(WifiOff, { className: "size-3.5" }),
      /* @__PURE__ */ jsx("span", { className: "hidden max-w-24 truncate sm:inline sm:max-w-32", children: text.sessionStatus(snapshot.status, snapshot.connection) })
    ] }),
    actions
  ] });
  if (deckHeader.inDeck) {
    return deckHeader.controls ? createPortal(controls, deckHeader.controls) : null;
  }
  return /* @__PURE__ */ jsxs("header", { className: "flex h-14 shrink-0 items-center gap-2 border-b border-border px-3 sm:gap-3 sm:px-4", children: [
    /* @__PURE__ */ jsx(WorkspaceHeaderMark, { snapshot }),
    /* @__PURE__ */ jsxs("div", { className: "min-w-0 flex-1 basis-40", children: [
      /* @__PURE__ */ jsx("div", { className: "truncate text-sm font-medium", children: title }),
      /* @__PURE__ */ jsxs("div", { className: "hidden min-w-0 items-center gap-1.5 overflow-hidden text-xs text-muted-foreground sm:flex", children: [
        subtitleLabel && /* @__PURE__ */ jsx("span", { className: "shrink-0", children: subtitleLabel }),
        presentation === "user" && model && /* @__PURE__ */ jsxs("span", { className: "contents", children: [
          subtitleLabel && /* @__PURE__ */ jsx("span", { "aria-hidden": "true", children: "·" }),
          /* @__PURE__ */ jsx("span", { className: "truncate", title: model, children: model })
        ] }),
        presentation === "developer" && (snapshot.metadata ?? []).map((item) => /* @__PURE__ */ jsxs("span", { className: "contents", children: [
          /* @__PURE__ */ jsx("span", { "aria-hidden": "true", children: "·" }),
          /* @__PURE__ */ jsx("span", { className: "truncate", title: `${item.label}: ${item.value}`, children: item.value })
        ] }, item.id))
      ] })
    ] }),
    identity,
    controls
  ] });
}
function currentModelLabel(snapshot) {
  const control = (snapshot.configuration ?? []).find(
    (item) => item.id === "model"
  );
  const value = control?.value?.trim();
  if (!value) return null;
  const option = control?.options.find((item) => item.value === value);
  return option?.label?.trim() || value;
}
function WorkspaceViewTab({
  active,
  icon,
  id,
  label,
  onClick,
  panelId
}) {
  return /* @__PURE__ */ jsxs(
    "button",
    {
      "aria-controls": panelId,
      "aria-label": label,
      "aria-selected": active,
      className: `flex h-9 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring ${active ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/60"}`,
      id,
      onClick,
      role: "tab",
      tabIndex: active ? 0 : -1,
      title: label,
      type: "button",
      children: [
        icon,
        /* @__PURE__ */ jsx("span", { className: "hidden sm:inline", children: label })
      ]
    }
  );
}
function WorkspaceViewTabs({
  conversationPanelId,
  conversationTabId,
  onViewChange,
  terminalEnabled,
  terminalPanelId,
  terminalTabId,
  view
}) {
  const text = useAgentWorkspaceText();
  return /* @__PURE__ */ jsxs(
    "nav",
    {
      "aria-label": text.workspaceViews,
      className: "flex shrink-0 items-center gap-1",
      onKeyDown: focusAdjacentTab,
      role: "tablist",
      children: [
        /* @__PURE__ */ jsx(
          WorkspaceViewTab,
          {
            active: view === "conversation",
            icon: /* @__PURE__ */ jsx(MessageSquare, { className: "size-3.5" }),
            id: conversationTabId,
            label: text.conversation,
            onClick: () => onViewChange("conversation"),
            panelId: conversationPanelId
          }
        ),
        terminalEnabled && /* @__PURE__ */ jsx(
          WorkspaceViewTab,
          {
            active: view === "terminal",
            icon: /* @__PURE__ */ jsx(Terminal, { className: "size-3.5" }),
            id: terminalTabId,
            label: text.terminal,
            onClick: () => onViewChange("terminal"),
            panelId: terminalPanelId
          }
        )
      ]
    }
  );
}
function AgentWorkspaceViewHeader({ actions, presentation, snapshot, tabs }) {
  return /* @__PURE__ */ jsx(
    WorkspaceHeader,
    {
      actions: /* @__PURE__ */ jsx(AgentWorkspaceHeaderActions, { ...actions }),
      identity: /* @__PURE__ */ jsx(PartnerCapabilityStrip, { partner: snapshot.partner }),
      presentation,
      snapshot,
      tabs: /* @__PURE__ */ jsx(WorkspaceViewTabs, { ...tabs })
    }
  );
}
function userVideoExecutionSteps(snapshot, artifacts) {
  const state = userVideoTaskState(snapshot, artifacts);
  const video2 = latestVideo(userVideoTaskArtifacts(snapshot, artifacts), state);
  if (!state) return [];
  if (!video2 || video2.manifest?.kind !== "video") {
    return snapshot.status === "failed" && snapshot.latestUserCommandId ? failedBeforeArtifact(state) : [];
  }
  const steps = baseSteps();
  const phase = video2.manifest.stage;
  const active = sessionActive(snapshot.status);
  if (phase === "failed") return failedGeneration(steps);
  if (phase === "queued") {
    return setStep(
      steps,
      "generation",
      active ? "pending" : "failed",
      active ? "queued" : "generation_failed"
    );
  }
  if (phase === "rendering") {
    return setStep(
      steps,
      "generation",
      active ? "running" : "failed",
      active ? "rendering" : "generation_failed",
      video2.manifest.progressFraction
    );
  }
  if (phase === "transcoding") {
    setStep(steps, "generation", "completed", "generation_ready");
    return setStep(
      steps,
      "preview",
      active ? "running" : "failed",
      active ? "preparing_preview" : "preview_failed"
    );
  }
  if (phase !== "ready") return steps;
  setStep(steps, "generation", "completed", "generation_ready");
  setStep(steps, "preview", "completed", "preview_ready");
  if (state === "verified" || state === "partial") {
    return setStep(steps, "verification", "completed", "published");
  }
  if (state === "verification_failed") {
    return setStep(steps, "verification", "failed", "verification_failed");
  }
  return setStep(
    steps,
    "verification",
    snapshot.status === "failed" ? "failed" : "running",
    snapshot.status === "failed" ? "verification_incomplete" : "verifying"
  );
}
function sessionActive(status) {
  return status === "launching" || status === "running" || status === "waiting";
}
function failedGeneration(steps) {
  return setStep(steps, "generation", "failed", "generation_failed");
}
function failedBeforeArtifact(state) {
  const detail = state === "model_quota_exhausted" || state === "provider_auth_failed" || state === "provider_unavailable" ? state : "generation_failed";
  return setStep(baseSteps(), "generation", "failed", detail);
}
function baseSteps() {
  return [
    step("request", "completed", "request_received"),
    step("generation", "pending", "waiting"),
    step("preview", "pending", "waiting"),
    step("verification", "pending", "waiting")
  ];
}
function latestVideo(videos, state) {
  if (state === "verified" || state === "partial") {
    return videos.find(isVerifiedReadyVideoArtifact) ?? videos.at(-1);
  }
  return videos.reduce(
    (latest, video2) => !latest || video2.revision > latest.revision ? video2 : latest,
    void 0
  );
}
function setStep(steps, id, status, detail, progress) {
  const index = steps.findIndex((step2) => step2.id === id);
  steps[index] = step(id, status, detail, progress);
  return steps;
}
function step(id, status, detail, progress) {
  return {
    detail,
    id,
    ...validProgress(progress) ? { progress } : {},
    status
  };
}
function validProgress(value) {
  return value !== void 0 && value >= 0 && value <= 1;
}
function agentWorkspaceViews(snapshot, presentation) {
  const allArtifacts = snapshot.items.filter((item) => item.kind === "artifact");
  const allConversationItems = snapshot.items.filter(
    (item) => item.kind !== "artifact"
  );
  const forUser = presentation === "user";
  return {
    allArtifacts,
    artifacts: forUser ? userVisibleArtifacts(allArtifacts) : allArtifacts,
    conversationItems: forUser ? userConversationItems(allConversationItems) : allConversationItems,
    videoExecutionSteps: forUser ? userVideoExecutionSteps(snapshot, allArtifacts) : []
  };
}
function startTerminalLeaseRenewal({
  expiresAt,
  leaseId,
  now = Date.now,
  onError = () => void 0,
  renew
}) {
  const interval = Math.max(expiresAt - now() - 1e4, 1e4);
  const timer = window.setInterval(() => {
    void renew(leaseId).catch(onError);
  }, interval);
  return () => window.clearInterval(timer);
}
const keys = {
  escape: "\x1B",
  interrupt: "",
  tab: "	",
  enter: "\r",
  up: "\x1B[A",
  down: "\x1B[B",
  left: "\x1B[D",
  right: "\x1B[C"
};
function TerminalMobileToolbar({
  disabled,
  send
}) {
  const chinese = useAgentWorkspaceLocale() === "zh-CN";
  return /* @__PURE__ */ jsxs(
    "div",
    {
      "aria-label": chinese ? "终端快捷键" : "Terminal shortcuts",
      className: "grid grid-cols-4 gap-1 p-1 min-[390px]:grid-cols-8",
      role: "toolbar",
      children: [
        /* @__PURE__ */ jsx(TerminalKey, { disabled, label: "ESC", onClick: () => send(keys.escape) }),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            label: "^C",
            onClick: () => send(keys.interrupt),
            tone: "danger",
            title: "Ctrl+C"
          }
        ),
        /* @__PURE__ */ jsx(TerminalKey, { disabled, label: "TAB", onClick: () => send(keys.tab) }),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            icon: /* @__PURE__ */ jsx(ArrowLeft, { className: "size-4" }),
            label: chinese ? "左方向键" : "Left",
            onClick: () => send(keys.left)
          }
        ),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            icon: /* @__PURE__ */ jsx(ArrowUp, { className: "size-4" }),
            label: chinese ? "上方向键" : "Up",
            onClick: () => send(keys.up)
          }
        ),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            icon: /* @__PURE__ */ jsx(ArrowDown, { className: "size-4" }),
            label: chinese ? "下方向键" : "Down",
            onClick: () => send(keys.down)
          }
        ),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            icon: /* @__PURE__ */ jsx(ArrowRight, { className: "size-4" }),
            label: chinese ? "右方向键" : "Right",
            onClick: () => send(keys.right)
          }
        ),
        /* @__PURE__ */ jsx(
          TerminalKey,
          {
            disabled,
            icon: /* @__PURE__ */ jsx(CornerDownLeft, { className: "size-4" }),
            label: chinese ? "回车" : "Enter",
            onClick: () => send(keys.enter)
          }
        )
      ]
    }
  );
}
function TerminalKey({
  disabled,
  icon,
  label,
  onClick,
  title,
  tone = "default"
}) {
  return /* @__PURE__ */ jsxs(
    "button",
    {
      "aria-label": label,
      className: `flex h-11 min-w-0 items-center justify-center rounded-md border px-1 font-mono text-[11px] transition-colors ${tone === "danger" ? "border-danger/40 text-danger hover:bg-danger/10" : "border-terminal-border bg-terminal-bg-active/60 text-terminal-text hover:bg-terminal-bg-active"} disabled:cursor-not-allowed disabled:opacity-40`,
      disabled,
      onClick,
      onPointerDown: (event) => event.preventDefault(),
      title: title ?? label,
      type: "button",
      children: [
        icon,
        /* @__PURE__ */ jsx("span", { className: icon ? "sr-only" : void 0, children: label })
      ]
    }
  );
}
function TerminalSurface({
  clientLabel,
  mobile = false,
  mobileToolbar,
  resource,
  runtime
}) {
  const text = useAgentWorkspaceText();
  const containerRef = useRef(null);
  const terminalRef = useRef(null);
  const resourceRef = useRef(resource);
  const hostControlled = resource.controlMode === "host";
  const leaseIdRef = useRef(null);
  const sizeRef = useRef(null);
  const stopRenewalRef = useRef(null);
  const [leaseId, setLeaseId] = useState(null);
  const [status, setStatus] = useState(resource.status);
  const statusRef = useRef(resource.status);
  const [error, setError] = useState(null);
  const canWrite = resource.writable && status === "connected" && (hostControlled || leaseId !== null);
  const writableRef = useRef(canWrite);
  writableRef.current = canWrite;
  resourceRef.current = resource;
  useEffect(() => {
    let disposed = false;
    let teardown = () => void 0;
    void Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]).then(([{ Terminal: Terminal2 }, { FitAddon }]) => {
      if (disposed || !containerRef.current) return;
      const terminalBg = getComputedStyle(document.documentElement).getPropertyValue("--terminal-bg").trim() || "#1e1e1e";
      const terminal = new Terminal2({
        disableStdin: !writableRef.current,
        convertEol: true,
        cursorBlink: true,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        fontSize: 13,
        theme: { background: terminalBg }
      });
      const fit = new FitAddon();
      terminal.loadAddon(fit);
      terminal.open(containerRef.current);
      terminalRef.current = terminal;
      if (terminal.textarea) terminal.textarea.readOnly = !writableRef.current;
      fit.fit();
      const outputOff = runtime.subscribeOutput(
        resource.id,
        (bytes) => terminal.write(bytes)
      );
      const statusOff = runtime.subscribeStatus(resource.id, (next) => {
        statusRef.current = next;
        setStatus(next);
      });
      const input = terminal.onData((data) => {
        if (resourceRef.current.writable && statusRef.current === "connected" && (hostControlled || leaseIdRef.current)) {
          void runtime.write(resource.id, new TextEncoder().encode(data)).catch((cause) => setError(errorMessage(cause)));
        }
      });
      const observer = new ResizeObserver(() => {
        fit.fit();
        sizeRef.current = { columns: terminal.cols, rows: terminal.rows };
        if (resourceRef.current.writable && statusRef.current === "connected" && (hostControlled || leaseIdRef.current)) {
          void runtime.resize(resource.id, terminal.cols, terminal.rows).catch((cause) => setError(errorMessage(cause)));
        }
      });
      observer.observe(containerRef.current);
      void runtime.connect(resourceRef.current).catch((cause) => setError(errorMessage(cause)));
      teardown = () => {
        observer.disconnect();
        input.dispose();
        outputOff();
        statusOff();
        terminal.dispose();
        terminalRef.current = null;
        runtime.disconnect(resource.id);
      };
    }).catch((cause) => setError(errorMessage(cause)));
    return () => {
      disposed = true;
      stopRenewalRef.current?.();
      teardown();
    };
  }, [hostControlled, resource.id, runtime]);
  const acquireControl = async () => {
    setError(null);
    try {
      const lease = await runtime.acquireControl(resource.id, clientLabel);
      leaseIdRef.current = lease.leaseId;
      stopRenewalRef.current = startTerminalLeaseRenewal({
        expiresAt: lease.expiresAt,
        leaseId: lease.leaseId,
        onError: (cause) => {
          stopRenewalRef.current?.();
          stopRenewalRef.current = null;
          leaseIdRef.current = null;
          setLeaseId(null);
          setError(errorMessage(cause));
        },
        renew: (activeLeaseId) => runtime.renewControl(resource.id, activeLeaseId)
      });
      setLeaseId(lease.leaseId);
      if (sizeRef.current) {
        await runtime.resize(
          resource.id,
          sizeRef.current.columns,
          sizeRef.current.rows
        );
      }
    } catch (cause) {
      setError(errorMessage(cause));
    }
  };
  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.options.disableStdin = !canWrite;
      if (terminalRef.current.textarea) terminalRef.current.textarea.readOnly = !canWrite;
    }
    if (!canWrite || !sizeRef.current) return;
    void runtime.resize(resource.id, sizeRef.current.columns, sizeRef.current.rows).catch((cause) => setError(errorMessage(cause)));
  }, [canWrite, resource.id, runtime]);
  const sendToolbarInput = (input) => {
    if (!canWrite) return;
    void runtime.write(resource.id, new TextEncoder().encode(input)).catch((cause) => setError(errorMessage(cause)));
  };
  return /* @__PURE__ */ jsxs("section", { className: "flex min-h-0 min-w-0 flex-1 flex-col bg-terminal-bg text-terminal-text", children: [
    /* @__PURE__ */ jsxs("div", { className: "flex h-10 items-center gap-2 border-b border-terminal-border px-3 text-xs", children: [
      /* @__PURE__ */ jsx("span", { className: "truncate", children: resource.label }),
      /* @__PURE__ */ jsx("span", { className: "text-terminal-text-muted", children: status }),
      error && /* @__PURE__ */ jsx("span", { className: "truncate text-danger", children: error }),
      resource.writable && !hostControlled && !leaseId && /* @__PURE__ */ jsxs(
        "button",
        {
          "aria-label": text.takeControl,
          className: "ml-auto flex h-7 items-center gap-1.5 rounded-md border border-terminal-border px-2",
          onClick: () => void acquireControl(),
          type: "button",
          children: [
            /* @__PURE__ */ jsx(Keyboard, { className: "size-3.5" }),
            text.takeControl
          ]
        }
      )
    ] }),
    /* @__PURE__ */ jsx("div", { className: "min-h-0 flex-1 overflow-hidden p-2", ref: containerRef }),
    mobile && /* @__PURE__ */ jsx("div", { className: "shrink-0 border-t border-terminal-border bg-terminal-bg-secondary", children: mobileToolbar ? mobileToolbar({ disabled: !canWrite, send: sendToolbarInput }) : /* @__PURE__ */ jsx(TerminalMobileToolbar, { disabled: !canWrite, send: sendToolbarInput }) })
  ] });
}
function errorMessage(value) {
  return value instanceof Error ? value.message : String(value);
}
function workbenchContainerMode(width) {
  if (width >= 960) return "wide";
  if (width >= 640) return "medium";
  return "narrow";
}
function useWorkbenchContainerMode() {
  const containerRef = useRef(null);
  const [mode, setMode] = useState("narrow");
  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const measure = () => {
      setMode(workbenchContainerMode(container.getBoundingClientRect().width));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(container);
    return () => observer.disconnect();
  }, []);
  return { containerRef, mode };
}
function useAgentSessionSnapshot(runtime, sessionId, lifecycleRuntime = runtime) {
  useEffect(() => {
    void lifecycleRuntime.open(sessionId).catch(() => void 0);
    return () => lifecycleRuntime.close(sessionId);
  }, [lifecycleRuntime, sessionId]);
  return useSyncExternalStore(
    (listener) => runtime.subscribe(sessionId, listener),
    () => runtime.getSnapshot(sessionId),
    () => runtime.getSnapshot(sessionId)
  );
}
function useElementFullscreen(target) {
  const [active, setActive] = useState(false);
  const [supported, setSupported] = useState(false);
  useEffect(() => {
    setSupported(
      typeof document !== "undefined" && !!document.fullscreenEnabled && typeof target.current?.requestFullscreen === "function"
    );
  }, [target]);
  useEffect(() => {
    if (!supported) return;
    const sync = () => setActive(document.fullscreenElement === target.current);
    document.addEventListener("fullscreenchange", sync);
    return () => document.removeEventListener("fullscreenchange", sync);
  }, [supported, target]);
  const toggle = useCallback(() => {
    const element = target.current;
    if (!element || typeof document === "undefined") return;
    if (document.fullscreenElement === element) {
      void document.exitFullscreen?.();
    } else {
      void element.requestFullscreen?.();
    }
  }, [target]);
  return { active, supported, toggle };
}
function WorkspaceErrorBanner({
  dismissLabel,
  message,
  onDismiss
}) {
  return /* @__PURE__ */ jsxs(
    "div",
    {
      className: "flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive",
      role: "alert",
      children: [
        /* @__PURE__ */ jsx("span", { className: "min-w-0 flex-1", children: message }),
        onDismiss && /* @__PURE__ */ jsx(
          "button",
          {
            "aria-label": dismissLabel,
            className: "flex size-6 shrink-0 items-center justify-center rounded-md outline-none hover:bg-destructive/10 focus-visible:ring-2 focus-visible:ring-ring",
            onClick: onDismiss,
            title: dismissLabel,
            type: "button",
            children: /* @__PURE__ */ jsx(X, { className: "size-3.5" })
          }
        )
      ]
    }
  );
}
class ReadOnlyAgentSessionRuntime {
  constructor(source) {
    this.source = source;
    this.formDraftScope = source.formDraftScope ?? source;
    if (source.loadArtifact) {
      this.loadArtifact = source.loadArtifact.bind(source);
    }
    if (source.loadAttachment) {
      this.loadAttachment = source.loadAttachment.bind(source);
    }
    if (source.loadPreviewUrl) {
      this.loadPreviewUrl = source.loadPreviewUrl.bind(source);
    }
    if (source.getFormState) {
      this.getFormState = source.getFormState.bind(source);
    }
  }
  source;
  formDraftScope;
  loadArtifact;
  loadAttachment;
  loadPreviewUrl;
  getFormState;
  sourceSnapshot = null;
  projectedSnapshot = null;
  open(sessionId) {
    return this.source.open(sessionId);
  }
  close(sessionId) {
    this.source.close(sessionId);
  }
  getSnapshot(sessionId) {
    const source = this.source.getSnapshot(sessionId);
    if (source !== this.sourceSnapshot || !this.projectedSnapshot) {
      this.sourceSnapshot = source;
      this.projectedSnapshot = {
        ...source,
        capabilities: {
          ...source.capabilities,
          interrupt: false,
          resolvePermission: false,
          sendMessage: false,
          updateConfiguration: false,
          executeToolAction: false,
          extensions: []
        },
        terminals: source.terminals.map((terminal) => ({
          ...terminal,
          writable: false
        }))
      };
    }
    return this.projectedSnapshot;
  }
  subscribe(sessionId, listener) {
    return this.source.subscribe(sessionId, listener);
  }
  sendMessage(_sessionId, _commandId, _input) {
    return rejectedReadOnlyCommand();
  }
  interrupt(_sessionId, _commandId) {
    return rejectedReadOnlyCommand();
  }
  resolvePermission(_sessionId, _commandId, _permissionId, _result) {
    return rejectedReadOnlyCommand();
  }
  updateConfiguration(_sessionId, _commandId, _patch) {
    return rejectedReadOnlyCommand();
  }
  executeToolAction(_sessionId, _action) {
    return rejectedReadOnlyCommand();
  }
  executeExtension(_sessionId, _commandId, _command) {
    return rejectedReadOnlyCommand();
  }
  loadOlder(sessionId, beforeItemId) {
    return this.source.loadOlder(sessionId, beforeItemId);
  }
}
function rejectedReadOnlyCommand() {
  return Promise.reject(new Error("agent_session_read_only"));
}
function resolveWorkbenchMode(input) {
  if (!input.hasCodeSource && !input.goalAvailable) {
    if (input.controlled === "code") {
      return { mode: "code", showToggle: false, unavailable: true };
    }
    return { mode: "work", showToggle: false, unavailable: false };
  }
  const mode = input.controlled ?? input.stored ?? input.hostDefault ?? "work";
  return { mode, showToggle: true, unavailable: false };
}
function workbenchModeStorageKey(scope, sessionId, workspaceKey) {
  return `agent-ui.workbenchMode:${scope.tenantId}:${scope.userId}:${sessionId}:${workspaceKey}`;
}
function readWorkbenchMode(storage, scope, sessionId, workspaceKey) {
  try {
    const raw = storage.getItem(
      workbenchModeStorageKey(scope, sessionId, workspaceKey)
    );
    if (raw === "code" || raw === "work" || raw === "goal") return raw;
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return parsed === "code" || parsed === "work" || parsed === "goal" ? parsed : null;
  } catch {
    return null;
  }
}
function writeWorkbenchMode(storage, scope, sessionId, workspaceKey, mode) {
  try {
    storage.setItem(
      workbenchModeStorageKey(scope, sessionId, workspaceKey),
      mode
    );
  } catch {
    return;
  }
}
function useResolvedWorkbenchMode(input) {
  const stored = storedWorkbenchMode(input);
  const [userChoice, setUserChoice] = useState(null);
  const resolved = resolveWorkbenchMode({
    controlled: input.controlled,
    stored: userChoice ?? stored,
    hostDefault: input.hostDefault,
    hasCodeSource: input.codeWorkspace != null,
    goalAvailable: input.goalAvailable
  });
  const setMode = (next) => {
    setUserChoice(next);
    input.onChange?.(next);
    const storage = browserStorage();
    if (!storage || !input.preferenceScope || !input.codeWorkspace) return;
    writeWorkbenchMode(
      storage,
      input.preferenceScope,
      input.sessionId,
      workspaceKeyForSession(input.codeWorkspace, input.sessionId),
      next
    );
  };
  return { ...resolved, setMode };
}
function storedWorkbenchMode(input) {
  const storage = browserStorage();
  if (!storage || !input.preferenceScope || !input.codeWorkspace) return null;
  return readWorkbenchMode(
    storage,
    input.preferenceScope,
    input.sessionId,
    workspaceKeyForSession(input.codeWorkspace, input.sessionId)
  );
}
function browserStorage() {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}
class RendererRegistryStore {
  registryEntries = /* @__PURE__ */ new Map();
  keyId;
  constructor(keyId) {
    this.keyId = keyId;
  }
  register(key, renderer, sourceId) {
    const id = this.keyId(key);
    const existing = this.registryEntries.get(id);
    if (existing) {
      throw new Error(
        `renderer_key_conflict: key=${id} existing_source=${existing.sourceId} new_source=${sourceId}`
      );
    }
    this.registryEntries.set(id, { key, renderer, sourceId });
  }
  has(key) {
    return this.registryEntries.has(this.keyId(key));
  }
  lookup(key) {
    return this.registryEntries.get(this.keyId(key))?.renderer;
  }
  entries() {
    return [...this.registryEntries.values()].map((entry) => ({
      key: entry.key,
      renderer: entry.renderer,
      sourceId: entry.sourceId
    }));
  }
  replace(key, renderer, replacement) {
    const id = this.keyId(key);
    const existing = this.registryEntries.get(id);
    if (!existing) {
      throw new Error(`renderer_key_missing: key=${id}`);
    }
    if (existing.sourceId !== replacement.expectedSourceId) {
      throw new Error(
        `renderer_source_mismatch: key=${id} actual_source=${existing.sourceId} expected_source=${replacement.expectedSourceId}`
      );
    }
    this.registryEntries.set(id, {
      key,
      renderer,
      sourceId: replacement.sourceId
    });
  }
}
function toolRendererKeyId(key) {
  return JSON.stringify([
    requiredKeyField("namespace", key.namespace),
    requiredKeyField("semanticKey", key.semanticKey),
    requiredKeyField("schemaVersion", key.schemaVersion)
  ]);
}
function contentRendererKeyId(key) {
  return JSON.stringify([
    requiredKeyField("blockKind", key.blockKind),
    optionalKeyField("manifestType", key.manifestType),
    optionalKeyField("namespace", key.namespace),
    optionalKeyField("semanticKey", key.semanticKey),
    optionalKeyField("mediaType", key.mediaType),
    optionalKeyField("role", key.role),
    requiredKeyField("schemaVersion", key.schemaVersion)
  ]);
}
function requiredKeyField(name, value) {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`renderer_key_invalid: field=${name}`);
  }
  return value;
}
function optionalKeyField(name, value) {
  if (value === void 0) {
    return void 0;
  }
  return requiredKeyField(name, value);
}
class ContentRendererRegistry {
  store = new RendererRegistryStore(contentRendererKeyId);
  register(key, renderer, sourceId) {
    this.store.register(key, renderer, sourceId);
  }
  has(key) {
    return this.store.has(key);
  }
  lookup(key) {
    return this.store.lookup(key);
  }
  entries() {
    return this.store.entries();
  }
  replace(key, renderer, replacement) {
    this.store.replace(key, renderer, replacement);
  }
}
class ToolRendererRegistry {
  store = new RendererRegistryStore(toolRendererKeyId);
  register(key, renderer, sourceId) {
    this.store.register(key, renderer, sourceId);
  }
  has(key) {
    return this.store.has(key);
  }
  lookup(key) {
    return this.store.lookup(key);
  }
  entries() {
    return this.store.entries();
  }
  replace(key, renderer, replacement) {
    this.store.replace(key, renderer, replacement);
  }
}
function mergeAgentAdapterRenderers(adapter, contentRenderers, toolRenderers) {
  if (!adapter?.renderers) return { contentRenderers, toolRenderers };
  return {
    contentRenderers: mergeContentRenderers(
      adapter,
      contentRenderers
    ),
    toolRenderers: mergeToolRenderers(adapter, toolRenderers)
  };
}
function mergeContentRenderers(adapter, source) {
  const entries = adapter.renderers?.content ?? [];
  if (entries.length === 0) return source;
  const next = new ContentRendererRegistry();
  for (const entry of source?.entries() ?? []) {
    next.register(entry.key, entry.renderer, entry.sourceId);
  }
  for (const entry of entries) {
    next.register(
      entry.key,
      entry.renderer,
      entry.sourceId ?? `adapter.${adapter.descriptor.id}`
    );
  }
  return next;
}
function mergeToolRenderers(adapter, source) {
  const entries = adapter.renderers?.tools ?? [];
  if (entries.length === 0) return source;
  const next = new ToolRendererRegistry();
  for (const entry of source?.entries() ?? []) {
    next.register(entry.key, entry.renderer, entry.sourceId);
  }
  for (const entry of entries) {
    next.register(
      entry.key,
      entry.renderer,
      entry.sourceId ?? `adapter.${adapter.descriptor.id}`
    );
  }
  return next;
}
function AgentWorkspace({
  runtime,
  terminalRuntime,
  sessionId,
  adapter,
  harness,
  goal,
  clientLabel = "agent-workspace",
  className = "",
  contentRenderers,
  headerActions,
  composer,
  renderModelControl,
  inspectorTabs,
  inspectorPanel,
  domainPanel,
  locale = "en-US",
  presentation = "developer",
  mobile = false,
  mobileTerminalToolbar,
  readOnly = false,
  showFullscreen = true,
  toolRenderers,
  workspaceArtifacts = [],
  workspaceFiles,
  mentionHarness = null,
  defaultView,
  sessionRecovery,
  workbenchMode: controlledMode,
  defaultWorkbenchMode,
  onWorkbenchModeChange,
  codeWorkspace,
  preferenceScope
}) {
  const activeHarness = harness ?? adapter?.descriptor;
  const activeWorkspaceFiles = workspaceFiles ?? adapter?.workspace?.files;
  const activeCodeWorkspace = codeWorkspace ?? adapter?.workspace?.code;
  const adapterRenderers = useMemo(
    () => mergeAgentAdapterRenderers(adapter, contentRenderers, toolRenderers),
    [adapter, contentRenderers, toolRenderers]
  );
  const activeRuntime = useMemo(
    () => readOnly ? new ReadOnlyAgentSessionRuntime(runtime) : runtime,
    [readOnly, runtime]
  );
  const snapshot = useAgentSessionSnapshot(activeRuntime, sessionId, runtime);
  const text = agentWorkspaceText(locale);
  const [selectedView, setSelectedView] = useState(null);
  const tabId = useId();
  const conversationTabId = `${tabId}-conversation-tab`;
  const conversationPanelId = `${tabId}-conversation-panel`;
  const terminalTabId = `${tabId}-terminal-tab`;
  const terminalPanelId = `${tabId}-terminal-panel`;
  const [surfaceError, setSurfaceError] = useState(null);
  const { containerRef, mode } = useWorkbenchContainerMode();
  const fullscreen = useElementFullscreen(containerRef);
  const workbench = useResolvedWorkbenchMode({
    codeWorkspace: activeCodeWorkspace,
    controlled: controlledMode,
    hostDefault: defaultWorkbenchMode,
    onChange: onWorkbenchModeChange,
    preferenceScope,
    sessionId,
    goalAvailable: goal != null
  });
  const terminal = snapshot.terminals[0];
  const activeSurfaceError = surfaceError?.sessionId === sessionId && surfaceError.commandId === snapshot.latestUserCommandId ? surfaceError.message : null;
  const views = agentWorkspaceViews(snapshot, presentation);
  const terminalEnabled = presentation === "developer" && snapshot.capabilities.terminal && terminalRuntime !== void 0 && terminal !== void 0;
  const view = (selectedView ?? defaultView ?? "conversation") === "terminal" && terminalEnabled ? "terminal" : "conversation";
  return /* @__PURE__ */ jsx(AgentWorkspaceLocaleProvider, { locale, children: /* @__PURE__ */ jsx(
    WorkspaceFileNavigationProvider,
    {
      onReveal: () => workbench.setMode("code"),
      children: /* @__PURE__ */ jsxs(
        "div",
        {
          className: `flex h-full min-h-0 flex-col overflow-hidden bg-background text-foreground ${fullscreen.active ? "agent-workspace-fullscreen" : ""} ${className}`,
          "data-agent-workspace": sessionId,
          "data-agent-harness": activeHarness?.id,
          "data-workbench-mode": workbench.mode,
          "data-workspace-view": view,
          ref: containerRef,
          children: [
            /* @__PURE__ */ jsx(
              AgentWorkspaceViewHeader,
              {
                actions: {
                  fullscreen,
                  goalAvailable: goal != null,
                  headerActions,
                  onWorkbenchModeChange: workbench.setMode,
                  showFullscreen,
                  showToggle: workbench.showToggle,
                  workbenchMode: workbench.mode
                },
                presentation,
                snapshot,
                tabs: {
                  conversationPanelId,
                  conversationTabId,
                  onViewChange: setSelectedView,
                  terminalEnabled,
                  terminalPanelId,
                  terminalTabId,
                  view
                }
              }
            ),
            (activeSurfaceError || snapshot.error) && /* @__PURE__ */ jsx(
              WorkspaceErrorBanner,
              {
                dismissLabel: text.dismissError,
                message: presentation === "user" ? text.conversationError : snapshot.error || activeSurfaceError || "",
                onDismiss: activeSurfaceError ? () => setSurfaceError(null) : void 0
              }
            ),
            domainPanel,
            view === "terminal" && terminalEnabled ? /* @__PURE__ */ jsx(
              "section",
              {
                "aria-labelledby": terminalTabId,
                className: "flex min-h-0 flex-1",
                id: terminalPanelId,
                role: "tabpanel",
                children: /* @__PURE__ */ jsx(
                  TerminalSurface,
                  {
                    clientLabel,
                    mobile,
                    mobileToolbar: mobileTerminalToolbar,
                    resource: terminal,
                    runtime: terminalRuntime
                  }
                )
              }
            ) : /* @__PURE__ */ jsx(
              "section",
              {
                "aria-labelledby": conversationTabId,
                className: "flex min-h-0 flex-1 flex-col",
                id: conversationPanelId,
                role: "tabpanel",
                children: /* @__PURE__ */ jsx(
                  AgentWorkspaceConversationPanel,
                  {
                    allArtifacts: views.allArtifacts,
                    artifacts: views.artifacts,
                    codeWorkspace: activeCodeWorkspace,
                    composer,
                    contentRenderers: adapterRenderers.contentRenderers,
                    conversationItems: views.conversationItems,
                    mentionHarness,
                    mode,
                    onError: (message) => setSurfaceError({ sessionId, message, commandId: activeRuntime.getSnapshot(sessionId).latestUserCommandId }),
                    plan: snapshot.plan,
                    presentation,
                    mobile,
                    runtime: activeRuntime,
                    renderModelControl,
                    sessionId,
                    sessionRecovery,
                    snapshot,
                    toolRenderers: adapterRenderers.toolRenderers,
                    videoExecutionSteps: views.videoExecutionSteps,
                    workbenchMode: workbench.mode,
                    workbenchUnavailable: workbench.unavailable,
                    preferenceScope,
                    workspaceArtifacts,
                    goal,
                    workspaceFiles: activeWorkspaceFiles
                  }
                )
              }
            ),
            inspectorTabs,
            inspectorPanel
          ]
        }
      )
    }
  ) });
}
function refKey(ref) {
  return `pod:${ref.podKey}:${ref.sessionId ?? "default"}`;
}
const defaultAgentHarnessCapabilities = {
  artifacts: true,
  codeWorkspace: false,
  goals: false,
  permissions: false,
  subagents: false,
  terminal: false
};
const defaultAgentHarnessWorkspaceCapabilities = {
  files: false,
  code: false,
  changes: false,
  terminal: false
};
const defaultAgentHarnessPermissionCapabilities = {
  approval: false,
  questions: false,
  customAnswers: false
};
function createAgentHarnessDescriptor(input) {
  const id = input.id.trim();
  if (!id) throw new Error("agent_harness_id_missing");
  const capabilities = {
    ...defaultAgentHarnessCapabilities,
    ...input.capabilities
  };
  return {
    id,
    profile: input.profile ?? "unknown",
    questionMode: input.questionMode ?? "unknown",
    protocol: input.protocol?.trim() || "unknown",
    displayName: input.displayName.trim() || id,
    sourceNamespaces: input.sourceNamespaces?.length ? [...new Set(input.sourceNamespaces)] : [`agentcloud.${input.protocol?.trim() || "unknown"}`],
    capabilities,
    workspaceCapabilities: {
      ...defaultAgentHarnessWorkspaceCapabilities,
      code: capabilities.codeWorkspace,
      terminal: capabilities.terminal,
      ...input.workspaceCapabilities
    },
    permissionCapabilities: {
      ...defaultAgentHarnessPermissionCapabilities,
      approval: capabilities.permissions,
      questions: capabilities.permissions,
      ...input.permissionCapabilities
    }
  };
}
function createAgentAdapterBridge(adapter, runtime, sessionId) {
  const context = () => ({
    runtime,
    sessionId,
    snapshot: runtime.getSnapshot(sessionId)
  });
  return {
    adapter,
    sessionId,
    context,
    projectCommand(command) {
      const projection = adapter.commandProjection;
      if (!projection) {
        throw new Error(`agent_adapter_command_projection_missing: id=${adapter.descriptor.id}`);
      }
      if (!projection.commands.some((candidate) => candidate.name === command.name)) {
        throw new Error(
          `agent_adapter_command_unsupported: id=${adapter.descriptor.id} command=${command.name}`
        );
      }
      return projection.project(command, context());
    },
    decodeEvent(event) {
      const projection = adapter.eventProjection;
      if (!projection) {
        throw new Error(`agent_adapter_event_projection_missing: id=${adapter.descriptor.id}`);
      }
      return projection.decode?.(event) ?? event;
    },
    projectEvent(event) {
      const projection = adapter.eventProjection;
      if (!projection) {
        throw new Error(`agent_adapter_event_projection_missing: id=${adapter.descriptor.id}`);
      }
      return projection.project(
        projection.decode?.(event) ?? event,
        context()
      );
    }
  };
}
class WorkerClient {
  transports = /* @__PURE__ */ new Map();
  sessions = /* @__PURE__ */ new Map();
  register(transport) {
    this.transports.set(transport.kind, transport);
  }
  transportFor(ref) {
    const transport = this.transports.get(ref.transport);
    if (!transport) {
      throw new Error(`no transport registered for ${ref.transport}`);
    }
    return transport;
  }
  resolveSession(ref, signal) {
    if (signal) {
      return this.transportFor(ref).resolveSession(ref, signal);
    }
    const key = refKey(ref);
    let pending = this.sessions.get(key);
    if (!pending) {
      pending = this.transportFor(ref).resolveSession(ref).catch((error) => {
        this.sessions.delete(key);
        throw error;
      });
      this.sessions.set(key, pending);
    }
    return pending;
  }
  runtimeFor(ref, sessionId) {
    return this.transportFor(ref).runtimeFor(sessionId);
  }
  adapterFor(ref, sessionId) {
    return this.transportFor(ref).adapterForSession?.(sessionId);
  }
  adapterBridgeFor(ref, sessionId, providedAdapter) {
    const adapter = providedAdapter ?? this.adapterFor(ref, sessionId);
    if (!adapter) return void 0;
    return createAgentAdapterBridge(
      adapter,
      this.runtimeFor(ref, sessionId),
      sessionId
    );
  }
  release(sessionId) {
    for (const transport of this.transports.values()) {
      transport.closeSession?.(sessionId);
    }
  }
  clearSession(ref) {
    this.sessions.delete(refKey(ref));
  }
}
const emptyWorkerDirectoryState = {
  archivedSessionIds: [],
  expandedGroupIds: [],
  sessionOrder: []
};
function reconcileWorkerSessionOrder(sessionIds, savedOrder) {
  const members = new Set(sessionIds);
  const ordered = savedOrder.filter((id, index) => members.has(id) && savedOrder.indexOf(id) === index);
  const included = new Set(ordered);
  return [...ordered, ...sessionIds.filter((id) => !included.has(id))];
}
function workerDirectoryStorageKey(workerId) {
  return `agent-ui.worker-directory:v1:${workerId}`;
}
function readWorkerDirectoryState(storage, workerId) {
  if (!storage || !workerId) return emptyWorkerDirectoryState;
  try {
    const value = JSON.parse(storage.getItem(workerDirectoryStorageKey(workerId)) ?? "null");
    if (!value || typeof value !== "object") return emptyWorkerDirectoryState;
    const row = value;
    return {
      activeSessionId: typeof row.activeSessionId === "string" ? row.activeSessionId : void 0,
      archivedSessionIds: strings(row.archivedSessionIds),
      expandedGroupIds: strings(row.expandedGroupIds),
      sessionOrder: strings(row.sessionOrder)
    };
  } catch {
    return emptyWorkerDirectoryState;
  }
}
function writeWorkerDirectoryState(storage, workerId, state) {
  if (!storage || !workerId) return;
  storage.setItem(workerDirectoryStorageKey(workerId), JSON.stringify(state));
}
function strings(value) {
  return Array.isArray(value) ? value.filter((item) => typeof item === "string") : [];
}
function isWorkerSessionReadable(liveness) {
  return liveness.state === "online" || liveness.state === "unknown";
}
function sameWorkerLiveness(left, right) {
  if (left.state !== right.state) return false;
  if (left.state === "starting" && right.state === "starting") {
    return left.progress === right.progress;
  }
  if (left.state === "online" && right.state === "online") {
    return left.readOnly === right.readOnly;
  }
  if (left.state === "unreachable" && right.state === "unreachable") {
    const leftDetail = "detail" in left.cause ? left.cause.detail : null;
    const rightDetail = "detail" in right.cause ? right.cause.detail : null;
    return left.cause.reason === right.cause.reason && leftDetail === rightDetail;
  }
  return true;
}
function projectPodLiveness(input) {
  if (input.podStatus === "paused" || input.podStatus === "disconnected") {
    return { state: "online", readOnly: input.podStatus };
  }
  if (input.podError || input.podStatus === "error") {
    return {
      state: "unreachable",
      cause: { reason: "launch-failed", detail: input.podError },
      recovery: [{ kind: "retry" }]
    };
  }
  if (input.podStatus === "completed" || input.podStatus === "terminated") {
    return { state: "online", readOnly: "ended" };
  }
  if (input.podStatus === "orphaned") {
    return {
      state: "unreachable",
      cause: { reason: "orphaned" },
      recovery: [{ kind: "wait" }]
    };
  }
  if (input.podStatus === "running" && input.isPodReady) {
    return { state: "online", readOnly: input.controlGranted ? null : "control" };
  }
  if (input.podStatus === "unknown") {
    return { state: "unknown" };
  }
  return { state: "starting", progress: input.initProgress };
}
function projectAttachmentFailure(failure, detail) {
  switch (failure) {
    case "forbidden":
      return {
        state: "unreachable",
        cause: { reason: "forbidden" },
        recovery: []
      };
    case "not-created":
      return {
        state: "unreachable",
        cause: { reason: "session-not-created" },
        recovery: [{ kind: "retry" }]
      };
    default:
      return {
        state: "unreachable",
        cause: { reason: "session-unresolved", detail },
        recovery: [{ kind: "retry" }]
      };
  }
}
class SessionAttachmentError extends Error {
  constructor(failure, message) {
    super(message);
    this.failure = failure;
    this.name = "SessionAttachmentError";
  }
  failure;
}
function sessionAttachmentFailureOf(error) {
  return error instanceof SessionAttachmentError ? error.failure : "load-failed";
}
function useRunnerOnlineEdge(online, refresh) {
  const prev = useRef(void 0);
  useEffect(() => {
    if (online === true && prev.current !== true) refresh();
    prev.current = online;
  }, [online, refresh]);
}
function buildReconnectCommand(input) {
  const lines = [
    "runner run \\",
    `  # resume session ${input.sessionId}`,
    `  # server: ${input.serverUrl}`
  ];
  if (input.wrapper !== "claude-code-native-ui") {
    lines.push("  # or re-open the session from Agent Cloud web UI");
  }
  return lines.join("\n");
}
function recoveryOptionsFor(cause, ctx) {
  if (cause.reason === "orphaned") return [{ kind: "wait" }];
  if (cause.reason !== "stranded") return [];
  const out = [
    { kind: "cli", command: buildReconnectCommand(ctx) }
  ];
  if (ctx.isUnboundFork) {
    out.push({
      kind: "resume-directory",
      sourceHostId: ctx.sourceHostId
    });
  }
  out.push({ kind: "fork" });
  return out;
}
function isUnboundCodingFork(labels, workspace) {
  if (!labels["agent-cloud.fork.source_id"]) return false;
  return workspace == null || workspace === "";
}
function sessionReadOnlyReason(labels, permissionLevel) {
  if (permissionLevel === 1) return "permission";
  if (labels["agent-cloud.closed"] === "true") return "closed-subagent";
  if (labels["agent-cloud.wrapper"] === "claude-code-native-ui-subagent") {
    return "native-subagent";
  }
  return null;
}
const WorkerClientContext = createContext(null);
function WorkerProvider({
  children,
  client,
  transports
}) {
  const value = useMemo(() => {
    if (client) return client;
    const next = new WorkerClient();
    for (const transport of transports ?? []) next.register(transport);
    return next;
  }, [client, transports]);
  return /* @__PURE__ */ jsx(WorkerClientContext.Provider, { value, children });
}
function useWorkerClient() {
  const client = useContext(WorkerClientContext);
  if (!client) {
    throw new Error("WorkerProvider is required");
  }
  return client;
}
const WORKER_LIVENESS_COPY = {
  "en-US": {
    starting: "Waiting for Worker to be ready…",
    reasons: {
      "launch-failed": "The Worker failed to start. Please try again.",
      orphaned: "Worker is reconnecting. Please wait.",
      "status-unavailable": "Could not load this Worker's status. Please retry.",
      stranded: "Session offline — reconnect to continue.",
      "session-unresolved": "Could not load this Worker session. Please retry.",
      "session-not-created": "This Worker has no conversation yet. It appears once the agent starts.",
      forbidden: "You no longer have access to this Worker."
    },
    reconnectCli: "Reconnect with:",
    retry: "Retry"
  },
  "zh-CN": {
    starting: "正在等待 Worker 就绪…",
    reasons: {
      "launch-failed": "Worker 启动失败，请稍后重试。",
      orphaned: "Worker 正在重连，请稍候。",
      "status-unavailable": "无法获取该 Worker 的状态，请重试。",
      stranded: "会话离线 — 请重连后继续。",
      "session-unresolved": "无法加载此 Worker 会话，请重试。",
      "session-not-created": "该 Worker 还没有会话，Agent 启动后会自动出现。",
      forbidden: "你没有访问该 Worker 的权限。"
    },
    reconnectCli: "使用以下命令重连：",
    retry: "重试"
  }
};
function workerLivenessMessage(liveness, copy) {
  if (liveness.state !== "unreachable") return copy.starting;
  return copy.reasons[liveness.cause.reason];
}
function workerLivenessDetail(liveness) {
  if (liveness.state !== "unreachable") return null;
  return "detail" in liveness.cause ? liveness.cause.detail : null;
}
function WorkerLivenessView({
  liveness,
  locale = "en-US",
  className = "",
  onRetry
}) {
  const copy = WORKER_LIVENESS_COPY[locale] ?? WORKER_LIVENESS_COPY["en-US"];
  const message = workerLivenessMessage(liveness, copy);
  const detail = workerLivenessDetail(liveness);
  const role = liveness.state === "unreachable" ? "alert" : "status";
  const recovery = liveness.state === "unreachable" ? liveness.recovery : [];
  const cli = recovery.find((item) => item.kind === "cli");
  const canRetry = Boolean(onRetry) && recovery.some((item) => item.kind === "retry");
  return /* @__PURE__ */ jsx(
    "div",
    {
      className: `flex h-full min-h-0 flex-1 items-center justify-center px-6 text-center text-sm text-muted-foreground ${className}`,
      role,
      children: /* @__PURE__ */ jsxs("div", { className: "max-w-sm space-y-2", children: [
        /* @__PURE__ */ jsx("p", { children: message }),
        liveness.state === "starting" && liveness.progress ? /* @__PURE__ */ jsx("p", { className: "text-xs opacity-80", children: liveness.progress }) : null,
        detail ? /* @__PURE__ */ jsx("p", { className: "text-xs opacity-80", children: detail }) : null,
        cli && cli.kind === "cli" ? /* @__PURE__ */ jsxs("div", { className: "pt-2 text-left", children: [
          /* @__PURE__ */ jsx("p", { className: "mb-1 text-xs", children: copy.reconnectCli }),
          /* @__PURE__ */ jsx("pre", { className: "overflow-x-auto rounded bg-muted/40 p-2 text-xs whitespace-pre-wrap", children: cli.command })
        ] }) : null,
        canRetry ? /* @__PURE__ */ jsx(
          "button",
          {
            className: "mt-3 inline-flex h-9 items-center justify-center rounded-md border bg-background px-3 text-sm text-foreground",
            onClick: onRetry,
            type: "button",
            children: copy.retry
          }
        ) : null
      ] })
    }
  );
}
function useWorkerLiveness(client, workerRef) {
  const [liveness, setLiveness] = useState({
    state: "unknown"
  });
  const [attempt, setAttempt] = useState(0);
  const key = refKey(workerRef);
  const refHolder = useRef(workerRef);
  refHolder.current = workerRef;
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  useEffect(() => {
    const transport = client.transportFor(refHolder.current);
    return transport.subscribeLiveness(refHolder.current, (next) => {
      setLiveness((prev) => sameWorkerLiveness(prev, next) ? prev : next);
    });
  }, [client, key, attempt]);
  return { liveness, retry };
}
function useWorkerSessionId(client, workerRef, liveness) {
  const [resolved, setResolved] = useState(null);
  const [resolveError, setResolveError] = useState(null);
  const [resolveFailure, setResolveFailure] = useState(null);
  const [attempt, setAttempt] = useState(0);
  const readable = isWorkerSessionReadable(liveness);
  const key = refKey(workerRef);
  const sessionId = resolved?.client === client && resolved.key === key ? resolved.sessionId : null;
  const refHolder = useRef(workerRef);
  refHolder.current = workerRef;
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  useEffect(() => {
    if (!readable) {
      setResolved(null);
      setResolveError(null);
      setResolveFailure(null);
      return;
    }
    let active = true;
    const controller = new AbortController();
    const currentRef = refHolder.current;
    setResolveError(null);
    setResolveFailure(null);
    void client.resolveSession(currentRef, controller.signal).then(
      (id) => {
        if (active) setResolved({ client, key, sessionId: id });
      },
      (error) => {
        if (!active) return;
        setResolved(null);
        setResolveError(
          error instanceof Error ? error.message : "Failed to resolve session"
        );
        setResolveFailure(sessionAttachmentFailureOf(error));
      }
    );
    return () => {
      active = false;
      controller.abort();
    };
  }, [client, readable, key, attempt]);
  useEffect(() => {
    return () => {
      if (sessionId) client.release(sessionId);
    };
  }, [client, sessionId]);
  return { sessionId, resolveError, resolveFailure, retry };
}
function useWorkerRuntime(client, workerRef, sessionId, liveness) {
  const [runtime, setRuntime] = useState(null);
  const [openError, setOpenError] = useState(null);
  const [openAttempt, setOpenAttempt] = useState(0);
  const key = refKey(workerRef);
  const refHolder = useRef(workerRef);
  refHolder.current = workerRef;
  const livenessKey = liveness.state === "online" ? `online:${liveness.readOnly ?? ""}` : liveness.state;
  const retryOpen = useCallback(() => setOpenAttempt((n) => n + 1), []);
  useEffect(() => {
    if (!sessionId || !isWorkerSessionReadable(liveness)) {
      setRuntime(null);
      setOpenError(null);
      return;
    }
    let active = true;
    setOpenError(null);
    const currentRef = refHolder.current;
    void (async () => {
      const next = client.runtimeFor(currentRef, sessionId);
      setRuntime(next);
      await next.open(sessionId);
    })().catch((error) => {
      if (!active) return;
      setRuntime(null);
      setOpenError(
        error instanceof Error ? error.message : "Failed to open session"
      );
    });
    return () => {
      active = false;
    };
  }, [client, sessionId, livenessKey, key, openAttempt]);
  return { runtime, openError, retryOpen };
}
function WorkerConversation({
  workerRef,
  adapter: providedAdapter,
  onAdapterBridgeReady,
  presentation = "developer",
  mobile = false,
  mobileTerminalToolbar,
  locale = "en-US",
  className = "",
  clientLabel,
  contentRenderers,
  domainPanel,
  headerActions,
  renderModelControl,
  inspectorTabs,
  inspectorPanel,
  showFullscreen,
  toolRenderers,
  workspaceArtifacts,
  terminalRuntime,
  onReadyChange,
  defaultView,
  goal,
  onWake,
  workbenchMode,
  defaultWorkbenchMode,
  onWorkbenchModeChange,
  preferenceScope
}) {
  const client = useWorkerClient();
  const { liveness, retry: retryLiveness } = useWorkerLiveness(client, workerRef);
  const {
    sessionId,
    resolveError,
    resolveFailure,
    retry: retrySession
  } = useWorkerSessionId(client, workerRef, liveness);
  const { runtime, openError, retryOpen } = useWorkerRuntime(
    client,
    workerRef,
    sessionId,
    liveness
  );
  const transport = client.transportFor(workerRef);
  const adapter = useMemo(
    () => providedAdapter ?? (sessionId ? transport.adapterForSession?.(sessionId) : void 0),
    [providedAdapter, sessionId, transport]
  );
  const harness = adapter?.descriptor ?? (sessionId ? transport.harnessForSession?.(sessionId) : void 0);
  const conversationReady = Boolean(
    !resolveError && !openError && sessionId && runtime && liveness.state !== "starting" && liveness.state !== "unreachable"
  );
  const adapterBridge = useMemo(
    () => conversationReady && sessionId && runtime ? client.adapterBridgeFor(workerRef, sessionId, adapter) : void 0,
    [adapter, client, conversationReady, runtime, sessionId, workerRef]
  );
  const onReadyChangeRef = useRef(onReadyChange);
  useEffect(() => {
    onReadyChangeRef.current = onReadyChange;
  }, [onReadyChange]);
  useEffect(() => {
    onAdapterBridgeReady?.(adapterBridge);
    return () => onAdapterBridgeReady?.(void 0);
  }, [adapterBridge, onAdapterBridgeReady]);
  useEffect(() => {
    onReadyChangeRef.current?.(conversationReady);
  }, [conversationReady]);
  useEffect(() => {
    return () => onReadyChangeRef.current?.(false);
  }, []);
  if (resolveError) {
    return /* @__PURE__ */ jsx(
      WorkerLivenessView,
      {
        className,
        locale,
        liveness: projectAttachmentFailure(
          resolveFailure ?? "load-failed",
          resolveError
        ),
        onRetry: retrySession
      }
    );
  }
  if (openError) {
    return /* @__PURE__ */ jsx(
      WorkerLivenessView,
      {
        className,
        locale,
        liveness: projectAttachmentFailure("load-failed", openError),
        onRetry: retryOpen
      }
    );
  }
  if (liveness.state === "starting" || liveness.state === "unreachable") {
    return /* @__PURE__ */ jsx(
      WorkerLivenessView,
      {
        className,
        locale,
        liveness,
        onRetry: retryLiveness
      }
    );
  }
  if (!sessionId || !runtime) {
    return /* @__PURE__ */ jsx(
      WorkerLivenessView,
      {
        className,
        locale,
        liveness: { state: "starting", progress: null }
      }
    );
  }
  return /* @__PURE__ */ jsx(
    AgentWorkspace,
    {
      className,
      clientLabel,
      adapter,
      contentRenderers,
      domainPanel,
      defaultView,
      headerActions,
      renderModelControl,
      inspectorPanel,
      inspectorTabs,
      locale,
      goal,
      harness: harness ?? createAgentHarnessDescriptor({
        id: workerRef.transport,
        displayName: "Agent"
      }),
      presentation,
      mobile,
      mobileTerminalToolbar,
      readOnly: liveness.state === "online" && liveness.readOnly !== null,
      runtime,
      sessionId,
      sessionRecovery: workerComposerRecovery({
        readOnlyReason: liveness.state === "online" ? liveness.readOnly : null,
        onReconnect: () => {
          retryOpen();
          retryLiveness();
        },
        onWake
      }),
      showFullscreen,
      terminalRuntime,
      toolRenderers,
      codeWorkspace: transport.codeWorkspace,
      defaultWorkbenchMode,
      onWorkbenchModeChange,
      preferenceScope,
      workbenchMode,
      workspaceArtifacts,
      workspaceFiles: transport.workspaceFiles
    },
    sessionId
  );
}
export {
  AttachmentPreview as $,
  writeWorkerDirectoryState as A,
  useAgentWorkspaceText as B,
  previewPaneClass as C,
  focusAdjacentTab as D,
  WorkspaceFullscreenButton as E,
  useElementFullscreen as F,
  AgentWorkspaceLocaleProvider as G,
  SessionDeckHeaderContext as H,
  AgentWorkspace as I,
  toolRendererKeyId as J,
  contentRendererKeyId as K,
  userVideoExecutionSteps as L,
  UserVideoExecutionTrace as M,
  artifactActionAllowed as N,
  ArtifactError as O,
  isSafeArtifactImage as P,
  useArtifactBlobUrl as Q,
  ContentRendererRegistry as R,
  SessionAttachmentError as S,
  ToolRendererRegistry as T,
  UserTaskStatus as U,
  GenericArtifactCard as V,
  WorkerClient as W,
  ActivityTimeline as X,
  AgentTenantBrandingProvider as Y,
  ApprovalDock as Z,
  ArtifactCard as _,
  WorkerConversation as a,
  ComposerCapabilityBar as a0,
  ConversationComposer as a1,
  ConversationEmptyState as a2,
  ExecutionTrace as a3,
  GoalPanel as a4,
  MarkdownMessage as a5,
  PlanStrip as a6,
  ResultWorkbench as a7,
  STATIC_HTML_CSP as a8,
  STATIC_HTML_REFERRER_POLICY as a9,
  STATIC_HTML_SANDBOX as aa,
  TerminalMobileToolbar as ab,
  TerminalSurface as ac,
  WorkspaceHeader as ad,
  artifactPresentation as ae,
  goalWithInteractionEvidence as af,
  markdownImageSource as ag,
  mergeAgentAdapterRenderers as ah,
  openStaticHtmlInNewWindow as ai,
  resultScopeKey as aj,
  staticHtmlDocument as ak,
  useAgentTenantBranding as al,
  useWorkbenchContainerMode as am,
  userVideoFailureState as an,
  workbenchContainerMode as ao,
  WorkerLivenessView as b,
  WorkerProvider as c,
  buildReconnectCommand as d,
  createAgentAdapterBridge as e,
  createAgentHarnessDescriptor as f,
  defaultAgentHarnessCapabilities as g,
  defaultAgentHarnessPermissionCapabilities as h,
  defaultAgentHarnessWorkspaceCapabilities as i,
  emptyWorkerDirectoryState as j,
  isUnboundCodingFork as k,
  isWorkerSessionReadable as l,
  projectPodLiveness as m,
  reconcileWorkerSessionOrder as n,
  recoveryOptionsFor as o,
  projectAttachmentFailure as p,
  refKey as q,
  readWorkerDirectoryState as r,
  sessionAttachmentFailureOf as s,
  sessionReadOnlyReason as t,
  useRunnerOnlineEdge as u,
  useWorkerClient as v,
  useWorkerLiveness as w,
  useWorkerRuntime as x,
  useWorkerSessionId as y,
  workerDirectoryStorageKey as z
};
