---
name: doops-console-design
scope: web-console
owner: doops-maintainers
updated: 2026-11-28
---

# Design System: DoOps Console & Portal (`doops-console`)

面向运维工程师、SRE 与平台管理员的统一智能运维设计系统，基于 **标准 shadcn/ui (`components.json`) + Radix UI + Tailwind CSS v4 + TanStack Table v8** 构建。以「**选定目标 -> 核查资产/指标 -> 受控执行/发布 -> 追溯审计回执**」为核心主路径，追求高信噪比、明确权限边界与可验证的操作反馈。

所有视觉令牌的唯一权威来源是 `app/globals.css`（`app/console/console.css` 仅保留 `@agent-cloud/agent-ui` 第三方组件作用域适配，禁止写业务样式或硬编码 hex）；全部基础原语、布局外壳与数据表格统一由 `@/components/ui`（`components/ui.tsx` 及 `components/ui/*`）导出。

---

## 1. Visual Theme & Atmosphere（视觉基调与界面氛围）

DoOps 采用**深色优先（Dark-first, `color-scheme: dark`）**的工业级运维美学，严格区分两类表面：

1. **生产运维工作台（`/console`、`/console/admin`）—— 高密度、高确定性、零装饰噪音**
   - 铺满视口（`100dvh` 全宽），以发丝级微亮边框、克制的深海军蓝层级对比和清晰的状态色区分信息模块。
   - **禁止**在运维、台账、审计、平台管理页面使用营销式大留白 Hero、紫粉渐变卡片、毛玻璃堆叠或无意义的装饰动效。
   - 记忆点来自工作流清晰度：当前目标身份、会话 ID、在线状态、执行进度、危险动作边界与审计证据一目了然。
2. **公开产品门户与文档（`/`、`/cases`、`/blog`、`/docs`）—— 克制科技感与统一栅格标尺**
   - 全站共享统一框架容器组件 `<PortalContainer>`（`max-w-7xl px-4 sm:px-6 lg:px-8`，对应 `80rem` 外轨与标准 `rem` 响应内距），页头（`SiteHeader`）、首页全部区块、案例、文档侧栏+正文+目录、博客与页脚严格齐平；移动端导航与文档目录统一使用 Radix `<Sheet>` 抽屉；长文阅读正文在主轨内收口至舒适行宽并配右侧目录，磨砂吸顶导航栏搭配低不透明度品牌蓝青环境光（`.hero-ambient`）与胶囊形主行动按钮（`<Button size="md" | "lg">`）。

---

## 2. Color Palette & Roles（色彩体系与语义角色）

主题采用 OKLCH 感知均匀色彩空间定义，反馈底色统一通过 `color-mix(in oklch, ...)` 从主语义色派生。**严禁在业务组件中硬编码十六进制色值或直接使用 Tailwind 裸色阶（如 `amber-500`、`emerald-600`、`red-500`、`slate-800`）。**

### 2.1 核心表面与品牌色

