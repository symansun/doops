# 《再见 SSH 之后》配图设计（images/design.md）

> 对象文章：`再见SSH之后-DoOps是什么-安全靠什么保证.md`
> 产出：8 张递进配图 + 本设计文档 + 每张图的 prompt 原文（`prompts/`）
> 生成方式：image2 CLI（`image2_with_env.sh generate`），模型 gpt-image-2.5-flare，尺寸 1536×1024，quality high。

## 0. 约定

- **图内文字只有英文短标签**（SSH / DoOps / Gateway / Agent / Token / Vault / Cluster A-C / 数字 1-7），**绝不出现中文**。
  中文解释一律交给文章图注（`图 N · 一句话`），这样图不会因为中文字形而崩。
- 每张图只讲**一个**论点，元素数量克制，宁可留白也不要密。
- 八张图按文章的叙事顺序递进：**对比 → 总览 → 部署形态 → 单次流程 → 七道门 → 身份与凭据 → 两种角色 → 责任边界**。
- CLI 固定输出 1536×1024（3:2）；构图按宽幅横向节奏设计，主体集中在中央安全区，文章里按 16:9 裁切不会切到关键元素。

## 1. 统一视觉规范

| 项 | 规定 |
|---|---|
| 风格 | 极简编辑风 / 扁平线描（flat vector line art），专业杂志插图感 |
| 背景 | 纯白 #FFFFFF |
| 线条 | 细而均匀的黑线（约 2px），几何构成，无阴影无渐变 |
| 填充 | 容器内部极浅灰 #F2F3F2 |
| 强调色 | 单一墨绿 **#0E7C66**，只用于少量箭头 / 一扇门 / 一个关键元素 |
| 字体 | 干净的几何无衬线，小号、疏朗；只有短标签 |
| 构图 | 正视平视（front elevation），内容居中，四周大幅留白 |
| 禁止 | 中文/日文/韩文、乱码字母、长句、图例、渐变、投影、3D、等距视角、霓虹、纹理、水印、密集小字、外边框 |

### 统一风格前缀（每个 prompt 首段，逐字复用）

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.
```

### 统一负面约束（每个 prompt 末段，逐字复用）

```
no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 统一文字规则

```
TEXT RULE: the only text allowed in the entire image is <labels>, spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.
```

## 2. 八张图逐一设计

### 图 1 · `01-ssh-vs-doops.png` — SSH vs DoOps：门的数量与方向

- **讲哪个点**：文章 0 节的核心对比：SSH 是每台机器一扇门、钥匙散在很多人手里；DoOps 是所有人走同一扇门，机器自己在门里向外连。
- **画面布局**：左右对开，中线一条细黑竖线。左半区标题 SSH；右半区标题 DoOps。
- **元素**：左：三台小机器方块（各带一扇小门 + 门把手），每台旁边一个小人举着一把钥匙，实线箭头从人指进机器门（inbound）；空中有几把散落的小钥匙。右：中央一扇高的拱门/大门（Gateway，墨绿描边），门上方两个小人和一个机器人头共持一枚圆令牌；一条实线箭头进门；门下方三台机器（没有门），墨绿色虚线箭头自下而上指向大门（outbound）。
- **图内文字**：`SSH` / `DoOps` / `Gateway`
- **完整 prompt**（`prompts/01.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-AND-RIGHT COMPARISON DIAGRAM, split by one thin black vertical hairline exactly at the center, both halves share the same horizon.

LEFT HALF ("SSH" world): three small identical machine boxes stacked vertically, each machine box has one small door with a round handle on its front, drawn slightly ajar. Next to each machine stands one tiny simple human figure holding an old-fashioned key. A short solid black arrow runs from each human into that machine's door (traffic goes inward). A few loose keys float in the empty space of the left half. The single word "SSH" sits at the top-left corner of this half, small and black.

RIGHT HALF ("DoOps" world): in the middle sits ONE tall arched gateway door with a thin black frame, highlighted with a deep teal-green outline, and a small round handle. Above the gateway, two tiny human figures and one small robot head stand together holding a single round disc-shaped token. One solid arrow goes from them down into the open gateway. Below the gateway, three plain machine boxes without doors; from each of these three machines a dashed deep teal-green arrow curves upward into the gateway, showing the machines connect outward to the single door. The single word "DoOps" sits at the top-right corner of this half.

The single word "Gateway" is placed just above the arched door, small and neat.

TEXT RULE: the only text allowed in the entire image is "SSH", "DoOps", "Gateway", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 2 · `02-architecture.png` — 总览：多个集群汇聚到一扇门

- **讲哪个点**：文章 1.1 四角色 + 1.4 反向连接：入口层 → 统一网关（认证/授权/路由/审计）→ 各集群里的 Agent 主动出站 → 目标。这是全文最重要的一张。
- **画面布局**：三层纵向结构：顶部入口图标行 → 中央大 Gateway 容器 → 底部三个集群横排；集群内每台机器上一个 Agent 小圆点；底部一条极简的目标图标行。
- **元素**：顶部：笔记本、机器人头、浏览器窗口三个小图标横排；一条实线箭头下指网关。网关：圆角矩形（浅灰填充、细黑描边），内部四个小图标横排：钥匙、盾牌、分叉路由箭头、账本。网关下方虚线分三路到 Cluster A/B/C 三个圆角容器；每个容器内三台小服务器方块，每块右上角一个墨绿小圆点（Agent）；每个集群的虚线箭头向上回指网关（出站连接）。底部：三个极小图标（容器方块、服务器、数据库），无文字。
- **图内文字**：`Gateway` / `Agent` / `Cluster A` / `Cluster B` / `Cluster C`
- **完整 prompt**（`prompts/02.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

