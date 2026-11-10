# 《DoOps 能做什么：一张能力地图》配图设计（blog/images/features-design.md）

> 系列第二篇。视觉语言与同目录 `design.md`（第一篇《再见 SSH 之后》）**完全一致**：风格前缀、负面约束、文字规则逐字复用。
> 产出：8 张配图 + 本设计文档 + `prompts/features/{01..08}.txt` 中的 prompt 原文。
> 生成：image2 CLI，gpt-image-2.5-flare，`--size 1536x1024 --quality high`。

## 0. 约定

- **图内只有英文短标签**（Gateway / Agent / CLI / K8s / Helm / CI / Registry / Harbor / AI / Skill / DB / Web / Edge / Token / Grant / Audit / Vault / Queue / Files / Bash）与 1–3 个阶段数字，**绝不出现中文**；中文解释交给文章图注。
- 每张图只讲一个论点，元素克制；宁留白不堆密。
- 能力地图（图 1）用「环」表达封闭性：中心永远是一扇门，能力绕着它长。
- CLI 固定输出 1536×1024（3:2）；构图按宽幅横向节奏设计，主体居中，文章里按 16:9 裁切不伤主体。

## 1. 统一视觉规范（与 design.md 相同）

| 项 | 规定 |
|---|---|
| 风格 | 极简编辑风 / 扁平线描（flat vector line art） |
| 背景 | 纯白 #FFFFFF |
| 线条 | 细而均匀黑线约 2px，几何构成，无阴影无渐变 |
| 填充 | 容器内部极浅灰 #F2F3F2 |
| 强调色 | 单一墨绿 **#0E7C66**，只给少量门 / 关键箭头 / 一个高亮块 |
| 构图 | 正视平视，居中，四周大幅留白 |
| 禁止 | 中文/日韩文、乱码字母、长句、图例、渐变、投影、3D、等距视角、霓虹、纹理、水印、密集小字、外边框 |

### 统一风格前缀

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.
```

### 统一负面约束

```
no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 统一文字规则

```
TEXT RULE: the only text allowed in the entire image is <labels>, spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.
```

## 2. 八张图逐一设计

### 图 1 · `capability-map.png` — 能力地图：一扇门，九个能力环

- **讲哪个点**：全文总览。读者先记住形状：中心永远是那扇 Gateway 门，九块能力绕着它长，能力再多也只有一个入口。
- **画面布局**：正圆放射构图。中心一扇小门（墨绿），门外写 Gateway；九个等大圆角小方块沿隐形圆环均布，每块一枚图标 + 一个短词，用细直线连到中心门，像车轮辐条。不画外圈。
- **元素**：CLI=终端窗口；Agent=机器人头；Files=文件夹；K8s=六边形；Helm=包裹+投递弯箭头；Registry=三个堆叠箱；AI=芯片+两粒星芒；DB=数据库圆柱；Audit=摊开账本。仅终端块可用墨绿描边。
- **图内文字**：`Gateway` / `CLI` / `Agent` / `Files` / `K8s` / `Helm` / `Registry` / `AI` / `DB` / `Audit`
- **prompt 文件**：`prompts/features/01.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A RADIAL CAPABILITY MAP, perfectly centered and symmetrical. In the middle stands one small gateway door in deep teal-green outline, with the single word "Gateway" underneath it.

Around the gateway door, NINE small rounded-square icon tiles are arranged evenly on one invisible circle like a clock face, all the same size, each tile a thin black outline with a very light grey fill, and each tile holds exactly one simple line icon with one short word written underneath the tile:
1 a terminal window with a chevron, word "CLI";
2 a robot head, word "Agent";
3 a folder, word "Files";
4 a hexagon, word "K8s";
5 a stacked package with a curved delivery arrow, word "Helm";
6 a stack of three boxes, word "Registry";
7 a small chip with two sparkles, word "AI";
8 a database cylinder, word "DB";
9 an open ledger book, word "Audit".

Each tile is joined to the central gateway door by one thin straight black spoke, like a wheel. All nine spokes are the same length, forming a tidy ring. One tile only, the terminal tile, may use the deep teal-green accent outline.

Everything is evenly spaced with generous white space between tiles, the ring floats free with no outer circle drawn.

TEXT RULE: the only text allowed in the entire image is "Gateway", "CLI", "Agent", "Files", "K8s", "Helm", "Registry", "AI", "DB", "Audit", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 2 · `four-entries.png` — 四种入口：四路进，一扇门

- **讲哪个点**：入口的多样性不改变控制面：终端 / 浏览器 / 编辑器里的 AI / API 最终都收敛到一枚 Token + 一扇门。
- **画面布局**：上下结构。顶部四个入口图标横排（各带一词），四条细箭头向下向内汇聚到中间一枚圆令牌，令牌旁写 Token；令牌再出一条箭头进入下方墨绿门，门下写 Gateway；门下方收束到一个无字小终端方块。
- **元素**：CLI=终端窗口；Web=浏览器窗口；AI=编辑器窗口+星芒；API=一对花括号作插头；中间圆盘=Token；门=Gateway。
- **图内文字**：`CLI` / `Web` / `AI` / `API` / `Token` / `Gateway`
- **prompt 文件**：`prompts/features/02.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