| 语义名称 | CSS 变量 | OKLCH 定义（sRGB 近似值） | 功能角色与使用边界 |
|---|---|---|---|
| **Deep Midnight Navy** | `--background` | `oklch(0.16 0.02 262)` (`#090d16`) | 全局页面底色、输入框底色、终端/代码阅读底色 |
| **Elevated Slate Navy** | `--card` / `--popover` | `oklch(0.2 0.02 262)` (`#11161f`) | 内容卡片、表格吸顶表头、模态弹窗、下拉菜单表面 |
| **Recessed Steel Surface** | `--muted` / `--secondary` | `oklch(0.25 0.016 262)` (`#1d2229`) / `oklch(0.27 0.018 262)` (`#22272f`) | 次级块底色、行悬停反馈、内嵌代码底、分段器未选悬停态 |
| **Crisp Ice Foreground** | `--foreground` | `oklch(0.92 0.006 252)` (`#e2e5e8`) | 一级标题、正文、表单输入文本、关键数值 |
| **Cool Slate Muted** | `--muted-foreground` | `oklch(0.72 0.012 252)` (`#9fa5ac`) | 辅助说明、表头标签、时间戳、占位提示符（保持 100% 不透明度可读） |
| **DoOps Brand Blue** | `--primary` | `oklch(0.58 0.22 266)` (`#3c6afa`) | 主操作按钮、当前页签指示条、主链接、复选框强调色 |
| **Brand Signal Teal** | `--accent` / `--brand-teal` | `oklch(0.78 0.14 194)` (`#00d3d1`) | 活跃态点缀、与 `--brand-blue` (`#2c61fe`) 组成品牌渐变 `--brand-gradient` |
| **Hairline Slate Border** | `--border` / `--input` | `oklch(0.3 0.018 262)` (`#292e37`) | 卡片分割线、表格行线、表单描边 |
| **Focus Halo Blue** | `--ring` | `oklch(0.7 0.16 266)` (`#6f99ff`) | 键盘 `:focus-visible` 焦点环与输入聚焦边框 |
| **Opaque Rail Navy** | `--sidebar` / `--sidebar-primary` | `oklch(0.21 0.032 256)` / `oklch(0.42 0.115 248)` | 工作台左侧不透明导航轨（`<Sidebar>` 与移动端 `<Sheet>`），以标准 shadcn/ui 侧栏 OKLCH 令牌保障高对比度 |

### 2.2 运维状态语义色（不得仅靠颜色表达状态，须配合文案或图标）

| 状态语义 | CSS 变量 / 组件变体 | OKLCH 定义（sRGB 近似值） | 适用场景 |
|---|---|---|---|
| **Success（正常/在线/已激活）** | `--success` / `--success-bg` | `oklch(0.72 0.15 150)` (`#53be70`) | Agent 在线空闲、凭据 `active`、校验通过、保存/发布成功回执 |
| **Warning（忙碌/运行中/待确认）** | `--warning` / `--warning-bg` | `oklch(0.78 0.13 75)` (`#e8aa4e`) | Agent 忙碌、升级观测中、离线横幅、`execution_outcome_unknown` 结果未知提示（不得标为错误红） |
| **Danger / Destructive（失败/阻断/吊销）** | `--destructive` / `--danger-bg` | `oklch(0.62 0.2 22)` (`#e6424c`) | 权限拒绝、执行报错、高危命令告警、删除/吊销/强制停止等破坏性操作 |

---

## 3. Typography Rules（字体与排版层级）

- **字体族**：
  - 无衬线正文 `--font-sans`: `"Geist", ui-sans-serif, system-ui, sans-serif`（开启 `"rlig" 1, "calt" 1` 与抗锯齿渲染）。
  - 等宽技术字体 `--font-mono`: `"Geist Mono", ui-monospace, SFMono-Regular, monospace`。
- **等宽铁律（Monospace Rule）**：凡涉及 `cluster/instance` 目标标识、`session_id`、资产编号（`asset_id`）、审计/操作 ID、镜像 Tag 与 SHA256 Digest、文件路径、IP/端口、环境变量键名、SQL 语句与 CLI 命令，**一律使用 `font-mono`**，防止字符混淆并保证列对齐。
- **字号与行高标尺**：
  - 完整保留 **Tailwind CSS v4 标准 `rem` 字号标尺**（不覆写 `--text-xs` / `--text-sm` / `--text-base`），尊重浏览器根字号。仅在 `@theme` 补充唯一命名小字令牌 `--text-2xs: 0.6875rem`（11px / 1rem）用于状态徽章与快捷键提示。**严禁在任何组件中书写 `text-[10px]`、`text-[11px]` 等固定像素字号或任意 `[...px]` 尺寸类。**
  - **标题**：管理页主标题 `text-xl`（`1.25rem`），区块标题 `text-base`（`1rem`），主要状态提示 `text-lg`（`1.125rem`）；公开门户保留响应式大标题。
  - **正文和控件**：`text-sm`（`0.875rem`）用于控制台常规正文、表单输入、表格单元格与导航项；长文阅读与对话正文使用 `text-base`（`1rem`）。
  - **辅助文字**：`text-xs`（`0.75rem`）用于表头、分组标题、说明与紧凑按钮；`text-2xs`（`0.6875rem`）仅用于徽章、编号、快捷键等紧凑标签。