VERTICAL THREE-LAYER SYSTEM ARCHITECTURE DIAGRAM on white, read top to bottom.

TOP LAYER: a small row of three simple entry icons, evenly spaced: a laptop, a robot head, and a browser window with a title bar. A single solid black arrow points straight down from this row.

MIDDLE LAYER: one large wide rounded-rectangle container, light grey fill with a thin black outline, centered, labelled "Gateway" in small clean letters at its top edge. Inside the container four small simple line icons sit in one evenly spaced row: a key, a shield, a route symbol of one line splitting into two arrows, and a ledger book.

BOTTOM LAYER: from the bottom edge of the Gateway container, three dashed black lines fan out downward to three equal rounded-rectangle containers placed side by side, each with a light grey fill: they are labelled "Cluster A", "Cluster B", "Cluster C". Inside each cluster container sit three small server boxes in a row. Each server box carries one small deep teal-green dot in its corner, and a delicate leader line from one single dot points to the word "Agent" placed once, small, in the empty space.

Reverse direction: from each cluster container, one dashed deep teal-green arrow curves upward back into the Gateway container, so every cluster also connects outward to the single gateway.

BELOW the clusters: a very small horizontal strip of three tiny grey icons with no text at all: a plain box, a server, a database cylinder.

Keep lots of white space between layers. Arrows thin and clean, arrowheads small triangles.

TEXT RULE: the only text allowed in the entire image is "Gateway", "Agent", "Cluster A", "Cluster B", "Cluster C", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 4 · `04-one-request.png` — 一次操作的旅程：1→5

