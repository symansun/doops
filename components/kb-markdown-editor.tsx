"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import CodeMirror, { type ReactCodeMirrorRef, type Statistics } from "@uiw/react-codemirror"
import { markdown, markdownLanguage } from "@codemirror/lang-markdown"
import { languages } from "@codemirror/language-data"
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language"
import { EditorView, keymap } from "@codemirror/view"
import { redo, undo } from "@codemirror/commands"
import { openSearchPanel, search } from "@codemirror/search"
import { tags as t } from "@lezer/highlight"
import {
  Bold,
  ChevronDown,
  Code,
  FileCode2,
  GitBranch,
  Heading,
  Italic,
  Link2,
  List,
  ListChecks,
  ListOrdered,
  Minus,
  PlusSquare,
  Quote,
  Redo2,
  Search,
  Strikethrough,
  Table2,
  Undo2,
  WrapText,
} from "lucide-react"
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Separator,
} from "@/components/ui"
import { cn } from "@/lib/cn"

export interface EditorCursorStats {
  line: number
  col: number
  lines: number
  length: number
}

// CodeMirror 6 theme bound strictly to DoOps OKLCH CSS variables (no raw hex values).
const doopsEditorTheme = EditorView.theme(
  {
    "&": {
      height: "100%",
      backgroundColor: "var(--background)",
      color: "var(--foreground)",
      fontSize: "13.5px",
    },
    "&.cm-focused": {
      outline: "none",
    },
    ".cm-scroller": {
      fontFamily: "var(--font-mono)",
      lineHeight: "1.65",
      overflow: "auto",
    },
    ".cm-content": {
      padding: "14px 0",
      caretColor: "var(--primary)",
    },
    ".cm-line": {
      padding: "0 16px",
    },
    ".cm-gutters": {
      backgroundColor: "color-mix(in oklch, var(--card) 55%, var(--background))",
      color: "color-mix(in oklch, var(--muted-foreground) 65%, transparent)",
      borderRight: "1px solid color-mix(in oklch, var(--border) 65%, transparent)",
      userSelect: "none",
    },
    ".cm-activeLineGutter": {
      backgroundColor: "color-mix(in oklch, var(--muted) 55%, transparent)",
      color: "var(--foreground)",
    },
    ".cm-activeLine": {
      backgroundColor: "color-mix(in oklch, var(--muted) 32%, transparent)",
    },
    ".cm-cursor, .cm-dropCursor": {
      borderLeftColor: "var(--primary)",
      borderLeftWidth: "2px",
    },
    "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection":
      {
        backgroundColor: "color-mix(in oklch, var(--primary) 32%, transparent)",
      },
    ".cm-panels": {
      backgroundColor: "var(--card)",
      color: "var(--foreground)",
      borderBottom: "1px solid var(--border)",
    },
    ".cm-search": {
      fontSize: "12px",
      padding: "6px 10px",
    },
    ".cm-textfield": {
      backgroundColor: "var(--background)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      color: "var(--foreground)",
      padding: "2px 6px",
    },
    ".cm-button": {
      backgroundImage: "none",
      backgroundColor: "var(--muted)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-sm)",
      color: "var(--foreground)",
      padding: "2px 8px",
      cursor: "pointer",
    },
    ".cm-searchMatch": {
      backgroundColor: "color-mix(in oklch, var(--warning) 30%, transparent)",
    },
    ".cm-searchMatch.cm-searchMatch-selected": {
      backgroundColor: "color-mix(in oklch, var(--primary) 45%, transparent)",
    },
    ".cm-placeholder": {
      color: "var(--muted-foreground)",
    },
  },
  { dark: true },
)