---

## 4. Layout, Spacing & Geometry（布局、间距与几何圆角）

### 4.1 布局骨架
- **控制台外壳（`ConsoleShell`）**：`h-dvh w-full flex-col overflow-hidden`，外层不设最大宽度限制。
  - **顶栏（Header）**：固定高紧凑工具条（品牌标志高 `30px`），左侧为移动端菜单 `<Button>` + DoOps 标识，右侧收敛 `TargetSwitcher`、当前用户名、博客、更新内容（带未读圆点）、帮助与断开按钮，在 `390px` 移动端单行不溢出。
  - **左侧导航栏（`ConsoleNav`）**：`≥768px` 渲染 shadcn `<Sidebar>`（`w-52 lg:w-56`），`<768px` 收起为 Radix `<Sheet side="left">` 侧滑抽屉（`id="console-nav-drawer"`，自带 Esc、遮罩关闭、焦点锁定与滚动锁定）。
  - **主工作区（Workspace）**：顶层固定承载离线横幅、中断命令「结果未知」横幅、错误提示与资产回跳上下文条；下方区域根据面板特性使用内部滚动或分栏滚动，禁止整页溢出裁切。
- **布局与数据原语优先**：在业务页面中优先组合 `PageHeader`、`PageSection`、`StatCard`、`FilterBar`、`EmptyState`、`PortalContainer`、`DataTable`（TanStack Table v8）、`Stack`、`Inline`、`Grid`、`Container` 与 `ResizablePanelGroup`，避免散落手写复杂栅格字符串或像素列宽。
- **间距阶梯**：以 `4px` 为基本单位：紧凑内联间距 `4px–8px`（`gap-1` / `gap-2`），表单与卡片内距 `12px–16px`（`p-3` / `p-4`），主要面板区块间距 `16px–24px`（`gap-4` / `gap-6`）。

### 4.2 圆角与深度层级
- **基础圆角 `--radius`**：`0.625rem` (`10px`)。
  - **交互控件**（按钮、输入框、下拉框、分段容器）：`--radius-md` (`8px`)，分段子项与徽章使用 `--radius-sm` (`6px`)。
  - **数据卡片与常规面板**（`Card`）：`rounded-lg` (`10px`) + `1px solid var(--border)`。
  - **模态弹窗与重点高光面板**（`DialogContent` / `.card-surface`）：`rounded-xl` (`14px`) 或 `1rem` (`16px`)，配合微弱顶部内高光与柔和投影区分浮层，不在卡片内部套叠多层阴影卡片。
  - **胶囊圆角**（`rounded-full`）：仅用于门户页大号行动按钮（`.btn-md` / `.btn-lg`）、进度条轨道与在线状态指示圆点。
- **滚动条预留**：`html { overflow-y: scroll; }` 常驻保留纵向滚动槽，防止路由切换时内容水平跳动。

---

## 5. Component Stylings & Unified Primitives（统一组件规范）

**工程铁律**：所有业务页面统一从 `@/components/ui` 导入共用控件、容器与浮层原语，**禁止在业务组件中手写裸 `<button>` / `<input>` / `<textarea>` / `<input type="checkbox">` 搭配长串 Tailwind 类名重复造轮子，也禁止手写自定义模态遮罩层**（文件树节点、表格内联排序/跳转链接、终端专用无边框编辑器等语义化交互除外）。

### 5.1 操作与表单控件