- **讲哪个点**：文章 1.2：请求带令牌到控制面认证/授权/记账 → 路由到集群 Agent → 本机执行 → 结果与审计回写。横向五步流程。
- **画面布局**：横向五站式流水线，一条细基线贯穿，站与站之间实线箭头；大号数字 1–5 统一放在各站上方。
- **元素**：1 = 小人 + 一枚圆形令牌；2 = 大门/网关，门内三小图标（钥匙、盾、账本）；3 = 小集群（三个方块）里有一个墨绿 Agent 点；4 = 终端窗口（> 提示符 + 齿轮）；5 = 账本页 + 勾。一条虚线的回写箭头从 4/5 回到 2（结果与审计回写）。
- **图内文字**：`Token` / `Gateway` / `Agent`
- **完整 prompt**（`prompts/04-one-request.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

HORIZONTAL FIVE-STEP PROCESS DIAGRAM: a single thin black baseline runs across the middle of the image, with small arrowheads between five evenly spaced stations. Above each station, one large deep teal-green numeral, big and clear: 1, then 2, then 3, then 4, then 5.

Station 1: a tiny human figure holding one round disc-shaped token, with the small word "Token" beneath it.
Station 2: one tall gateway door with a thin frame, labelled "Gateway" beneath it, and three very small icons drawn on the door face: a key, a shield, a ledger book.
Station 3: a small cluster of three rounded squares, one square carrying a single deep teal-green dot, and the small word "Agent" beneath it.
Station 4: a simple terminal window (rounded rectangle with a tiny top bar, a small chevron prompt and a gear glyph) standing on a machine box, meaning local execution.
Station 5: an open ledger page with a thin check mark, meaning the result and the audit record.

A long dashed deep teal-green arrow curves underneath the baseline from station 4 back toward station 2, representing the result and audit write-back. Solid black baseline elsewhere.

All five stations sit on the same horizontal line, evenly spaced, each with generous white space. No other decoration.

TEXT RULE: the only text allowed in the entire image is "1", "2", "3", "4", "5", "Token", "Gateway", "Agent", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 5 · `05-seven-gates.png` — 七道门：从外走到内

- **讲哪个点**：文章第三节：一次操作依次过七道门（身份 / 目标授权 / 记账 / 凭据 / 执行边界 / AI 变更 / 升级）。门排成一排，门上数字 1-7，象征逐层收紧。
- **画面布局**：水平一排七扇门框，站在同一条细地平线上，从左到右每扇略小一点，制造纵深；每扇门框正上方的小方牌上是数字 1–7；左侧一个小人正走进第一扇门。
- **元素**：七扇门内的图标分别为：钥匙（身份）、准星/靶心（目标授权）、账本（记录）、挂锁+卡片（凭据）、一条虚线的墙与一个小方块（执行边界）、机器人头+扳手（AI 变更）、环形升级箭头（升级）。门底部一条极细虚线把七扇门串起来。
- **图内文字**：`1` / `2` / `3` / `4` / `5` / `6` / `7`
- **完整 prompt**（`prompts/05-seven-gates.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A ROW OF SEVEN DOOR FRAMES standing on one thin horizontal ground line, evenly spaced across the wide image, read left to right. Each door frame is an upright rectangle with a thin black outline and lies open; each frame shrinks very slightly from left to right to suggest depth, staying flat 2D, no perspective room, no vanishing point.

Above each door frame hangs a small square number tab containing exactly one big deep teal-green numeral, in order from left to right: 1, 2, 3, 4, 5, 6, 7. The numerals are large, clean and unambiguous.

Inside each of the seven frames, one small and simple line icon, all different, left to right:
1 a key, 2 a crosshair target, 3 an open ledger book, 4 a padlock beside a small credential card, 5 a short dashed boundary wall with one small blank box, 6 a robot head next to a wrench, 7 a circular arrow that loops back on itself.

At the far left, one tiny simple human figure walks toward the first door frame along the ground line. A very thin dotted guideline runs along the bottom from the first door to the seventh, like a path.

Everything else is empty white space. Icons are small and calm, not crowded.

TEXT RULE: the only text allowed in the entire image is "1", "2", "3", "4", "5", "6", "7", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 6 · `06-identity-credentials.png` — 身份与凭据：门里有身份，机器上无钥匙