const doopsHighlightStyle = HighlightStyle.define([
  { tag: [t.heading1], color: "var(--foreground)", fontWeight: "700", fontSize: "1.22em" },
  { tag: [t.heading2], color: "var(--foreground)", fontWeight: "700", fontSize: "1.1em" },
  { tag: [t.heading3, t.heading4, t.heading5, t.heading6], color: "var(--foreground)", fontWeight: "600" },
  { tag: [t.strong], color: "var(--foreground)", fontWeight: "700" },
  { tag: [t.emphasis], fontStyle: "italic" },
  { tag: [t.strikethrough], textDecoration: "line-through", color: "var(--muted-foreground)" },
  { tag: [t.link, t.url], color: "color-mix(in oklch, var(--primary) 72%, var(--foreground))", textDecoration: "underline" },
  { tag: [t.monospace], color: "color-mix(in oklch, var(--accent) 72%, var(--foreground))" },
  { tag: [t.quote], color: "var(--muted-foreground)", fontStyle: "italic" },
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: "color-mix(in oklch, var(--primary) 62%, var(--foreground))", fontWeight: "500" },
  { tag: [t.string, t.special(t.string), t.inserted], color: "color-mix(in oklch, var(--success) 75%, var(--foreground))" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "color-mix(in oklch, var(--warning) 80%, var(--foreground))" },
  { tag: [t.propertyName, t.attributeName, t.typeName, t.className], color: "color-mix(in oklch, var(--accent) 75%, var(--foreground))" },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: "color-mix(in oklch, var(--accent) 52%, var(--foreground))" },
  { tag: [t.deleted], color: "color-mix(in oklch, var(--destructive) 76%, var(--foreground))" },
  { tag: [t.comment, t.lineComment, t.blockComment, t.meta], color: "var(--muted-foreground)", fontStyle: "italic" },
  { tag: [t.punctuation, t.processingInstruction], color: "var(--muted-foreground)" },
])

function wrapInline(view: EditorView, marker: string, fallback: string): boolean {
  const { from, to } = view.state.selection.main
  const selected = view.state.sliceDoc(from, to)
  const mLen = marker.length

  if (
    from >= mLen &&
    view.state.sliceDoc(from - mLen, from) === marker &&
    view.state.sliceDoc(to, to + mLen) === marker
  ) {
    view.dispatch({
      changes: [
        { from: from - mLen, to: from, insert: "" },
        { from: to, to: to + mLen, insert: "" },
      ],
      selection: { anchor: from - mLen, head: to - mLen },
    })
    view.focus()
    return true
  }

  const inner = selected || fallback
  const replacement = `${marker}${inner}${marker}`
  view.dispatch({
    changes: { from, to, insert: replacement },
    selection: { anchor: from + mLen, head: from + mLen + inner.length },
  })
  view.focus()
  return true
}

function insertLink(view: EditorView): boolean {
  const { from, to } = view.state.selection.main
  const selected = view.state.sliceDoc(from, to) || "链接说明"
  const url = "https://"
  const insert = `[${selected}](${url})`
  const urlStart = from + selected.length + 3
  view.dispatch({
    changes: { from, to, insert },
    selection: { anchor: urlStart, head: urlStart + url.length },
  })
  view.focus()
  return true
}

function toggleLinePrefix(view: EditorView, prefix: string): boolean {
  const { state } = view
  const line = state.doc.lineAt(state.selection.main.from)
  const existingHeading = /^#{1,6}\s+/.exec(line.text)

  if (line.text.startsWith(prefix)) {
    view.dispatch({
      changes: { from: line.from, to: line.from + prefix.length, insert: "" },
    })
  } else if (prefix.startsWith("#") && existingHeading) {
    view.dispatch({
      changes: { from: line.from, to: line.from + existingHeading[0].length, insert: prefix },
    })
  } else {
    view.dispatch({
      changes: { from: line.from, to: line.from, insert: prefix },
      selection: { anchor: line.from + prefix.length + line.length },
    })
  }
  view.focus()
  return true
}

function insertBlockSnippet(view: EditorView, snippet: string, cursorOffsetFromStart?: number) {
  const { state } = view
  const { from, to } = state.selection.main
  const line = state.doc.lineAt(from)
  const needLeadingNewline = from > 0 && line.text.trim().length > 0
  const prefix = needLeadingNewline ? "\n\n" : ""
  const full = `${prefix}${snippet}\n`
  const anchor =
    cursorOffsetFromStart !== undefined ? from + prefix.length + cursorOffsetFromStart : from + full.length
  view.dispatch({
    changes: { from, to, insert: full },
    selection: { anchor },
  })
  view.focus()
}

