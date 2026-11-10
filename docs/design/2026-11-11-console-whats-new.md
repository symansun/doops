# 控制台「更新内容」设计记录（magpie 对照）

- 日期：2026-11-11
- 状态：已通过生产构建与浏览器验收（2026-11-11，见第 6 节）
- 版本基线：`v0.2.3`（2026-11-11 已发布 tag）；本功能尚未发布，CHANGELOG 条目位于 `[Unreleased]`
- 相关实现：`lib/changelog.ts`、`lib/whats-new.ts`、`lib/server/changelog.ts`、`app/console/page.tsx`、`components/console-shell.tsx`、`components/whats-new-dialog.tsx`、`Dockerfile.web`
- 参考实现：magpie（`internal/gui/whatsnew.go`、`internal/update/notes.go`、`internal/gui/assets/app.js`、`site/worker.js`）
- 文档：`backend/docs/DOOPS_WEBUI.md` 的 What's New in the Console 一节；数据源是仓库根 `CHANGELOG.md`

## 1. 目标与结论

控制台要在不依赖 Gateway、不联网的前提下回答「这一版改了什么」：版本升级后首次进入时自动弹出一次，平时可从顶栏随时打开；内容来自仓库根 `CHANGELOG.md`，由 Web 镜像构建时打包。

magpie 的同类能力是本次参考对象：桌面端在本地文件夹记录 last-version，启动到新版本后向站点 `/api/notes` 拉取自上次版本以来的 release notes，合并为一个对话框展示一次，并提供「今天不再弹出」与 Settings 手动入口。DoOps 保留这套交互骨架，把数据源换成随镜像打包的 CHANGELOG，把状态放进浏览器 localStorage，服务端不新增接口。

## 2. magpie 事实核对（源码）

### 2.1 数据源与拉取

- `internal/update/notes.go`：`NotesFeed()` 默认指向站点 `/api/notes`（可用 `MAGPIE_NOTES_FEED` 覆盖）；`NotesBetween(after, upto, lang)` 请求 `?upto=&after=&lang=`，`NotesBack` 用 `limit=n+1` 取「本版本及之前 n 个」。
- `site/worker.js`：`/api/notes?after=&upto=&limit=` 返回 `{releases:[{version, notes, url, published}]}`，newest first；`?lang=zh` 只截取 `<!-- lang:zh -->` 标记以下的中文；数据源是 GitHub Releases（`yetone/magpie-releases`），只索引最近 100 个 release。
- `internal/gui/whatsnew.go`：`lastRunFile`（last-version）、`updatedInAppFile`（updated-in-app）、`quietDayFile`（whatsnew-quiet-day）三个文件记录状态；`start()` 计算 `after/pending/quiet` 并写入本次版本号。
- 失败路径：站点不可达时回退到 GitHub release 页面；仍失败则对话框显示「Couldn't load the release notes」并提供 Try again 与 release page 链接；完全离线时不展示。

### 2.2 弹出规则

- 第一次运行（没有 last-version）：只记录版本，不弹历史。
- 运行到比上次新的版本：自动弹一次；`usedBefore` 用于区分全新安装。
- 用户从应用内「Restart to update」升级：先写 `updated-in-app` 标记，新版本首次启动时保持安静，notes 只在 Settings 里可看。
- 自动弹出的「Don't show again today」写入本地日期文件，当日余下时间不再自动弹；取消勾选会删除文件。手动入口不受影响。

### 2.3 展示与交互（`app.js`）

- `showWhatsNew(releases, auto, more)`：多版本时用 `mergedNotes`，单版本时按 release notes 原文分块渲染；自动弹窗带「Don't show again today」勾选项，手动打开带范围选择器 `wnRange`（Since the last update / This version and 10/20/50/100 before）。
- `mergedNotes`：所有版本的条目合并成一份，分类顺序由 `NOTE_ORDER` 决定（Features、Improvements、Performance Improvements、Bug Fixes、Security、Provider Changes、Changes、Documentation），未知标题归入 Changes；每条带版本徽标，范围摘要为 `{n} releases · vFROM – vTO`；重复文本只留一次；没有 notes 的版本略过。
- 解析细节：标题接受 1-6 个 `#`，条目标记接受 `-`/`*`/`+`，缩进 2 个空格的续行并入上一条，列表外的普通行按条目处理；`inlineMD` 只对 `**bold**`、`code`、`[text](https://…)`、裸 http(s) 链接和 `#123` issue 引用建节点，一律作为文本节点插入，不写 innerHTML。

## 3. DoOps 设计

### 3.1 数据流

```text
仓库根 CHANGELOG.md
  -> Dockerfile.web: COPY CHANGELOG.md ./CHANGELOG.md（builder 阶段；runner 阶段也保留一份，静态预渲染失效时仍可读）
  -> lib/server/changelog.ts: loadChangelogReleases() 读取 process.cwd()/CHANGELOG.md
  -> lib/changelog.ts: parseChangelog()（解析、按语义版本降序）
  -> app/console/page.tsx: async Server Component，把 releases 交给 ConsoleShell
  -> components/console-shell.tsx: 顶栏入口 + autoOpenDecision() 决定自动弹出
  -> components/whats-new-dialog.tsx: selectReleases()/rangeReleases() + mergeReleases() 渲染
```