- **讲哪个点**：文章 1.4「身份留在门里」+ 2.4「凭据只在控制面、按需下发、用完即清」：多人/程序共用一枚令牌进门，机器上没有钥匙；凭据金库只向被授权的那一台目标递出一次。
- **画面布局**：左中右三段：最左三名使用者 → 一枚令牌 → 中间 Gateway 大门 → 门下三台机器（各带一个划掉钥匙孔的符号）；右侧金库 Vault，一条墨绿虚线箭头只指向其中一台机器，箭头旁一枚文件图标。
- **元素**：左上两个小人 + 一个机器人头，下方一枚圆形令牌，实线箭头进网关；网关下方三台无门机器，每台正面一个钥匙孔，钥匙孔被一条对角斜线划掉（无钥匙）。右侧保险柜（方形柜体 + 圆形转盘把手），标 Vault；一条墨绿虚线从金库只连到最近的一台机器，途中一枚小文件图标；其余两台机器没有连线。
- **图内文字**：`Token` / `Gateway` / `Vault`
- **完整 prompt**（`prompts/06-identity-credentials.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-TO-RIGHT COMPOSITION in three zones.

ZONE 1, left: two tiny simple human figures and one small robot head standing in a row, and below them one single round disc-shaped token, labelled "Token" in small clean letters. One solid black arrow goes from the token into the gateway door in the next zone.

ZONE 2, center: one tall gateway door with a thin frame and a small handle, labelled "Gateway". Under the gateway stand three identical machine boxes arranged side by side. Each machine has no door; instead each has a small keyhole on its front face, and each keyhole is crossed out by a single thin diagonal slash, meaning no keys live on the machines.

ZONE 3, right: a large safe or vault: a square body with one big round dial handle, outlined in deep teal-green, standing on the same ground line, labelled "Vault" in small clean letters. From the vault, one dashed deep teal-green arrow travels to exactly ONE of the three machines, and along that arrow sits one small page icon, showing a single one-time credential hand-off. The other two machines receive nothing, no line at all.

Balanced spacing, calm, plenty of empty white space, no other marks.

TEXT RULE: the only text allowed in the entire image is "Token", "Gateway", "Vault", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 6 生成阶段修订（实际使用版本）

原 prompt 首次生成时上游卡死（进程 10 分钟无返回，被 Lead 终止）。按既定降级策略改写：**精简为四个稀疏区域、只保留 Token / Gateway / Vault 三个词**，并以 `--quality medium --timeout 180 --max-attempts 2` 重生成，385s 返回，构图与文字均合格，直接留用。
修订要点：左「小人 + 机器人头 + 一枚 Token 圆盘」→ 中间「Gateway 门（墨绿描边）」→ 下方「三台机器，每台正面钥匙孔被一条斜线划掉（机器上无钥匙）」→ 右侧「Vault 保险箱（墨绿描边）+ 一条墨绿虚线箭头只连到最近的一台机器」。删掉了原先的团队多人、文件图标等次要元素。

修订后 prompt 见 `prompts/06-identity-credentials.txt`（文件已按此版本覆盖），正文如下：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A SIMPLE LEFT-TO-RIGHT DIAGRAM WITH FOUR SPARSE ZONES on one shared ground line, airy and uncrowded.

Far left: one tiny human figure and one small robot head standing side by side, and directly below them a single round disc-shaped token. The small word "Token" sits under the disc.

Center-left: one tall gateway door with a thin frame and a small round handle, drawn in deep teal-green outline, with the small word "Gateway" underneath it. One short solid arrow leads from the token into the gateway.

Center-bottom: three identical plain machine boxes standing in a row. Each machine box shows one small keyhole on its front, and each keyhole is crossed out by a single thin diagonal slash, so no key lives on the machines.

Right: one safe with a square body and a big round dial handle, deep teal-green outline, with the small word "Vault" underneath it. From this safe one single dashed deep teal-green arrow runs to only the nearest machine box. No line reaches the other two machines.

Only three short words exist in the whole picture, spelled exactly: "Token", "Gateway", "Vault". Nothing else is written anywhere. Very few elements, giant empty white space, calm and clean.
```
（负面约束同上文统一负面约束，逐字复用。）

### 图 8 · `08-boundary.png` — 边界与分工：产品负责什么，部署方补什么

