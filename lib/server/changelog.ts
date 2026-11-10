// The console image is built from one repository revision, so the changelog
// that revision carries is exactly the release log the console should show.
// Reading it while the page renders keeps the dialog a build artifact: no
// Gateway call, no network feed, and nothing to fetch at runtime.
//
// The file is optional. A checkout without CHANGELOG.md (or a vendor build
// that strips it) still renders the console; the dialog has no notes and its
// entry stays disabled instead of failing the page.

import { readFile } from "node:fs/promises"
import { join } from "node:path"

import { parseChangelog, type ChangelogRelease } from "../changelog.ts"

export const CHANGELOG_FILENAME = "CHANGELOG.md"

export async function loadChangelogMarkdown(root: string = process.cwd()): Promise<string | null> {
  try {
    return await readFile(join(root, CHANGELOG_FILENAME), "utf8")
  } catch {
    return null
  }
}

export async function loadChangelogReleases(root: string = process.cwd()): Promise<ChangelogRelease[]> {
  const markdown = await loadChangelogMarkdown(root)
  if (!markdown) return []
  return parseChangelog(markdown).releases
}