FOUR DIFFERENT ENTRY POINTS CONVERGING INTO ONE SINGLE DOOR, read top to bottom.

TOP ROW: four small simple line icons evenly spaced across the width, each with one short word underneath it:
a terminal window with a chevron, word "CLI";
a browser window with a title bar, word "Web";
an editor window with a tiny sparkle star beside it, word "AI";
a pair of curly braces shaped like a plug, word "API".

From each of the four icons a thin solid black arrow curves down and inward, all four arrows meet at ONE small round disc in the middle of the image, and the word "Token" is written beside that disc. From the token disc one single solid arrow continues down into one gateway door drawn in deep teal-green outline, and the word "Gateway" is written underneath that door.

Below the door, a short thin line ends in a small square terminal box, left plain with no text at all.

The four entry icons, the single token and the single door form a clear funnel: four in, one gate. Thin arrows, small arrowheads, symmetrical layout, big empty white space, nothing else in the picture.

TEXT RULE: the only text allowed in the entire image is "CLI", "Web", "AI", "API", "Token", "Gateway", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 3 · `exec-files.png` — 执行与文件：命令下行，文件双向

- **讲哪个点**：Agent 通过受控通道执行命令；文件与代码在会话工作区里双向同步（push/pull），大文件另走存储桶。
- **画面布局**：左右两组，共用一条地平线。左组=执行：终端 → 实线箭头 → 机器方块里的机器人头（Agent）。右组=文件：文件夹形工作区为中心，上方文档图标与工作区之间一对方向相反的平行实线箭头；下方存储桶（宽圆柱）与工作区之间一对方向相反的墨绿虚线箭头。两组之间一条细点线分隔。
- **元素**：终端（CLI）、机器方块+机器人头（Agent）、文件夹、文档页、圆柱存储桶、两对反向箭头。
- **图内文字**：`CLI` / `Agent`
- **prompt 文件**：`prompts/features/03.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-TO-RIGHT WORKFLOW DIAGRAM IN TWO CLEAR GROUPS, sharing one thin ground line.

LEFT GROUP (execution): first a terminal window, a rounded rectangle with a small top bar and a chevron prompt, with the word "CLI" underneath it. One solid black arrow runs from the terminal to a small machine box; inside the machine box stands a tiny robot head, and the word "Agent" is written underneath the machine box.

RIGHT GROUP (files and workspace): a folder-shaped workspace box in the middle, and above it a small document icon, and below it a storage bucket drawn as a wide cylinder with a small lid. TWO PARALLEL HORIZONTAL ARROWS run between the workspace box and the document icon, one arrow pointing right and the other pointing left, showing two-way push and pull. A second pair of short parallel arrows, drawn dashed in deep teal-green, runs between the workspace box and the storage bucket below, also pointing opposite ways.

A thin dotted line separates the left group from the right group, with wide empty space on both sides. Nothing else is written anywhere.

TEXT RULE: the only text allowed in the entire image is "CLI", "Agent", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 4 · `k8s-delivery.png` — K8s 与应用交付：三段式管线

- **讲哪个点**：交付不是一把梭：preflight → apply → verify 三步，验证用徽章表达「每一步都有回执」。
- **画面布局**：横向单行。最左墨绿门（Gateway）出一条箭头进入中间三枚等大阶段方块，方块上方各一个大号墨绿数字 1 / 2 / 3；方块内依次是「剪贴板+放大镜」「包裹+向下投递箭头」「带勾的验证徽章」。第三块出一条箭头到最右：六边形集群图标（K8s）+ 旁边的小船舵（Helm）。
- **元素**：门、三段方块各一枚图标、数字 1-3、六边形集群含三个小 Pod、船舵。
- **图内文字**：`1` / `2` / `3` / `Gateway` / `K8s` / `Helm`
- **prompt 文件**：`prompts/features/04.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

