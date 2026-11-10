import { ConsoleShell } from "@/components/console-shell"
import { loadChangelogReleases } from "@/lib/server/changelog"

// The release log is read while the page is built (see lib/server/changelog.ts)
// and handed to the shell as plain data, so the "更新内容" dialog never fetches
// it and a Gateway outage cannot break it.
export default async function ConsolePage() {
  const releases = await loadChangelogReleases()
  return <ConsoleShell changelog={releases} />
}
