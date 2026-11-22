---
name: doops-console-design
scope: web-console
owner: doops-maintainers
updated: 2026-11-22
---

# Design System: DoOps Console & Portal (`doops-console`)

面向运维工程师、SRE 与平台管理员的统一智能运维设计系统。以「**选定目标 -> 核查资产/指标 -> 受控执行/发布 -> 追溯审计回执**」为核心主路径，追求高信噪比、明确权限边界与可验证的操作反馈。

所有视觉令牌的唯一权威来源是 `app/globals.css`（工作台侧栏补充令牌位于 `app/console/console.css`）；基础原语与业务通用控件统一由 `@/components/ui`（`components/ui.tsx` 及 `components/ui/*`）导出。

---

## 1. Visual Theme & Atmosphere（视觉基调与界面氛围）

DoOps 采用**深色优先（Dark-first, `color-scheme: dark`）**的工业级运维美学，严格区分两类表面：

1. **生产运维工作台（`/console`、`/console/admin`）—— 高密度、高确定性、零装饰噪音**
   - 铺满视口（`100dvh` 全宽），以发丝级微亮边框、克制的深海军蓝层级对比和清晰的状态色区分信息模块。
   - **禁止**在运维、台账、审计、平台管理页面使用营销式大留白 Hero、紫粉渐变卡片、毛玻璃堆叠或无意义的装饰动效。
   - 记忆点来自工作流清晰度：当前目标身份、会话 ID、在线状态、执行进度、危险动作边界与审计证据一目了然。
2. **公开产品门户与文档（`/`、`/cases`、`/blog`、`/docs/deploy`）—— 克制科技感与高可读性**
   - 采用居中容器（`max-w-6xl` / 阅读区 `max-w-3xl`）、磨砂吸顶导航栏（`.site-header`）、低不透明度品牌蓝青环境光（`.hero-ambient`）与胶囊形主行动按钮（`.btn-lg` / `.btn-md`）。

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
| **Opaque Rail Navy** | `.console-sidebar` | `#101b2b`（激活态 `#174d86` + `#9edbff` 左边条） | 工作台左侧不透明导航轨，确保任何主区背景都不干扰菜单对比度 |

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
  - 全站使用 `app/globals.css` 中的 `rem` 标尺，尊重浏览器默认字号。下列像素值以默认 `16px` 根字号为例，不通过更改根字号来缩放布局。
  - **标题**：管理页标题 `text-xl`（22px），区块标题 `text-base`（18px）；整屏恢复会话等主要状态用 `text-lg`（20px）。公开页保留响应式大标题。
  - **正文和控件**：`text-sm`（16px / 24px）用于常规正文、导航项和标准输入；长篇对话正文用 `text-base`。
  - **辅助文字**：`text-xs`（14px / 20px）用于分组标题、表头与紧凑控件。`text-2xs`（13px / 18px）仅用于徽章、编号、耗时等次要信息。不另写固定像素小字。

---

## 4. Layout, Spacing & Geometry（布局、间距与几何圆角）

### 4.1 布局骨架
- **控制台外壳（`ConsoleShell`）**：`h-dvh w-full flex-col overflow-hidden`，外层不设最大宽度限制。
  - **顶栏（Header）**：固定高紧凑工具条（品牌标志高 `34px`），左侧为窄屏菜单触发器 + DoOps 标识，右侧收敛 `TargetSwitcher`、当前用户名、博客、更新内容（带未读圆点）、帮助与断开按钮。
  - **左侧导航栏（`ConsoleNav`）**：`≥768px` 显示固定宽 `clamp(176px, 16vw, 216px)` 独立滚动侧栏；`<768px` 收起为左上角「菜单」触发的模态 `<dialog>` 抽屉（宽 `min(80vw, 288px)`，支持 Esc 与遮罩点击关闭）。
  - **主工作区（Workspace）**：顶层固定承载离线横幅、中断命令「结果未知」横幅、错误提示与资产回跳上下文条；下方区域根据面板特性使用内部滚动或分栏滚动，禁止整页溢出裁切。
- **布局原语优先**：在业务页面中优先组合 `Stack`（纵向节奏）、`Inline`（工具条/操作行）、`Grid`（响应式卡片墙）、`Container`、`Spacer` 与 `ResizablePanelGroup`（左右/上下可拖拽分栏），避免散落手写复杂栅格字符串。
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
| **`Button`** | `variant`: `default` \| `primary` \| `danger` \| `danger-outline` \| `ghost`<br>`size`: `default` \| `sm` \| `md` \| `lg`；`icon?: boolean` | 默认最小高 `36px`（`14px` 字）；工具条/表格行内用 `size="sm"`（最小高 `32px`，`12px` 字）；方形纯图标按钮加 `icon` 且必须提供 `aria-label` 或 `title`。`danger` 仅用于最终破坏性确认或紧急停止，`danger-outline` 用于次级危险操作（如行内停用/停止）。 |
| **`Input` / `Textarea`** | `scale`: `default` \| `sm`；支持 `aria-invalid` | 默认最小高 `36px`（`sm` 为 `32px`），深底 `--background` + `1px` 描边，聚焦切换 `--ring` 边框，校验失败自动切换 `--destructive` 边框。 |
| **`Select`** | `scale`: `default` \| `sm`（底层同步隐藏原生 `<select>`） | 视觉与 `Input` 完全对齐，弹出跟随主题的顶层 `listbox` 菜单，支持方向键、Home/End、首字母检索、Enter 确认、Esc 关闭及原生表单校验。 |
| **`Checkbox`** | 原生 `input[type="checkbox"]` 封装 | `16px × 16px` 圆角方框，`accent-color: var(--primary)`，统一焦点环与禁用半透明表现，始终包裹在 `<label>` 内或绑定 `aria-label`。 |
| **`SegmentedControl`** | `value`、`options`、`onChange`、`ariaLabel` | 用于平级模式/范围切换（如「账号密码 / 粘贴 Token」「只读查询 / 扫描发现 / 变更计划」），子项最小高 `30px`，通过 `aria-pressed` 驱动主色高亮。 |
| **`PageTabs` / `Tabs`** | `PageTabs`（轻量受控）或 Radix `Tabs`（带键盘漫游焦点） | 面板顶部横向切换条，最小高 `44px`，底部 `2px` 品牌蓝激活指示线，窄屏允许横向滚动不折行挤压。 |