HORIZONTAL THREE-STAGE DELIVERY PIPELINE WITH A GATEWAY ON THE LEFT, one thin ground line.

FAR LEFT: one small gateway door in deep teal-green outline with the word "Gateway" underneath it, and one solid thin arrow leaving it toward the right.

MIDDLE: three equal small rounded-square stage tiles in a row, joined by two short solid arrows left to right, and one large numeral above each tile in deep teal-green: 1, 2, 3.
Tile 1 holds a small clipboard with a magnifier, meaning preflight.
Tile 2 holds a small stacked package with a downward delivery arrow, meaning apply.
Tile 3 holds a round verification badge with a check mark inside, meaning verify.

FAR RIGHT: one hexagon cluster icon with three tiny pods inside it, and the word "K8s" underneath it, plus a second small ship-wheel-shaped Helm chart symbol beside the cluster with the word "Helm" underneath it. A solid arrow connects the third stage tile to this cluster.

Everything small, even and airy; the three stage tiles sit exactly in the middle; no other writing anywhere.

TEXT RULE: the only text allowed in the entire image is "1", "2", "3", "Gateway", "K8s", "Helm", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 5 · `artifacts.png` — 镜像与制品：只朝一个方向的供应链

- **讲哪个点**：镜像从 CI 出来进 registry；目标 Harbor 是**拉**不是被推——单向箭头 + 虚线表示 pull-based。
- **画面布局**：三站横排。CI（工厂+齿轮）→ 实线箭头 → Registry（三箱堆叠）→ 墨绿虚线箭头 → Harbor（集装箱+锚）；虚线箭头旁一枚「双页复制/同步」小符号。全程没有回指箭头。
- **元素**：工厂齿轮、箱堆、集装箱+锚、单向实线 + 单向虚线、复制/同步小符号。
- **图内文字**：`CI` / `Registry` / `Harbor`
- **prompt 文件**：`prompts/features/05.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-TO-RIGHT ONE-DIRECTION IMAGE SUPPLY CHAIN WITH THREE STATIONS on one thin ground line, evenly spaced.

STATION 1, far left: a small factory-and-gear icon, and underneath it the single word "CI".
A solid thin black arrow points right from it.

STATION 2, middle: a neat stack of three small boxes, and underneath it the single word "Registry".

STATION 3, far right: one shipping-crate style container with a small anchor beside it, and underneath it the single word "Harbor".

The arrow from CI to the registry box is short and solid. The arrow from the registry to the Harbor crate is longer and drawn as a DASHED deep teal-green arrow, clearly showing that the destination pulls the artifact instead of being pushed to; a tiny two-way copy symbol, one small page and one small page with a curved arrow, sits right beside that dashed arrow.

No arrow ever points backwards. Nothing else is drawn and nothing else is written.

TEXT RULE: the only text allowed in the entire image is "CI", "Registry", "Harbor", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 6 · `ai-skills.png` — AI 与 Skill：机器人只能碰白名单里的工具

- **讲哪个点**：AI 的能力边界是那张卡：Bash / K8s / Helm / Web 逐行列举并逐行打勾，Skill 作为「书库」另行提供知识。
- **画面布局**：左中右。左机器人头（AI）；中间一张竖长卡片，卡内四行 = 小图标 + 词 + 勾；一条墨绿实线箭头从机器人头指进卡片；右三本竖排书（Skill），卡片与书之间一条细双向虚线。
- **元素**：机器人头、四行白名单（终端 chevron / 六边形 / 船舵 / 地球+节点，各配一个勾）、三本书。
- **图内文字**：`AI` / `Bash` / `K8s` / `Helm` / `Web` / `Skill`
- **prompt 文件**：`prompts/features/06.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-TO-RIGHT DIAGRAM IN THREE PARTS on one thin ground line.