- **讲哪个点**：文章第四节：DoOps 负责闸门、账本、金库；加密入口、网络、审批、钥匙管理仍由部署方负责。左右两栏 + 中间虚线，表达责任分界。
- **画面布局**：左右对称双栏，中间一条竖直虚线；左栏标题 DoOps，栏内竖排三枚图标（门、账本、金库）；右栏标题 You，栏内竖排三枚图标（证书/锁、网络、审批戳）。
- **元素**：左三枚：一扇带框的门（墨绿描边）、一本账本、一个保险柜。右三枚：一张带缎带/挂锁的证书、一个由线和圆点组成的网络球、一个带勾的审批戳。图标各自独立、大小一致、上下对齐。
- **图内文字**：`DoOps` / `You`
- **完整 prompt**（`prompts/08-boundary.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A BALANCED TWO-COLUMN DIVISION-OF-RESPONSIBILITY DIAGRAM. A single thin dashed vertical line divides the canvas exactly in half, top to bottom.

LEFT COLUMN: the heading "DoOps" in small clean letters, centered at the top of the column. Below the heading, three identical rounded square tiles stacked vertically with even spacing, each tile a thin black outline with a light grey fill, each containing one simple line icon: top tile a framed gateway door, middle tile an open ledger book, bottom tile a safe with a round dial. The gateway door icon uses the deep teal-green accent outline.

RIGHT COLUMN: the heading "You" in small clean letters, centered at the top of the column. Below the heading, three identical rounded square tiles stacked vertically with even spacing, each a thin black outline with a light grey fill, each containing one simple line icon: top tile a certificate sheet with a ribbon and a small padlock, middle tile a network globe made of a few lines and nodes, bottom tile an approval stamp with a check mark. One of these three icons may use the deep teal-green accent outline, only one.

The two columns mirror each other exactly in size and vertical rhythm, calm, airy, lots of white space, no arrows, no extra marks, no other words.

TEXT RULE: the only text allowed in the entire image is "DoOps", "You", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```


## 2b. 图 7（新增章节配图）· `07-two-roles.png` — 两种角色：团队怎么管 / 开发者怎么用

> 说明：本节由 task-5 追加；Lead 已统一定稿编号：本图=图 7（`07-two-roles.png`），边界图=图 8（`08-boundary.png`），其余图顺延。
> 风格前缀、负面约束、文字规则与第 1 节完全一致，逐字复用。

- **讲哪个点**：文章新增章节「两种角色：企业/团队怎么管，开发者怎么用」——管理面与使用面共用同一扇门。左侧管理面表达**可配置、可审计、可撤销**（人 / 权限矩阵 / 账本 / 机群清单 / 钥匙令牌盘）；右侧使用面表达**装一次、日常一条命令**（人 + AI 机器人 / 终端窗口 / Git 双向箭头）；中间一扇 Gateway 门把两侧连起来：左「配门」，右「进门」。
- **画面布局**：左右两块等大圆角面板相对而立，同一条地平线；中缝正中一扇高的 Gateway 门（墨绿描边），门下写 Gateway。左面板内部 2×2 四枚图标瓦片；左面板靠门一侧挂一个钥匙盘，虚线指向门（配门）。右面板自上而下：人 + 机器人头 → 终端窗口 → Git 双向箭头；右面板出一条实线箭头进入中央门（进门）。
- **元素**：
  - 左 2×2：① 三个小人（团队）② 小型权限矩阵（格子 + 一格墨绿填充）③ 摊开的账本（审计）④ 三台服务器一排（机群纳管清单）
  - 左靠门：圆形钥匙盘 + 两把钥匙 → 墨绿虚线 → 门
  - 右：小人 + 机器人头；终端窗口（标题栏 + chevron + 一条空白命令条，**画面内不出现任何命令文字**）；Git 节点 + 双向弯箭头（一支墨绿）
  - 中：Gateway 门；右面板 → 门 的实线箭头