### 5.2 容器、表格、徽章与反馈条

| 组件 | 组成与变体 | 规范说明 |
|---|---|---|
| **`Card` / `.card-surface`** | `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent`, `CardFooter` | 常规分组使用 `Card`；登录框、重点概览或折叠操作台可使用 `.card-surface` 微高光表面。避免超过两层的卡片嵌套。 |
| **`Badge`** | `variant`: `default` \| `outline` \| `primary` \| `success` \| `warning` \| `danger` | `11px` 紧凑单行状态标签，统一用于角色（`admin`）、生命周期（`active` / `staged` / `revoked`）、连接状态与审计结果。 |
| **`Table`** | `Table`, `TableHeader`, `TableBody`, `TableRow`, `TableHead`, `TableCell` | 外层包裹 `overflow-x-auto` 容器保证窄屏可横向滚动；表头 `12px` 弱化色，单元格统一 `px-3 py-2.5`，宽表操作列支持右对齐或右侧 `sticky`。 |
| **`StatusBanner`** | `tone`: `error` \| `warning` \| `success` \| `info`；`compact?: boolean` | 统一封装 `.status-*` 横幅；`error` 默认带 `role="alert"`，其余默认 `role="status"`，严禁散落手写 `bg-amber-*` / `bg-red-*` 提示块。 |
| **`Modal` / `Dialog`** | 基于 `@radix-ui/react-dialog`：`Modal` 便捷封装 + `Dialog*` 组合原语 + `useAppDialog` | 所有模态框共享统一遮罩（`bg-black/65 backdrop-blur-xs`）、`rounded-xl bg-card shadow-xl` 容器、标准头部/正文滚动区/底部操作栏、Esc 关闭、焦点锁定与恢复。 |
| **辅助原语** | `DropdownMenu*`、`Tooltip*`、`ScrollArea`、`Separator`、`ResizablePanelGroup` | 统一从 `@/components/ui` 获取，保证浮层 `z-index`、圆角、阴影与键盘可达性一致。 |

---

## 6. Interaction States, Accessibility & Safety Locks（交互、无障碍与安全锁）

1. **完整组件状态契约**：所有交互控件必须覆盖 `default`、`hover`、`focus-visible`（`2px` 焦点轮廓）、`active/selected`（由 `aria-current` / `aria-pressed` / `aria-selected` / `data-[state=active]` 驱动）、`disabled`（`opacity: 0.5` + `cursor: not-allowed`）与 `loading/busy`（按钮文案明确变化如「连接中…」「正在停止…」，防止重复提交）。
2. **触屏与无障碍基线**：
   - 在粗指针触屏设备（`@media (pointer: coarse)`）下，所有 `.btn`、`.segment-option` 与单行 `.input` 自动提升至最小 `44px × 44px` (`2.75rem`) 点击热区。
   - 在系统开启减少动态效果（`prefers-reduced-motion: reduce`）时，关闭非必要的过渡与位移动画。
   - 终端与多行命令输入框必须识别中文/日文等 IME 输入法合成态（`isComposing`），合成期间按 Enter 仅上屏候选词，不触发命令提交。
3. **运维执行期安全锁**：
   - 当终端命令、领域工具或 AI 执行栏任务运行中时，必须锁定目标切换、会话切换、菜单导航与退出登录，保留当前输出流与独立的「停止」按钮。
   - 「执行」与「停止」必须分离渲染或防双击穿透（如 Kubernetes 面板 `event.detail <= 1` 保护），避免快速双击把刚提交的任务误取消或重复提交。
   - 页面刷新中断或网络断开导致终态丢失时，固定在工作台顶层展示 `warning` 级「结果未知」提示横幅，**绝不静默重放变更请求**。

---

## 7. Verification Checklist（UI 变更验收清单）

每次新增页面或重构共用组件后，按以下清单完成验证：

1. **静态与构建检查**：`pnpm exec tsc --noEmit` 零类型错误，`pnpm build` 生产构建通过，`git diff --check` 无空白格式问题，Python 契约测试通过。
2. **令牌与组件一致性检查**：新增/修改代码中无裸十六进制颜色、无 `amber-*` / `emerald-*` 等绕过主题令牌的 Tailwind 颜色类、无绕过 `@/components/ui` 的手写弹窗或重复控件样式串。
3. **多视口与真实浏览器检查**：
   - 核对宽屏桌面（`1440px+`）与移动端窄屏（`390px`）下的顶栏、侧边栏/抽屉、目标选择器弹层、数据表格横向滚动与分栏面板。
   - 逐一验证加载态、空态、权限受限态、表单校验错误态、运行锁定态以及弹窗的键盘聚焦/Esc 关闭行为，确保浏览器 Console 无运行时异常或无障碍缺失警告。