LEFT: one small robot head with antenna, and underneath it the single word "AI".

MIDDLE: one tall rounded-rectangle card with a thin black outline and a light grey fill; inside this card four short rows are stacked evenly, each row has one tiny line icon on the left, one short word in the middle, and a small check mark on the right:
row one: a terminal chevron, word "Bash";
row two: a hexagon, word "K8s";
row three: a ship wheel, word "Helm";
row four: a globe with two small nodes, word "Web".
One single deep teal-green arrow runs from the robot head into this card, showing the AI may only use what the list allows.

RIGHT: a stack of three books standing upright, and underneath them the single word "Skill". A thin two-way dashed arrow connects the card to the book stack.

Everything small and tidy, lots of empty space, no other writing.

TEXT RULE: the only text allowed in the entire image is "AI", "Bash", "K8s", "Helm", "Web", "Skill", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 7 · `data-web.png` — 数据库与网站：跨一层网络边界

- **讲哪个点**：数据库与网站都在内网侧；外网访客经边界上的 Edge 反向代理进来，流量单向向内。
- **画面布局**：一条竖直虚线=网络边界。左侧内网：五个圆柱横排（DB）与一个网站窗口（Web）；边界线上一个小方块节点（Edge，墨绿）；右侧外网：两个人 + 一台笔记本，一条实线箭头穿过 Edge 进入网站；没有反向箭头。
- **元素**：五个数据库圆柱、浏览器/网站窗口、边界上的墨绿节点、两个访客、单向箭头。
- **图内文字**：`DB` / `Web` / `Edge`
- **prompt 文件**：`prompts/features/07.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

LEFT-TO-RIGHT DIAGRAM CROSSING ONE NETWORK BOUNDARY.

A single vertical dashed black line divides the picture into an inner side on the left and an outer side on the right.

INNER SIDE, left: a row of FIVE small database cylinders standing side by side, each cylinder the same size with one thin band, and the single word "DB" underneath the row. To the right of the cylinders stands one website window with a title bar and a small globe in its corner, the single word "Web" underneath it.

ON THE BOUNDARY: a small square node sits exactly on the dashed vertical line, drawn in deep teal-green, with the single word "Edge" underneath it. One solid arrow goes from the outer side, through this node on the boundary, into the website window.

OUTER SIDE, right: two tiny simple human figures with one small laptop, representing remote visitors, and one solid thin arrow from them into the node on the boundary. No arrow points from the inner side back to the outer side.

Everything calm and sparse, one thin ground line, plenty of white space, nothing else written.

TEXT RULE: the only text allowed in the entire image is "DB", "Web", "Edge", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 8 · `governance-scale.png` — 治理与规模化：五件套 + 多集群汇入一扇门

- **讲哪个点**：治理不是模块堆叠，而是五件套：Token 认人、Grant 授权、Audit 留痕、Vault 存凭据、Queue 控节奏；集群再多也汇入同一扇门。
- **画面布局**：上下两层。底层五枚等大方块横排，各一枚图标 + 一个词；五条细虚线向上汇聚到上层一扇墨绿门（Gateway）；门上方三个六边形集群各出一条虚线箭头进同一扇门。
- **元素**：圆盘、钥匙+靶环、账本、保险柜、闸机+虚线队列、门、三个六边形集群。
- **图内文字**：`Token` / `Grant` / `Audit` / `Vault` / `Queue` / `Gateway`
- **prompt 文件**：`prompts/features/08.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

