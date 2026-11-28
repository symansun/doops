import type { ComponentType } from "react"
import {
  Activity,
  ArrowUpCircle,
  Box,
  Boxes,
  CheckCircle2,
  ClipboardCheck,
  Cpu,
  Database,
  Eye,
  Flame,
  Gauge,
  Globe2,
  HardDrive,
  Layers,
  Lock,
  Network,
  PackagePlus,
  Plug,
  Puzzle,
  Radar,
  Server,
  Shield,
  Sparkles,
  Terminal,
  Workflow,
  Wrench,
  Zap,
} from "lucide-react"
import type { AssetArea } from "@/lib/console-views"
import { ActivityIcon, FileIcon, FileTextIcon, HistoryIcon, KeyIcon, RocketIcon, SparkIcon, TerminalIcon } from "@/components/icons"

export type NavIconComponent = ComponentType<{ width?: number; height?: number; className?: string; "aria-hidden"?: boolean | "true" | "false" }>

// Whitelisted static icon registry shared by ConsoleNav and L1/L2 plugins.
// Prevents arbitrary SVG/HTML icon injection from declarative manifests.
export const CONSOLE_NAV_ICONS: Record<string, NavIconComponent> = {
  home: ActivityIcon,
  overview: ActivityIcon,
  ask: SparkIcon,
  terminal: TerminalIcon,
  tasks: Workflow,
  database: Database,
  website: Globe2,
  k8s: Boxes,
  image: Boxes,
  files: FileIcon,
  assets: Boxes,
  monitor: ActivityIcon,
  credentials: KeyIcon,
  audit: HistoryIcon,
  kb: FileTextIcon,
  "situational-awareness": Radar,
  "resource-monitor": Gauge,
  "smart-inspection": ClipboardCheck,
  "plugin-center": Puzzle,
  "skill-registry": Sparkles,
  "mcp-registry": Plug,
  "platform-monitor": ActivityIcon,
  "ledger-machines": Server,
  vm: Server,
  "ledger-systems": Layers,
  "deploy-workload": RocketIcon,
  "deploy-upgrade": ArrowUpCircle,
  "deploy-onboard": PackagePlus,
  "deploy-cicd": TerminalIcon,
  "platform-admin": Shield,
  // Complete 20-icon whitelist for declarative L2 plugin.yaml manifests (PLG-02).
  cpu: Cpu,
  shield: Shield,
  globe: Globe2,
  activity: Activity,
  server: Server,
  lock: Lock,
  layers: Layers,
  sparkles: Sparkles,
  gauge: Gauge,
  radar: Radar,
  "hard-drive": HardDrive,
  network: Network,
  box: Box,
  flame: Flame,
  eye: Eye,
  wrench: Wrench,
  zap: Zap,
  "check-circle": CheckCircle2,
  puzzle: Puzzle,
  "terminal-icon": Terminal,
}

// Explicit L1 plugin UI registry IDs checked by lib/plugins/plugins.test.mjs
// against lib/plugins/builtin-catalog.json so Go catalog and UI never drift.
export const REGISTERED_BUILTIN_PLUGIN_IDS = [
  "doops.inspection",
  "doops.k8s",
  "doops.delivery",
  "doops.database",
  "doops.website",
  "doops.image",
  "doops.cmdb",
  "doops.automation",
] as const

export function isPluginCenterArea(area: AssetArea): boolean {
  return area === "plugins" || area === "skills" || area === "mcp"
}