const CODE_SNIPPETS: ReadonlyArray<{ label: string; lang: string; body: string }> = [
  { label: "Shell / Bash 命令", lang: "bash", body: "kubectl get pods -A" },
  { label: "SQL 查询语句", lang: "sql", body: "SELECT id, created_at\nFROM events\nORDER BY created_at DESC\nLIMIT 20;" },
  { label: "YAML 配置清单", lang: "yaml", body: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app-config" },
  { label: "JSON 数据结构", lang: "json", body: '{\n  "status": "ok"\n}' },
  { label: "Diff 变更对比", lang: "diff", body: "@@ -1,3 +1,3 @@\n-replicaCount: 1\n+replicaCount: 3" },
]

const CALLOUT_SNIPPETS: ReadonlyArray<{ label: string; kind: string; body: string }> = [
  { label: "提示 [!NOTE]", kind: "NOTE", body: "在此填写背景说明或默认约定。" },
  { label: "建议 [!TIP]", kind: "TIP", body: "在此填写推荐操作方式。" },
  { label: "重要 [!IMPORTANT]", kind: "IMPORTANT", body: "在此填写关键前置条件。" },
  { label: "注意 [!WARNING]", kind: "WARNING", body: "执行前请先核对目标环境与备份状态。" },
  { label: "高危 [!CAUTION]", kind: "CAUTION", body: "该操作具有破坏性，请确认影响范围。" },
]

const MERMAID_FLOW_SNIPPET = `\`\`\`mermaid
flowchart LR
  User[运维工程师] --> GW[DoOps Gateway]
  GW --> Agent[Target Agent]
  Agent --> K8s[(Kubernetes / Host)]
\`\`\``

const MERMAID_SEQ_SNIPPET = `\`\`\`mermaid
sequenceDiagram
  participant Op as 控制台
  participant GW as Gateway
  participant Ag as Agent
  Op->>GW: 提交受控操作
  GW->>Ag: 鉴权并转发指令
  Ag-->>GW: 返回执行证据
  GW-->>Op: 记录审计并展示结果
\`\`\``

const TABLE_SNIPPET = `| 检查项 | 期望标准 | 当前状态 |
| :--- | :--- | :---: |
| Pod 就绪状态 | Running / Ready | 待核查 |
| 错误日志巡检 | 0 异常告警 | 待核查 |`

export function KbMarkdownEditor({
  value,
  disabled,
  autoFocus,
  onChange,
  onSave,
  onStatsChange,
}: {
  value: string
  disabled?: boolean
  autoFocus?: boolean
  onChange: (next: string) => void
  onSave: () => void
  onStatsChange?: (stats: EditorCursorStats) => void
}) {
  const cmRef = useRef<ReactCodeMirrorRef>(null)
  const [lineWrap, setLineWrap] = useState(true)
  const onSaveRef = useRef(onSave)
  onSaveRef.current = onSave

  const withView = useCallback((fn: (view: EditorView) => void) => {
    const view = cmRef.current?.view
    if (view) fn(view)
  }, [])

  const shortcutExtension = useMemo(
    () =>
      keymap.of([
        {
          key: "Mod-s",
          run: (view) => {
            if (view.composing) return false
            onSaveRef.current()
            return true
          },
        },
        {
          key: "Mod-b",
          run: (view) => (view.composing ? false : wrapInline(view, "**", "粗体文本")),
        },
        {
          key: "Mod-i",
          run: (view) => (view.composing ? false : wrapInline(view, "*", "斜体文本")),
        },
        {
          key: "Mod-e",
          run: (view) => (view.composing ? false : wrapInline(view, "`", "code")),
        },
        {
          key: "Mod-k",
          run: (view) => (view.composing ? false : insertLink(view)),
        },
      ]),
    [],
  )

  const extensions = useMemo(
    () => [
      doopsEditorTheme,
      syntaxHighlighting(doopsHighlightStyle),
      search({ top: true }),
      markdown({ base: markdownLanguage, codeLanguages: languages }),
      shortcutExtension,
      ...(lineWrap ? [EditorView.lineWrapping] : []),
    ],
    [lineWrap, shortcutExtension],
  )

  const handleStats = useCallback(
    (data: Statistics) => {
      onStatsChange?.({
        line: data.line.number,
        col: data.selectionAsSingle.from - data.line.from + 1,
        lines: data.lineCount,
        length: data.length,
      })
    },
    [onStatsChange],
  )

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
      {/* 编辑器格式与块插入工具栏 */}
      <div
        role="toolbar"
        aria-label="Markdown 格式工具栏"
        className="flex shrink-0 flex-wrap items-center gap-0.5 border-b bg-card/55 px-2 py-1"
      >
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="撤销 (⌘Z)"
          aria-label="撤销"
          onClick={() => withView((v) => undo(v))}
        >
          <Undo2 aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="重做 (⇧⌘Z)"
          aria-label="重做"
          onClick={() => withView((v) => redo(v))}
        >
          <Redo2 aria-hidden="true" className="size-3.5" />
        </Button>

        <Separator orientation="vertical" className="mx-1 h-4" />

        {/* 标题下拉 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={disabled}
              title="插入标题"
              aria-label="插入标题"
              className="gap-1 px-2"
            >
              <Heading aria-hidden="true" className="size-3.5" />
              <ChevronDown aria-hidden="true" className="size-3 opacity-70" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-40">
            <DropdownMenuLabel>章节标题</DropdownMenuLabel>
            <DropdownMenuItem onSelect={() => withView((v) => toggleLinePrefix(v, "# "))}>
              <span className="font-mono text-xs text-muted-foreground">H1</span> 一级标题
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => withView((v) => toggleLinePrefix(v, "## "))}>
              <span className="font-mono text-xs text-muted-foreground">H2</span> 二级标题
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => withView((v) => toggleLinePrefix(v, "### "))}>
              <span className="font-mono text-xs text-muted-foreground">H3</span> 三级标题
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => withView((v) => toggleLinePrefix(v, "#### "))}>
              <span className="font-mono text-xs text-muted-foreground">H4</span> 四级标题
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="粗体 (⌘B)"
          aria-label="粗体"
          onClick={() => withView((v) => wrapInline(v, "**", "粗体文本"))}
        >
          <Bold aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="斜体 (⌘I)"
          aria-label="斜体"
          onClick={() => withView((v) => wrapInline(v, "*", "斜体文本"))}
        >
          <Italic aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="删除线"
          aria-label="删除线"
          onClick={() => withView((v) => wrapInline(v, "~~", "已废弃内容"))}
        >
          <Strikethrough aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="行内代码 (⌘E)"
          aria-label="行内代码"
          onClick={() => withView((v) => wrapInline(v, "`", "code"))}
        >
          <Code aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="插入链接 (⌘K)"
          aria-label="插入链接"
          onClick={() => withView((v) => insertLink(v))}
        >
          <Link2 aria-hidden="true" className="size-3.5" />
        </Button>

        <Separator orientation="vertical" className="mx-1 h-4" />

        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="无序列表"
          aria-label="无序列表"
          onClick={() => withView((v) => toggleLinePrefix(v, "- "))}
        >
          <List aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="有序列表"
          aria-label="有序列表"
          onClick={() => withView((v) => toggleLinePrefix(v, "1. "))}
        >
          <ListOrdered aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="检查清单"
          aria-label="检查清单"
          onClick={() => withView((v) => toggleLinePrefix(v, "- [ ] "))}
        >
          <ListChecks aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="引用块"
          aria-label="引用块"
          onClick={() => withView((v) => toggleLinePrefix(v, "> "))}
        >
          <Quote aria-hidden="true" className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon
          disabled={disabled}
          title="插入表格"
          aria-label="插入表格"
          onClick={() => withView((v) => insertBlockSnippet(v, TABLE_SNIPPET))}
        >
          <Table2 aria-hidden="true" className="size-3.5" />
        </Button>

        {/* 运维代码块、Mermaid 与提示块快捷菜单 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={disabled}
              title="插入代码块、架构图或提示块"
              className="gap-1 px-2"
            >
              <PlusSquare aria-hidden="true" className="size-3.5" />
              <span className="hidden xl:inline">插入块</span>
              <ChevronDown aria-hidden="true" className="size-3 opacity-70" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-52">
            <DropdownMenuLabel>代码块 (Shiki 高亮)</DropdownMenuLabel>
            <DropdownMenuGroup>
              {CODE_SNIPPETS.map((item) => (
                <DropdownMenuItem
                  key={item.lang}
                  onSelect={() =>
                    withView((v) =>
                      insertBlockSnippet(v, `\`\`\`${item.lang}\n${item.body}\n\`\`\``, item.lang.length + 4),
                    )
                  }
                >
                  <FileCode2 aria-hidden="true" className="size-3.5 text-muted-foreground" />
                  <span>{item.label}</span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>

            <DropdownMenuSeparator />
            <DropdownMenuLabel>Mermaid 图表</DropdownMenuLabel>
            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={() => withView((v) => insertBlockSnippet(v, MERMAID_FLOW_SNIPPET))}>
                <GitBranch aria-hidden="true" className="size-3.5 text-muted-foreground" />
                <span>流程图 / 拓扑图</span>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => withView((v) => insertBlockSnippet(v, MERMAID_SEQ_SNIPPET))}>
                <GitBranch aria-hidden="true" className="size-3.5 text-muted-foreground" />
                <span>调用时序图</span>
              </DropdownMenuItem>
            </DropdownMenuGroup>

            <DropdownMenuSeparator />
            <DropdownMenuLabel>运维提示块 (Callout)</DropdownMenuLabel>
            <DropdownMenuGroup>
              {CALLOUT_SNIPPETS.map((item) => (
                <DropdownMenuItem
                  key={item.kind}
                  onSelect={() => withView((v) => insertBlockSnippet(v, `> [!${item.kind}]\n> ${item.body}`))}
                >
                  <span>{item.label}</span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>

            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => withView((v) => insertBlockSnippet(v, "---"))}>
              <Minus aria-hidden="true" className="size-3.5 text-muted-foreground" />
              <span>水平分割线</span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <div className="ml-auto flex items-center gap-0.5">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            icon
            disabled={disabled}
            title="查找与替换 (⌘F)"
            aria-label="查找与替换"
            onClick={() => withView((v) => openSearchPanel(v))}
          >
            <Search aria-hidden="true" className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            icon
            aria-pressed={lineWrap}
            title={lineWrap ? "关闭自动换行" : "开启自动换行"}
            aria-label="切换自动换行"
            className={cn(lineWrap && "bg-primary/15 text-primary")}
            onClick={() => setLineWrap((v) => !v)}
          >
            <WrapText aria-hidden="true" className="size-3.5" />
          </Button>
        </div>
      </div>

      {/* CodeMirror 6 主编辑画布 */}
      <div className="min-h-0 flex-1 overflow-hidden [&_.cm-theme]:h-full">
        <CodeMirror
          ref={cmRef}
          value={value}
          height="100%"
          theme="none"
          autoFocus={autoFocus}
          editable={!disabled}
          readOnly={disabled}
          placeholder="输入 Markdown 知识库正文，支持代码块语法高亮、Mermaid 图表、LaTeX 公式、GFM 表格与提示块…"
          basicSetup={{
            lineNumbers: true,
            foldGutter: true,
            highlightActiveLine: true,
            highlightActiveLineGutter: true,
            bracketMatching: true,
            closeBrackets: true,
            autocompletion: false,
            searchKeymap: true,
            history: true,
            indentOnInput: true,
          }}
          extensions={extensions}
          onChange={onChange}
          onStatistics={handleStats}
          className="h-full"
        />
      </div>
    </div>
  )
}