TWO-LAYER GOVERNANCE AND SCALE DIAGRAM, read bottom to top.

BOTTOM ROW: five small rounded-square tiles evenly spaced across the width, each one a thin black outline with a very light grey fill and exactly one simple icon plus one short word underneath:
tile one a round disc, word "Token";
tile two a key beside a small target ring, word "Grant";
tile three an open ledger book, word "Audit";
tile four a safe with a round dial, word "Vault";
tile five a small gate barrier with a dashed queue line, word "Queue".

ABOVE THE TILES: five short thin dashed arrows rise from the tiles and converge into ONE small gateway door drawn in deep teal-green outline, and the single word "Gateway" is written beside it.
Above and around that gateway door, THREE small hexagon cluster icons send three further short dashed arrows into the same door, so many clusters funnel into a single gate.

Symmetrical, airy, thin arrows with small arrowheads, large empty white space, nothing else in the picture.

TEXT RULE: the only text allowed in the entire image is "Token", "Grant", "Audit", "Vault", "Queue", "Gateway", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```
## 2b. 两张总览图（补充）· 业务能力地图 / 管理能力地图

> 本节由 task-15 追加，用于功能全景文章重构后的总览位。风格前缀、负面约束、文字规则与第 1 节完全一致。
> 两张图分工：**business-map 讲「用 DoOps 能做成什么」，management-map 讲「这些东西靠什么被管住」**。

### 图 9 · `business-map.png` — 业务能力地图：七种事，一个 DoOps

- **讲哪个点**：读者最关心"我能拿它干什么"。七块业务能力（SSH 执行 / K8s 操作 / Helm 交付 / 镜像制品 / SQL 数据 / Web 发布 / AI 协作）绕着中心的 DoOps 排成环，一眼看出覆盖面与唯一入口。
- **画面布局**：正圆放射。中心一块浅灰方块，内嵌一扇墨绿小门，方块下方写 `DoOps`；七块等大圆角方瓦沿隐形圆等角均布，每块一枚图标 + 一个词，各用一条细直线连到中心，像车轮辐条；不画外圈。
- **元素**：SSH=带钥匙的终端窗口；K8s=六边形；Helm=船舵；Images=三只带标签的集装箱；SQL=两箍数据库圆柱；Web=带地球的浏览器窗口；AI=带两粒星芒的芯片。七个图标刻意做成七种不同轮廓（窗 / 六边形 / 轮 / 箱 / 柱 / 浏览器 / 芯片），缩到手机屏也能区分。
- **图内文字**：`DoOps` / `SSH` / `K8s` / `Helm` / `Images` / `SQL` / `Web` / `AI`
- **prompt 文件**：`prompts/features/09-business-map.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A RADIAL BUSINESS CAPABILITY MAP, centered and symmetrical. In the middle sits ONE rounded-square tile with a light grey fill, holding one small gateway door icon drawn in deep teal-green outline, and the single word "DoOps" written directly underneath that central tile.

Around the centre, SEVEN equal rounded-square tiles are arranged evenly on one invisible circle, each tile with a thin black outline and a very light grey fill, each holding exactly one simple line icon and one short word written underneath the tile:
a terminal window with a tiny key drawn inside it, word "SSH";
a hexagon, word "K8s";
a ship wheel, word "Helm";
a stack of three shipping crates with a small tag, word "Images";
a database cylinder with two bands, word "SQL";
a browser window with a globe in its corner, word "Web";
a microchip with two small sparkles, word "AI".

Each of the seven tiles is joined to the central tile by one thin straight black spoke, so the seven spokes form a tidy wheel with the hub in the middle. Every tile is the same size and the gaps between tiles are equal.

The seven icons must look clearly different from each other at a glance: a window, a hexagon, a wheel, crates, a cylinder, a browser, a chip. Keep everything small and airy, no outer circle drawn, no other marks anywhere in the picture.

TEXT RULE: the only text allowed in the entire image is "DoOps", "SSH", "K8s", "Helm", "Images", "SQL", "Web", "AI", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

### 图 10 · `management-map.png` — 管理能力地图：八件事，一个底座