| 组件 | 变体与属性 | 桌面尺寸与视觉规范 |
|---|---|---|
| **`Button`** | 基于 CVA + `@radix-ui/react-slot` (`asChild`)；`variant`: `default` \| `primary` \| `secondary` \| `outline` \| `danger` \| `danger-outline` \| `ghost` \| `link`；`size`: `default` \| `sm` \| `md` \| `lg` \| `icon` \| `icon-sm` | 标准 `min-h-9`（`text-sm`），紧凑 `size="sm"` 为 `min-h-8`（`text-xs`），门户胶囊 `md`/`lg`；支持 `asChild` 包装 `<Link>` / `<a>`，严禁在业务组件直接手写 `.btn` CSS 类。 |
| **`Input` / `Textarea` / `Label`** | 基于 CVA + `@radix-ui/react-label`；`scale`: `default` \| `sm`；支持 `aria-invalid` | 标准 `min-h-9`（`sm` 为 `min-h-8`），深底 `--background` + `1px` 描边，聚焦 `--ring`，校验失败 `--destructive`。 |
| **`Select`** | 基于 `@radix-ui/react-select`（同时导出 `SelectRoot`/`SelectTrigger`/`SelectContent`/`SelectItem` 与兼容原生 `<option>` + `FormData` / `ConsoleFormAssist` 的 `<Select>` 适配器） | 碰撞感知弹出、键盘漫游、类型检索与无障碍全权交给 Radix Popper，彻底移除手算像素定位。 |
| **`Checkbox` / `Switch`** | `Checkbox`（表单兼容）+ `RadixCheckbox`（`@radix-ui/react-checkbox`）+ `Switch`（`@radix-ui/react-switch`） | 统一 `size-4` / `h-5 w-9` 比例尺寸、焦点环与禁用表现。 |
| **`Table` / `DataTable`** | `Table*` 基础语义表 + `@tanstack/react-table` v8 驱动的 `DataTable` / `DataTablePagination` / `DataTableColumnHeader` | 全站业务表格统一使用 `Table` 或 `DataTable`（禁止裸 `<table>` 与写死 `min-w-[900px]` 像素宽度），自动包裹 `overflow-x-auto` 适配窄屏。 |
| **`Sheet` / `Sidebar` / `Dialog`** | 基于 `@radix-ui/react-dialog`：`Dialog*`/`Modal`、`Sheet*`（四向侧滑抽屉）与 `Sidebar*`（桌面侧栏轨） | 唯一浮层与抽屉体系（已删除冗余 `modal.tsx` 与手写 `<dialog>`），统一遮罩、Esc、焦点锁定与 OKLCH 令牌。 |
| **`StatusBanner` / `Skeleton` / `PageHeader`** | `StatusBanner`（CVA 语义态横幅）、`Skeleton`、`PageHeader`、`PageSection`、`StatCard`、`FilterBar`、`EmptyState`、`PortalContainer` | 全面以 Tailwind OKLCH 语义令牌驱动，彻底淘汰 `.status-*` / `.portal-container` / `.console-sidebar` 手写 CSS 类。 |
| **`SegmentedControl`** | `value`、`options`、`onChange`、`ariaLabel` | 用于平级模式/范围切换（如「账号密码 / 粘贴 Token」「只读查询 / 扫描发现 / 变更计划」），子项最小高 `30px`，通过 `aria-pressed` 驱动主色高亮。 |
| **`PageTabs` / `Tabs`** | `PageTabs`（轻量受控）或 Radix `Tabs`（带键盘漫游焦点） | 面板顶部横向切换条，最小高 `44px`，底部 `2px` 品牌蓝激活指示线，窄屏允许横向滚动不折行挤压。 |

### 5.2 容器、表格、徽章与反馈条

| 组件 | 组成与变体 | 规范说明 |
|---|---|---|
| **`Card` / `.card-surface`** | `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent`, `CardFooter` | 常规分组使用 `Card`；登录框、重点概览或折叠操作台可使用 `.card-surface` 微高光表面。避免超过两层的卡片嵌套。 |
| **`Badge`** | `variant`: `default` \| `outline` \| `primary` \| `success` \| `warning` \| `danger` | `11px` 紧凑单行状态标签，统一用于角色（`admin`）、生命周期（`active` / `staged` / `revoked`）、连接状态与审计结果。 |
| **`Table`** | `Table`, `TableHeader`, `TableBody`, `TableRow`, `TableHead`, `TableCell` | 外层包裹 `overflow-x-auto` 容器保证窄屏可横向滚动；表头 `12px` 弱化色，单元格统一 `px-3 py-2.5`，宽表操作列支持右对齐或右侧 `sticky`。 |
| **`StatusBanner`** | `tone`: `error` \| `warning` \| `success` \| `info`；`compact?: boolean` | 基于 CVA 与 OKLCH 语义令牌（`bg-destructive/12`、`bg-warning/12` 等）；`error` 默认带 `role="alert"`，其余默认 `role="status"`，严禁散落手写裸色阶提示块或旧 `.status-*` 类。 |
| **`Modal` / `Dialog`** | 基于 `@radix-ui/react-dialog`：`Modal` 便捷封装 + `Dialog*` 组合原语 + `useAppDialog` | 所有模态框共享统一遮罩（`bg-black/65 backdrop-blur-xs`）、`rounded-xl bg-card shadow-xl` 容器、标准头部/正文滚动区/底部操作栏、Esc 关闭、焦点锁定与恢复。 |
| **辅助原语** | `DropdownMenu*`、`Tooltip*`、`ScrollArea`、`Separator`、`ResizablePanelGroup` | 统一从 `@/components/ui` 获取，保证浮层 `z-index`、圆角、阴影与键盘可达性一致。 |