没有新增 Gateway RPC、没有运行时 HTTP 请求、没有 API route；文件读不到时返回空数组。

### 3.2 解析契约（`lib/changelog.ts`）

- 版本标题：`## [0.2.2] — 2026-11-08`。接受 `## 0.2.2`、`## [v0.2.2]` 等变体与预发布后缀；日期可缺省，也接受括号日期；`## [Unreleased]` 解析进 `unreleased` 但不展示。
- 当前版本：`releases[0]`（`parseChangelog` 已按 `compareVersions` 降序排序；`latestRelease` 取第一个）。
- 分类标题只认 `##` / `###` 两级；`###` 标题经别名表映射到 `CATEGORY_ORDER`（added、changed、performance、fixed、security、deprecated、removed、other），英文与常见中文标题都接受，未知标题归「其他」。
- 条目以 `-` / `*` 列表标记开头；条目后的非空普通行按续行以空格拼接，空行结束条目。无法识别的 `##` 标题会结束上一个 release；空 release 与重复版本 section 都跳过。
- 行内只支持三种 token：`code`、`**粗体**`、`[文本](https://…)`；非 http(s) 链接整段按纯文本渲染。`mergeReleases` 按分类聚合并按小写/空白归一后的文本去重，每个条目保留它首次出现的版本，范围用 `from`/`to`/`versions` 表示。

### 3.3 状态机（`lib/whats-new.ts`）

浏览器本地状态只有两项，均为 per-browser、不上报：

- `LAST_VERSION_KEY`：上次看过或记录过的版本号。
- `QUIET_DAY_KEY`：用户点「今天不再弹出」的本地日期（`YYYY-MM-DD`，按浏览器本地日切分，不是 UTC）。

`autoOpenDecision({state, currentVersion, today})` 的决策表：

| 条件 | 决策 |
| --- | --- |
| `currentVersion` 为空（镜像没有 CHANGELOG） | skip |
| `lastVersion` 为空（首次访问或存储被清） | record：静默记录当前版本，不弹 |
| `lastVersion >= currentVersion` | skip |
| `lastVersion < currentVersion` 且 `quietDay == today` | skip：只抑制当天自动弹出 |
| `lastVersion < currentVersion` | open：自动弹一次 |

- 已读时机：`openWhatsNew()` 在打开对话框时（自动或手动）立即 `recordLastVersion` 并清掉未读圆点，所以刷新或再次打开不会重复自动弹；关闭时再记录一次，只有勾选「今天不再弹出」才写 `QUIET_DAY_KEY`。
- 未读圆点：`hasUnseenVersions` 仅在 `lastVersion` 存在且小于当前版本时为 true；首次访问没有 lastVersion，不显示圆点。
- 自动弹出时机：等会话恢复且新手引导关闭后再打开，每页加载最多一次。
- 展示范围：自动弹窗固定「自上次查看以来」，用 `selectReleases({since})`；手动弹窗可选「自上次查看以来」或最近 10/20/50/100 个版本。since 为空（上次查看后没有新版本）时用 `rangeReleases({count:10})` 回退并显示提示，保证对话框不空。

### 3.4 UI 行为

- 顶栏按钮：`HistoryIcon` +「更新内容」；当前版本无数据时 `disabled`，title 提示「当前版本没有更新内容」；未读时显示小圆点并带屏幕阅读器文案「有新版本」。
- 自动弹窗：`mode="auto"`，默认范围「自上次查看以来」，不显示范围选择器，底部有「今天不再弹出」勾选框。
- 手动弹窗：`mode="manual"`，顶部提供范围选择「自上次查看以来 / 最近 10/20/50/100 个版本」；有未读时默认「自上次查看以来」，否则默认最近 20 个版本。
- 标题与副标题：`v{to} 更新内容`、`共 N 个版本 · vFrom – vTo`（单版本只显示 `vTo`）；无内容时为「更新内容」/「当前版本没有更新内容」。
- 对话框结构：分类标题带条目数徽标，每条带 `v{version}` 版本徽标；since 回退时显示「上次查看之后没有新版本，下面显示最近 10 个版本的更新。」；空范围显示「当前范围内没有可展示的更新内容。」
- 关闭方式：关闭按钮、Escape、点击遮罩；打开时锁定 body 滚动并把焦点移到关闭按钮，关闭后焦点回到打开前的元素。

### 3.5 失败与降级

- `CHANGELOG.md` 缺失、不可读、为空或无有效版本：`loadChangelogReleases` 返回 `[]`，控制台照常渲染，入口禁用、对话框不渲染，不抛错。
- localStorage 被禁用或抛异常：读写被捕获，状态视为首次访问（`record`，不自动弹），手动入口仍可用。
- 解析容错是逐行的：坏标题不会吞掉后续内容；未知分类、空版本、重复版本都不会让对话框崩掉。
- 无 Gateway/网络依赖：air-gapped 部署与 Gateway 故障都不影响该对话框。

### 3.6 安全边界