- **讲哪个点**：安全与治理不是散装的模块，而是八项管理能力全部汇聚到同一个控制底座（账本 + 那一扇门）。图中刻意不画瓦片之间的连线——它们不互相调用，只共同指向底座。
- **画面布局**：上下结构。顶部八个等大方瓦一字排开；底部中央一块宽而低的圆角底座板（浅灰填充），板内并排两枚图标：摊开的账本 + 墨绿小门，底座不写字；八条等长细虚线自各瓦片下沿向内下方汇聚，终点全部落在底座上沿，形成整齐的扇面。
- **元素**：Users=两个小人；Token=带锁孔的圆盘；Grant=钥匙+靶环；Audit=账本；Vault=转盘保险柜；Agents=方块里的机器人头；Queue=闸机+虚线队列；API=花括号插头；底座=账本 + 墨绿门。
- **图内文字**：`Users` / `Token` / `Grant` / `Audit` / `Vault` / `Agents` / `Queue` / `API`
- **prompt 文件**：`prompts/features/10-management-map.txt`

```
Minimalist editorial technical illustration, flat vector line-art diagram for a professional magazine article. Pure white background (#FFFFFF). Thin uniform black strokes about 2px, geometric construction, no shading, no texture. Large containers filled with very light warm grey (#F2F3F2) and outlined in thin black. Exactly one accent color: deep teal-green #0E7C66, used sparingly for one highlighted element or for a few small arrows. Generous negative space, calm Swiss infographic aesthetic, flat straight-on front elevation view, wide horizontal composition, content centered with wide empty margins on all sides.

A FAN-IN MANAGEMENT MAP: eight small capabilities above, all feeding one base below, centred and symmetrical.

TOP: EIGHT equal small rounded-square tiles in ONE horizontal row, evenly spaced across the width, each with a thin black outline, a very light grey fill, exactly one simple line icon and one short word underneath it, left to right:
two tiny human figures side by side, word "Users";
a round disc with a small keyhole, word "Token";
a key beside a small target ring, word "Grant";
an open ledger book with ruled lines, word "Audit";
a safe with a round dial, word "Vault";
a small robot head inside a plain box, word "Agents";
a barrier gate with a short dashed queue line, word "Queue";
a pair of curly braces shaped like a plug, word "API".

BOTTOM CENTRE: one wide, low rounded rectangle like a control base plate, very light grey fill with a thin black outline, sitting level on a thin ground line. Inside this base plate, two small icons side by side: an open ledger book and a small gateway door drawn in deep teal-green outline. No word is written on the base plate.

CONNECTIONS: from each of the eight tiles one thin DASHED black arrow runs downward and inward and ends on the top edge of the base plate, so all eight converge into the single base. Every arrow is the same length as its neighbours, forming a neat fan. No arrow or line joins the tiles to each other.

Big empty white space between the row and the base plate, small neat lettering, nothing else written anywhere.

TEXT RULE: the only text allowed in the entire image is "Users", "Token", "Grant", "Audit", "Vault", "Agents", "Queue", "API", spelled exactly like that, rendered in a clean geometric sans-serif, small and neat, lots of space around it. Invent nothing else.

no Chinese characters, no CJK glyphs, no Japanese, no Korean, no Arabic script, no gibberish letters, no fake text, no lorem ipsum, no misspellings, no small captions, no long sentences, no paragraphs, no legend, no annotations beyond the exact labels listed, no extra numerals, no watermarks, no logos, no signatures, no gradients, no drop shadows, no 3D, no isometric view, no photorealism, no neon, no glow, no dark background, no paper texture, no clutter, no dense detail, no emoji, no clip-art, no outer frame or border around the canvas.
```

## 3. 生成与自检记录