---

## 6. Interaction States, Accessibility & Safety Locks（交互、无障碍与安全锁）

1. **完整组件状态契约**：所有交互控件必须覆盖 `default`、`hover`、`focus-visible`（`2px` 焦点轮廓）、`active/selected`（由 `aria-current` / `aria-pressed` / `aria-selected` / `data-[state=active]` 驱动）、`disabled`（`opacity: 0.5` + `cursor: not-allowed`）与 `loading/busy`（按钮文案明确变化如「连接中…」「正在停止…」，防止重复提交）。
2. **触屏与无障碍基线**：
   - 在粗指针触屏设备（`@media (pointer: coarse)`）下，所有 `[data-slot="button"]`、`[data-slot="segmented-control"] > button`、`[data-slot="input"]` 与 `[data-slot="select-trigger"]` 自动提升至最小 `2.75rem`（`44px`）点击热区。
   - 在系统开启减少动态效果（`prefers-reduced-motion: reduce`）时，关闭非必要的过渡与位移动画。
   - 终端与多行命令输入框必须识别中文/日文等 IME 输入法合成态（`isComposing`），合成期间按 Enter 仅上屏候选词，不触发命令提交。
3. **运维执行期安全锁**：
   - 当终端命令、领域工具或 AI 执行栏任务运行中时，必须锁定目标切换、会话切换、菜单导航与退出登录，保留当前输出流与独立的「停止」按钮。
   - 「执行」与「停止」必须分离渲染或防双击穿透（如 Kubernetes 面板 `event.detail <= 1` 保护），避免快速双击把刚提交的任务误取消或重复提交。
   - 页面刷新中断或网络断开导致终态丢失时，固定在工作台顶层展示 `warning` 级「结果未知」提示横幅，**绝不静默重放变更请求**。

---

## 7. Verification Checklist（UI 变更验收清单）

每次新增页面或重构共用组件后，按以下清单逐项验证（缺一项不得交付）：

1. **静态、类型与守卫检查**：
   ```bash
   git diff --check
   corepack pnpm exec tsc --noEmit
   corepack pnpm run test:gateway-policy
   corepack pnpm run test:console
   corepack pnpm run test:dosql
   corepack pnpm build
   ```
   其中 `lib/console-guards.test.mjs` 会对全部 `app/**/*.tsx`、`components/**/*.tsx`、`app/globals.css` 与 `app/console/console.css` 自动执行全站框架守卫扫描，必须零违规通过。
2. **多视口真实浏览器验收**：
   - 必须覆盖桌面宽屏（`1440×900`）、平板/小桌面（`1024×768`）与移动端（`390×844`）三档视口。
   - **硬指标**：`documentElement.scrollWidth <= window.innerWidth`（零横向溢出）、公开门户所有 `<PortalContainer>` 左右对齐误差 `≤ 1px`、顶栏导航不换行不溢出、移动端控制台顶栏单行不折行、所有 `<Sheet>` / `<Dialog>` / `<Select>` / `<DropdownMenu>` 正常弹出并支持 Esc 与焦点回收、浏览器 Console 零未捕获报错与零 React hydration 警告。
   - **全状态核验**：加载态（`Skeleton` / loading 文案）、空态（`EmptyState`）、错误态（`StatusBanner tone="error"`）、权限受限态、表单校验错误态、运行锁定态。

