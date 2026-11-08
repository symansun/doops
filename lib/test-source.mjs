import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { after } from "node:test"
import { pathToFileURL } from "node:url"
import TypeScript from "typescript"

// Compile real local sources with the same module resolution used by the route tests.
export function sourceLoader(root) {
  const fixture = mkdtempSync(join(tmpdir(), "doops-source-test-"))
  symlinkSync(join(root, "node_modules"), join(fixture, "node_modules"), "dir")
  const compiled = new Map()
  after(() => rmSync(fixture, { recursive: true, force: true }))
  function sourceModule(filename) {
    filename = resolve(root, filename)
    if (compiled.has(filename)) return compiled.get(filename)
    const output = join(fixture, `${compiled.size}.mjs`)
    const url = pathToFileURL(output).href
    compiled.set(filename, url)
    let javascript = TypeScript.transpile(readFileSync(filename, "utf8"), {
      module: TypeScript.ModuleKind.ESNext,
      target: TypeScript.ScriptTarget.ES2022,
    }, filename)
    const parsed = TypeScript.createSourceFile(filename, javascript, TypeScript.ScriptTarget.ES2022, true, TypeScript.ScriptKind.JS)
    for (const statement of parsed.statements.filter((statement) =>
      (TypeScript.isImportDeclaration(statement) || TypeScript.isExportDeclaration(statement)) && statement.moduleSpecifier,
    ).reverse()) {
      const specifier = statement.moduleSpecifier
      const name = specifier.text
      if (!name.startsWith(".") && !name.startsWith("@/")) continue
      const source = name.startsWith("@/") ? join(root, name.slice(2)) : name
      const resolved = TypeScript.resolveModuleName(source, filename, {
        moduleResolution: TypeScript.ModuleResolutionKind.Bundler,
        module: TypeScript.ModuleKind.ESNext,
      }, TypeScript.sys).resolvedModule
      assert.ok(resolved && resolved.resolvedFileName.startsWith(root) && resolved.resolvedFileName.endsWith(".ts"),
        `Missing DoOps TypeScript source: ${name} from ${filename}`)
      javascript = javascript.slice(0, specifier.getStart(parsed)) +
        JSON.stringify(sourceModule(resolved.resolvedFileName)) + javascript.slice(specifier.end)
    }
    writeFileSync(output, javascript)
    return url
  }
  return (filename) => import(sourceModule(filename))
}