| # | 文件 | 生成次数 | 自检结论（乱码/中文/构图） |
|---|---|---|---|
| 1 | capability-map.png | 1（160.6s） | 1536×1024；标签 Gateway / CLI / Agent / Files / K8s / Helm / Registry / AI / DB / Audit 全部拼写正确，无中文无乱码。中心墨绿拱门 + 九块图标瓦片用辐条连成环，CLI 块为墨绿描边。可用；九块瓦片疏密略不匀（非严格等角），是最值得重画的一张。 |
| 2 | four-entries.png | 1（68.8s） | 1536×1024；标签 CLI / Web / AI / API / Token / Gateway 全部正确，无中文无乱码。四入口四条弧线汇聚到一枚 Token 圆盘，再进墨绿门。可用；门形为圆角矩形+门点，与 01/08 的拱门略有差异。 |
| 3 | exec-files.png | 1（41.3s） | 1774×887；标签 CLI / Agent 正确，无中文无乱码。左：终端 → 机器方块内机器人头；右：文件夹与文档一对反向实线箭头、文件夹与存储桶一对反向墨绿虚线箭头。可用。 |
| 4 | k8s-delivery.png | 1（77.3s） | 2172×724；标签 1 / 2 / 3 / Gateway / K8s / Helm 正确，无中文无乱码。门 → 三段管线（剪贴板+放大镜、包裹+投递箭头、带勾徽章）→ 六边形集群 + 船舵。可用。 |
| 5 | artifacts.png | 1（164.4s） | 2172×724；标签 CI / Registry / Harbor 正确，无中文无乱码。CI 工厂齿轮 → 实线 → Registry 箱堆 → 墨绿虚线 → Harbor 集装箱+锚，虚线旁有复制/同步小符号，全程单向无回指。可用。 |
| 6 | ai-skills.png | 1（64.9s） | 1774×887；标签 AI / Bash / K8s / Helm / Web / Skill 全部正确，无中文无乱码。机器人头 → 白名单卡片（四行图标+词+勾）↔ 三本书。可用，本组文字最密的一张仍无乱码。 |
| 7 | data-web.png | 1（59.2s） | 2172×724；标签 DB / Web / Edge 正确，无中文无乱码。五个圆柱 + 网站窗口在内网侧，竖直虚线为边界，墨绿节点 Edge 骑在边界上，外网两人+笔记本单向进入。可用；图内流向为右→左。 |
| 8 | governance-scale.png | 1（57.9s） | 1752×897；标签 Token / Grant / Audit / Vault / Queue / Gateway 全部正确，无中文无乱码。底层五枚能力块五条虚线汇聚进墨绿拱门，上层三组六边形集群也汇入同一扇门。可用。 |
| 9 | business-map.png | 1（35.1s） | 1536×1024；标签 DoOps / SSH / K8s / Helm / Images / SQL / Web / AI 全部拼写正确，无中文无乱码。中心 DoOps 方块内墨绿小门，七块等大瓦片辐条成环；七个图标轮廓（窗口/六边形/船舵/集装箱/圆柱/浏览器/芯片）互不混淆。可用。 |
| 10 | management-map.png | 1（24.1s） | 1774×887；标签 Users / Token / Grant / Audit / Vault / Agents / Queue / API 全部拼写正确，无中文无乱码。八块一字排开，八条等长虚线向内汇聚到中央底座（板内为墨绿账本 + 拱门），底座不写字。可用。 |

## 4. 结论与建议

- 共 10 张全部**一次生成即合格**，无需重试：无中文、无乱码、英文标签逐个核对拼写正确，构图可读，留白与前 8 张一致。
- 两张总览图（`business-map.png` / `management-map.png`）作为功能全景文章的"业务面 / 管理面"门面，均一次通过；business-map 的环略欠严格等角但七块大小一致、辐条匀称，无需重画。
- **建议优先重画**：`capability-map.png`（图 1）——九块能力瓦片沿环的分布略不匀（不是严格等角），作为封面级"地图"值得再出一版把环摆正；其余 7 张可直接用。
- 次要可选项：`four-entries.png` 的门是圆角矩形而非拱门，若要与 `capability-map` / `governance-scale` 的门型完全统一，可顺带重出。
- `data-web.png` 内部流向为右→左（外网在右、内网在左），与其它图左→右的阅读习惯相反；若文章段落先讲"外网访客"再讲"内网服务"，可接受，否则可镜像重出。