---

## 8. Mandatory Working Standards & Requirements（前端强制工作规范与工作要求）

本节是 DoOps 前端开发、改造、代码评审与 AI 协作的**强制工作规范与交付要求**，适用于所有公开门户（`/`、`/cases`、`/docs`、`/blog`）与运维控制台（`/console`、`/console/admin`）代码。

### 8.1 八条零容忍架构红线

| # | 严禁行为（零容忍） | 唯一允许的框架标准做法 |
|---|---|---|
| 1 | **禁止像素驱动设计**：严禁在业务组件中使用任意 `[...px]` Tailwind 类（如 `w-[240px]`、`min-w-[900px]`、`max-w-[300px]`、`text-[11px]`、`shadow-[0_0_8px_...]`）或手写像素弹窗坐标 | 使用 Tailwind 标准 `rem` 标尺（`w-60`、`max-w-72`、`text-xs` / `text-2xs`、`shadow-lg`）与响应式弹性/栅格布局；浮层定位全权交给 Radix Popper / Dialog / Sheet |
| 2 | **禁止手写 CSS 组件原语**：严禁在 `app/globals.css` 或 `app/console/console.css` 新增 `.btn*`、`.input*`、`.status-*`、`.page-tab`、`.segmented`、`.console-sidebar`、`.portal-container` 等自定义组件样式类 | 组件样式通过 CVA + Tailwind 工具类封装在 `components/ui/*.tsx`，由 `@/components/ui` 统一导出 |
| 3 | **禁止破坏标准字号标尺**：严禁在 `@theme` 中覆写 Tailwind 原生 `--text-xs` / `--text-sm` / `--text-base` / `--text-lg` / `--text-xl` | 完整保留 Tailwind v4 标准 `rem` 字号阶梯，仅使用标准类名与唯一命名小字令牌 `text-2xs`（`0.6875rem`） |
| 4 | **禁止硬编码颜色与裸色阶**：严禁在 JSX 或 CSS 中写入 `#hex`、`rgb()` 状态色或 Tailwind 裸色阶（`slate-*`、`sky-*`、`cyan-*`、`blue-*`、`emerald-*`、`amber-*`、`rose-*` 等） | 全部使用 `app/globals.css` 定义的 OKLCH 语义令牌（`background`、`card`、`foreground`、`muted-foreground`、`primary`、`accent`、`success`、`warning`、`destructive`、`sidebar-*`、`border`、`input`、`ring`） |
| 5 | **禁止裸 HTML 表格与自造弹层**：严禁在业务页面手写裸 `<table>`、原生 `<dialog>`、自定义 `fixed inset-0` 遮罩或第二套弹窗壳 | 表格统一用 `@/components/ui` 的 `<Table>` 或 TanStack `<DataTable>`；弹窗统一用 `<Dialog>` / `<Modal>`；抽屉统一用 `<Sheet>`；侧栏统一用 `<Sidebar>` |
| 6 | **禁止公开门户容器宽度漂移**：严禁在首页、案例、文档、博客页面散落手写 `max-w-3xl` / `max-w-5xl` / `max-w-[90rem]` 作为页面外层容器 | 所有公开页外层对齐轨一律使用 `<PortalContainer>`，保证页头、正文、侧栏、目录与页脚严格齐平 |
| 7 | **禁止错误版本包管理器改写锁文件**：严禁使用 `pnpm v9` 或 `npm` / `yarn` 执行安装或修改 `pnpm-lock.yaml`（会静默破坏 `pnpm-workspace.yaml` 中的 `patchedDependencies`） | 一律使用 `corepack pnpm`（锁定 `pnpm@10.18.0`，与 `package.json#packageManager` 和 CI 一致） |
| 8 | **禁止弱化守卫或绕过验收**：严禁删除/放宽 `lib/console-guards.test.mjs` 断言、严禁用 `@ts-ignore` / `any` 掩盖类型错误、严禁未经真实浏览器验证即宣称 UI 改动完成 | 从根因修复违规项并重跑完整验收链；任何发现的问题当轮解决或显式登记到 `docs/operations/backlog.md` |