- **图内文字**：`Admins` / `Developers` / `Gateway`（仅此三个，无数字、无中文）
- **完整 prompt**（`prompts/07-two-roles.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A LEFT-RIGHT SPLIT DIAGRAM WITH ONE CENTRAL DOOR THAT CONNECTS BOTH SIDES, symmetrical and calm.

Two large rounded-rectangle panels of equal size face each other, one filling the left half and one filling the right half, each with a very light grey fill and a thin black outline, standing on the same thin ground line. Exactly in the middle seam between the two panels stands ONE tall gateway door with a thin frame and a small round handle, drawn with the deep teal-green accent outline, and the small word "Gateway" is placed directly under it.

LEFT PANEL, labelled "Admins" in small clean letters centered at its top edge. Inside it, four small rounded square tiles form a neat two-by-two grid with even spacing; each tile is a thin black outline with white fill and holds exactly one simple line icon:
top-left tile: a small group of three simple human figures standing together;
top-right tile: a small permission matrix, a tiny grid of squares where a few cells are marked and one single cell is filled deep teal-green;
bottom-left tile: an open ledger book with ruled lines;
bottom-right tile: a small fleet of three server boxes lined up in a row.
Just to the left of the central gateway door hangs one small round tray holding two old-fashioned keys, and one short dashed deep teal-green line runs from this key tray into the door, showing that the admins configure the single door.

RIGHT PANEL, labelled "Developers" in small clean letters centered at its top edge. Inside it, from top to bottom: one simple human figure and one small robot head standing side by side; beneath them a terminal window, a rounded rectangle with a tiny top bar, a small chevron prompt and one short blank command bar with no letters on it at all; beneath the terminal a small git symbol, a round node joined by two short curved arrows pointing in opposite directions, one arrow deep teal-green.
One solid black arrow leaves the right panel and enters the central gateway door, showing that everyday work goes through the same single door.

Very few elements in each area, big empty white space, thin clean arrows with small arrowheads, small neat lettering. Nothing else is written anywhere in the picture, and the terminal window must stay completely blank.

TEXT RULE: the only text allowed in the entire image is "Admins", "Developers", "Gateway", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

- **自检要点**：三个标签拼写无误；终端窗口内不得出现任何字母；左侧四枚图标可辨认（团队 / 矩阵 / 账本 / 机群）；门在正中、左右各一条连接关系清楚；无中文、无乱码。

### 图 7 最终采用版本（生成阶段实际使用）

按 Lead 的降级方案，本节原方案（左 2×2 四瓦片 + 钥匙盘）在读图前被压缩为 **左 3 元素 + 右 3 元素 + 中间一扇门**，并以 `--quality medium --timeout 180 --max-attempts 2` 生成（228.4s 返回），一次通过，已留用。取舍：用户提到的「钥匙/令牌盘」并入中央 Gateway 门的语义（门本身即唯一入口），不再单独画一件图标，画面更干净、也更贴近前 6 张的稀疏度。

- **最终画面**：左右两块等大圆角浅灰面板 + 正中一扇墨绿拱形 Gateway 门（门下 Gateway）。左栏 `Admins` 自上而下三元素：三个小人（团队）、3×3 权限矩阵（一格墨绿填充）、摊开的账本（审计）。右栏 `Developers` 自上而下三元素：小人 + 机器人头（人 + AI）、终端窗口（chevron + 一条空白命令条，画面内无任何字母）、Git 节点 + 双向弯箭头（一支墨绿）＝装一次、日常一条命令。右栏一条实线箭头进入中央门。
- **图内文字**：`Admins` / `Developers` / `Gateway`（仅此三个）。
- **最终 prompt**（`prompts/07-two-roles.txt` 已是此版本）：

```
Minimalist editorial technical illustration, flat vector line-art diagram. Pure white background. Thin uniform black strokes about 2px, geometric, no shading. Panels filled with very light grey (#F2F3F2) and outlined in thin black. One single accent color: deep teal-green #0E7C66, used sparingly. Generous negative space, calm Swiss infographic look, flat straight-on front view, wide horizontal composition, wide empty margins.

A SIMPLE LEFT-RIGHT SPLIT DIAGRAM: two equal rounded-rectangle panels facing each other on one thin ground line, with ONE tall gateway door standing exactly in the middle seam between them, drawn with the deep teal-green accent outline and the small word "Gateway" underneath it.

LEFT PANEL: only three elements, evenly spaced vertically. The small word "Admins" at its top center. Below it, first a group of three tiny human figures; second a small list or matrix, a neat little grid of squares with one single cell filled deep teal-green; third an open ledger book with a few ruled lines.

RIGHT PANEL: only three elements, evenly spaced vertically. The small word "Developers" at its top center. Below it, first one tiny human figure standing next to a small robot head; second a terminal window, a plain rounded rectangle with a small chevron and one blank bar and no letters inside it at all; third a small git mark, one round node joined by two short curved arrows pointing in opposite directions, one of them deep teal-green.

One solid thin black arrow goes from the right panel into the middle doorway.

Nothing else is written anywhere, the terminal stays completely blank, and the whole picture is airy with large empty white space.

TEXT RULE: the only text allowed is "Admins", "Developers", "Gateway", spelled exactly, in a clean geometric sans-serif, small and neat. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no gibberish letters, no fake text, no misspellings, no captions, no sentences, no legend, no extra numerals, no watermark, no logo, no signature, no gradients, no shadows, no 3D, no isometric view, no photorealism, no neon, no dark background, no clutter, no dense detail, no emoji, no outer frame around the canvas.
```

## 2c. 图 3（新增章节配图）· `03-deployment-topology.png` — 三种部署形态：内网 / 公网 / 分域多网关

> 说明：本节由 task-6 追加；Lead 已统一定稿编号：本图=图 3（`03-deployment-topology.png`），原 03–07 依次顺延为 04–08。风格前缀、负面约束、文字规则与第 1 节一致。
> 核心表达：**DoOps 不是必须部署在公网，Gateway 可以放内网；网络位置决定边界，而不是决定架构。**

- **讲哪个点**：同一套架构可以摆在内网、公网入口、或分域多网关三种网络位置；变的是边界画在哪里，不变的是"一扇门 + 机器主动向外连"的结构。
- **画面布局**：横向三个等宽面板，面板之间留白（不画分隔线）；每个面板顶部一个短词，下面一个大圆角**虚线边界**；三面板共用一条地平线。
- **元素**：
  - **左 Intranet（内网闭环）**：虚线边界内 = 三个小人（用户/CI）→ 墨绿 Gateway 门 → 两台机器（各带墨绿 Agent 绿点，"Agent" 用引线标注一次）；实线箭头全部在边界内，边界外空无一物。
  - **中 Public（公网入口）**：边界压低，其上边缘加粗表现"网络边界"；边界外上部 = 笔记本 / 云 / 手机三个远程客户端；**门骑在边界上**（墨绿）+ 旁边一个小挂锁＝ TLS；边界内两台机器，机器→门为**墨绿虚线出站箭头**，绝无指回机器的入站箭头。
  - **右 Multi（分域多网关）**：两个独立虚线小域并排，每域各一扇小 Gateway 门 + 一台带绿点的机器；两域之间一条短双竖线表示隔离，**不画任何跨域箭头**。
- **图内文字**：`Intranet` / `Public` / `Multi` / `Gateway` / `Agent`（各出现一次，仅此五个）。
- **完整 prompt**（`prompts/03-deployment-topology.txt`）：

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for the gateway doors, a few small dots and a few arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

THREE EQUAL-WIDTH VERTICAL PANELS SIDE BY SIDE IN ONE ROW, separated by wide empty white gutters, no dividing rules between them. Each panel has one small word centered at its top, and under that word one large rounded rectangle drawn with a DASHED black border and a very light grey fill, representing a network boundary. Each panel tells a different deployment shape.

LEFT PANEL, header word "Intranet". Inside its dashed boundary everything stays inside: three tiny simple human figures at the top left, one small gateway door in deep teal-green outline in the middle of the boundary labelled "Gateway", and two small machine boxes at the bottom right. Each machine box carries one small deep teal-green dot, and a delicate leader line points from one dot to the word "Agent" placed once inside this boundary. A solid thin arrow runs from the three figures to the gateway door, and another solid thin arrow runs from the door to the two machines. No element and no arrow sits outside this boundary.

MIDDLE PANEL, header word "Public". Its dashed boundary is placed LOW, and its TOP border line is drawn as one stronger solid vertical-horizontal edge that represents the public network edge. Above that edge, outside the boundary, stand three small remote client icons: a laptop, a cloud and a phone. Exactly ON that edge sits one small gateway door in deep teal-green outline with a tiny padlock drawn right beside it. A solid thin arrow comes from the remote clients outside and enters the gateway door. Inside the boundary, two small machine boxes each carry one small deep teal-green dot, and from each machine one DASHED deep teal-green arrow goes outward to the gateway door. Only outbound arrows exist here, no arrow points back into the machines.

RIGHT PANEL, header word "Multi". Inside it, two separate smaller dashed boundaries stand side by side, and each one holds its own small gateway door in deep teal-green outline plus one small machine box with a green dot, with a short solid arrow from the door to its machine. Between the two domains runs a short double vertical isolation line, and NO arrow ever crosses from one domain to the other. Nothing else connects them.

All three panels share one common ground line. Doors, dots and arrowheads stay small. Everything is airy and uncrowded.

TEXT RULE: the only text allowed in the entire image is "Intranet", "Public", "Multi", "Gateway", "Agent", each spelled exactly like that and appearing once, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

- **自检要点**：五个标签各一次、拼写无误；左图所有元素都在虚线内；中图门压在边界上、有锁、只有出站虚线；右图域间无互连箭线；无中文、无乱码。
- **生成结果**：`--quality high --timeout 300 --max-attempts 2`，121.7s 一次返回，未触发降级。产物 1920×819；五个标签均正确无乱码；左图虚线内闭环、边界外空白，中图门与挂锁压在边界上且只有墨绿虚线出站、无入站箭头，右图两域之间只有双竖线隔离、无任何跨域连线。**留用，未重试。**

## 3. 生成与自检记录

下表为实际生成次数与逐张自检结论（2026-11-10 回填，含后续新增的 03-deployment-topology 与 07-two-roles）。全部 8 张：无中文、无乱码、英文标签拼写正确、构图可读，均无需重画。

| # | 文件 | 生成次数 | 自检结论（乱码/中文/构图） |
|---|---|---|---|
| 1 | 01-ssh-vs-doops.png | 1 | 1752×897；文字仅 SSH / DoOps / Gateway，拼写正确；无中文、无乱码。左：三台带门机器 + 三个持钥匙的人 + 向内实线箭头 + 散落钥匙；右：三人群持一枚 Token 进墨绿大门 + 三台无门机器虚线向上外连。可用。 |
| 2 | 02-architecture.png | 1 | 1536×1024；文字 Gateway / Agent / Cluster A / Cluster B / Cluster C，全部正确；无中文、无乱码。三层结构清晰，双向箭头到位，底部目标图标行无文字。可用（本组文字最多的一张，仍无乱码）。 |
| 3 | 03-deployment-topology.png | 1 | 1920×819；文字 Intranet / Public / Multi / Gateway / Agent，各出现一次、拼写正确；无中文、无乱码。左：虚线边界内 三用户 → 墨绿门 → 两台机器（Agent 引线标注），边界外无元素；中：门骑在边界上并带挂锁，边界外笔记本/云/手机，机器→门为墨绿虚线出站箭头，无入站箭头；右：两个虚线域各一扇门 + 一台机器，域间双竖线隔离、无跨域箭头。可用。 |
| 4 | 04-one-request.png | 1 | 1920×819；文字 1-5 + Token / Gateway / Agent，全部正确；无中文、无乱码。五站流水线 + 墨绿虚线回写箭头清晰。可用。 |
| 5 | 05-seven-gates.png | 1 | 2172×724；图内仅数字 1-7，全部正确；无中文、无乱码。七扇门一排、门内七个不同图标、左侧小人入场，语义可读。可用。 |
| 6 | 06-identity-credentials.png | 2（首版上游卡死被终止，第 2 版 medium 成功） | 2149×732；文字 Token / Gateway / Vault，全部正确；无中文、无乱码。Token → Gateway、三台机器划掉的钥匙孔、Vault 虚线只连一台机器，语义完整。可用。 |
| 7 | 07-two-roles.png | 3（首版 high 上游卡死被 Lead 终止；第 2 版 4+钥匙盘版被主动放弃；第 3 版按 Lead 降级方案 3+3 精简、medium 成功） | 1774×887；文字 Admins / Developers / Gateway，拼写正确；无中文、无乱码；终端窗口内无任何字母。左栏三元素（三人形 / 3×3 权限矩阵含一格墨绿 / 摊开账本）；中间墨绿拱形 Gateway 门 + 门下 Gateway；右栏三元素（人 + 机器人头 / 终端窗口 / Git 双向弯箭头，一支墨绿），右栏一条实线箭头进门。构图清楚，可用。 |