- 数据是构建产物，不新增端点、不改 Gateway 鉴权、不读取用户信息、不发遥测。
- 渲染只走 React 文本节点与白名单行内 token（`InlineText` 再次校验 http(s)）；原始 HTML 不执行，非 http(s) 链接不生成 `<a>`；外链带 `rel="noopener noreferrer"`。
- localStorage 只存版本号与本地日期，不存 token、用户名或会话；不改动已有的 HttpOnly cookie 会话。
- CHANGELOG 属于仓库受控内容，但仍按不可信文本渲染（防御纵深）。

## 4. 与 magpie 的取舍

| 维度 | magpie | DoOps |
| --- | --- | --- |
| 数据源 | 站点 `/api/notes` + GitHub Releases，运行时拉取 | 仓库根 `CHANGELOG.md`，镜像构建时打包，零网络 |
| 历史来源 | release notes Markdown（含语言标记） | Keep a Changelog 分类条目，中文 |
| 状态存储 | 应用目录文件（last-version、updated-in-app、quiet-day） | 浏览器 localStorage（LAST_VERSION_KEY、QUIET_DAY_KEY） |
| 升级继承 | 区分应用内重启更新，那类升级首次启动不弹 | 无应用内重启更新流程，只按版本比较；首次访问静默记录 |
| 范围选择 | since / 本版本及之前 10/20/50/100 | 自动固定 since（空则最近 10）；手动可选 since / 最近 10/20/50/100 |
| 失败回退 | release page 链接 + Try again | 无远程地址，降级为无内容/入口禁用 |
| 分类 | NOTE_ORDER，未知归 Changes | Keep a Changelog 固定顺序，未知归「其他」 |
| 多语言 | 英文/中文双段 release notes | 单一中文 CHANGELOG |

未实现（可作为后续）：release page 回退链接、多语言正文、Provider Changes/Documentation 等 magpie 专有分类。

## 5. 验收标准

1. `pnpm test:console`（含 `lib/changelog.test.mjs`、`lib/whats-new.test.mjs`）全部通过（解析、合并、范围、行内 token、状态机、存储失败）。
2. CHANGELOG 解析：`[Unreleased]` 不出现在展示列表；0.2.2 > 0.2.1 > 0.2.0 > 0.1.0；日期缺省可用；未知标题归「其他」；空版本与重复版本跳过。
3. 状态机：首次访问不弹且记录版本；版本更新当天弹一次；勾选「今天不再弹出」后当天不再自动弹但手动可开；打开过对话框后刷新不再自动弹。
4. 降级：镜像内没有 `CHANGELOG.md` 时 `pnpm build` 成功、控制台正常渲染、入口禁用。
5. 安全：条目中的 `<img onerror=…>`、`[x](javascript:…)` 以纯文本显示，不产生 HTML 或可点击链接。
6. Web 验收：生产构建 + 浏览器登录；顶栏入口、自动弹窗、手动打开、范围切换、关闭、Escape/遮罩、localStorage 被禁用、Gateway 断网均符合预期；console 无错误，Network 无新增请求。
7. `git diff --check` 干净；`backend/docs/DOOPS_WEBUI.md` 与本记录描述一致。

## 6. 验收记录（2026-11-11）

命令：`pnpm test:gateway-policy`、`pnpm test:console`（576 项，含 `lib/changelog.test.mjs`、`lib/whats-new.test.mjs` 20 项）、`pnpm exec tsc --noEmit`、`pnpm build` 全部通过；`git diff --check` 干净。构建输出中 `/console` 为 `○ Static`，预渲染 HTML 已包含 CHANGELOG 条目，说明内容在构建时读入、运行时零请求。

浏览器（ego-browser，`pnpm dev` + `/console?demo=1`）：

- 手动入口：顶栏「更新内容」打开对话框，标题 `v0.2.3 更新内容`、副标题给出范围与版本数，分类分组带数量与版本徽标，范围选择默认「最近 20 个版本」，切换范围即时重排。
- 自动弹出：把 localStorage 的已读版本改成 0.2.0 后刷新，自动弹窗只显示 0.2.1–0.2.3，无范围选择器，底部有「今天不再弹出」；打开即把已读版本写成 0.2.3。
- 抑制与未读：勾选「今天不再弹出」并关闭后写入当天日期；再次把已读版本改回 0.2.0 并刷新，当天不自动弹、顶栏按钮出现未读圆点，手动入口仍可打开且默认「自上次查看以来」。
- 关闭方式：Esc、点遮罩、关闭按钮均可关闭；打开时焦点落到关闭按钮，关闭后 restore。
- 健康度：弹窗打开期间无任何网络请求、无 console 错误、无 Next.js 错误覆盖层；`body` 滚动在打开时锁定、关闭后恢复。
- 截图：`.local/whats-new-evidence/`（本地忽略目录，不随仓库交付）。

未演练项：镜像内缺少 `CHANGELOG.md` 的降级路径由 `loadChangelogReleases` 单元测试覆盖（返回空数组），未在真实镜像中演练；`Dockerfile.web` 已 COPY 该文件，文件缺失会先让构建失败，因此线上不会静默丢内容。