### 8.2 代码分层与目录职责边界

| 目录 / 文件 | 唯一职责 | 边界约束 |
|---|---|---|
| `components.json`、`components/ui/*`、`components/ui.tsx` | 标准 shadcn/ui + Radix UI + TanStack Table 框架原语与通用布局骨架（`PortalContainer`、`Sidebar`、`Sheet`、`PageHeader`、`DataTable` 等） | **零业务逻辑、零接口请求**；对外统一经 `@/components/ui` 导出，业务组件不绕过统一出口重复封装 |
| `app/globals.css` | 唯一全局设计令牌源（OKLCH 语义色、圆角、基础排版、最小化通用质感类与 `.agent-ui .cm-*` 适配） | 禁止写入具体业务页面样式或旧式 `.btn`/`.input` 类；除发丝边框 `1px` 外禁止写固定 `px` 尺寸 |
| `app/console/console.css` | **仅限**第三方包 `@agent-cloud/agent-ui` 的作用域主题覆盖 | 禁止出现任何业务布局/侧栏/登录样式，禁止硬编码 `#hex` |
| `app/**/page.tsx`、`app/**/layout.tsx` | Next.js 路由装配、元数据声明、服务端数据读取与外壳组合 | 保持薄路由层，不堆砌复杂状态机或大段内联业务渲染 |
| `components/*.tsx`、`components/plugins/*.tsx` | 领域交互视图与受控表单 | 必须组合 `@/components/ui` 原语；纯计算/格式化/筛选/分页/状态机逻辑下沉到 `lib/*.ts` 并配单测 |
| `lib/*.ts`、`lib/*.test.mjs` | 纯函数领域逻辑、API 客户端、BFF 代理与自动化单测/守卫 | 可脱离浏览器在 Node 测试运行器直接验证；新增核心交互必须同步补充单测 |

### 8.3 标准工作流（接单 → 设计 → 实现 → 验收 → 交付）

1. **动手前先登记与对齐**：
   - 在 `docs/operations/backlog.md` 确认/登记任务行与文件归属，明确改动层级（`T0 / T1 / T2`，见 `AGENTS.md`）。
   - 涉及架构选型、新依赖引入、跨页交互重组或视觉基调变化时，先完成现状排查并向负责人确认方案，不擅自做高风险假设。
2. **实现期坚持框架优先与逻辑解耦**：
   - 先检查 `@/components/ui` 现有原语是否已满足需求；确需新原语时先沉淀到 `components/ui/` 并通过 `components/ui.tsx` 导出，再由业务页复用（遵循 shadcn/ui + Radix 规范）。
   - 列表超过单屏数据量必须支持分页（优先 `DataTable` + `DataTablePagination` 或稳定分页辅助函数）与按可见页按需拉取，禁止一把梭全量渲染数百行 DOM。
   - 涉及敏感凭据的表单与视图必须遵守 `AGENTS.md` 凭据安全红线：零密文入 URL/Prompt/日志、提交/关闭即清零前端敏感状态、测试仅使用合成金丝雀值。
3. **同批更新文档与测试**：
   - 行为或规范变更同批更新唯一权威文档（前端规范改 `DESIGN.md`、控制台行为改 `backend/docs/DOOPS_WEBUI.md`、用户可见改动记入 `CHANGELOG.md` `## [Unreleased]`）。
   - 新增或修改核心逻辑/UI 契约时，同批补齐 `lib/*.test.mjs` 单测，并至少执行一次反向验证（确认测试能真正拦截缺陷）。
4. **交付闭环与证据留痕**：
   - 严格遵循 `docs/operations/closure-standard.md`：按「真跑浏览器 > 单测 > 静态守卫 > 未覆盖」分级汇报实测命令与结果，未覆盖项必须显式说明原因。
   - 提交前在干净归档树复验；使用 `git commit -o <owned paths>` 提交避免夹带无关改动；除非用户明确指令，不擅自合并主干、推送远端或切版发布。
