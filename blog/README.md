# DoOps Blog

**中文** | [English](#english) | [Русский](#русский)

本目录收录 DoOps 的对外文章，按“文章语义化命名 + 语言分文件夹”组织：

```
blog/
├── README.md                # 本索引
├── zh/                      # 中文
├── en/                      # English
├── ru/                      # Русский
│   └── after-goodbye-ssh-what-is-doops-and-security.md
├── images/                  # 配图与设计资源，三种语言共用
└── references/              # 参考资料（上一篇正文与留言归档）
```

每篇文章在各语言目录下使用相同的语义化文件名（`<slug>.md`），front matter 里用 `lang` 区分语言、`slug` 标识同一篇文章。

## 中文

- [DoOps 能做什么：业务功能、管理底座与 AI 原生](zh/doops-core-features.md)
  —— 三部分：业务功能（通道/远程运维/K8s 与交付/镜像/数据库/网站/AI）、支撑管理（身份与授权/审计与凭据/节点与 Agent/任务与可靠性/控制台与规模化）、AI 原生设计（为什么这些能力长成这样）；10 张配图。
- [再见 SSH 之后：DoOps 是什么，安全靠什么保证](zh/after-goodbye-ssh-what-is-doops-and-security.md)
  —— 逐条回应《再见，SSH。》评论区的质疑：先讲架构（一扇门、多集群、反向连接、内网/公网/分域多网关三种部署形态），
  再讲安全靠什么保证（七道门），最后说清两种角色（企业怎么管、开发者怎么用）与边界分工。8 张配图。

## English

- [What DoOps Can Do: Business Capabilities, the Management Plane, and AI-Native Design](en/doops-core-features.md)
  —— Three parts: business capabilities (connectivity, remote ops, K8s & delivery, images, database, web, AI), the management plane (identity & grants, audit & credentials, agents, reliability, console & scale), and AI-native design (why the capabilities look the way they do). 10 figures.
- [After Goodbye, SSH: What DoOps Is, and How Its Security Is Guaranteed](en/after-goodbye-ssh-what-is-doops-and-security.md)
  —— A point-by-point reply to the comments on "Goodbye, SSH.": the architecture first (one gate, many clusters,
  outbound agents, and three deployment topologies), then the seven gates behind its security, and finally the
  two roles (how teams govern it, how developers use it) and the boundary of responsibilities. 8 figures.

## Русский

- [Что умеет DoOps: бизнес-возможности, плоскость управления и AI-нативность](ru/doops-core-features.md)
  —— Три части: бизнес-возможности (подключение, удалённая эксплуатация, K8s и доставка, образы, БД, веб, AI), плоскость управления (доступы, аудит и учётные данные, агенты, надёжность, консоль и масштабирование) и AI-нативный дизайн (почему возможности выглядят именно так). 10 иллюстраций.
- [После «Прощай, SSH»: что такое DoOps и как обеспечивается безопасность](ru/after-goodbye-ssh-what-is-doops-and-security.md)
  —— Постраничный ответ комментаторам статьи «Прощай, SSH»: сначала архитектура (одни ворота, множество кластеров,
  исходящие соединения агентов, три топологии развёртывания), затем «семь дверей» безопасности, и наконец две роли
  (как управлять командой и как пользоваться разработчику) и границы ответственности. 8 иллюстраций.

## 图片与配图资源（Images）

- `images/01-…08-…png`：8 张配图，三种语言共用；
- `images/design.md`：配图设计文档（讲点、布局、统一风格前缀、负面约束、逐张 prompt 与自检记录）；
- `images/prompts/`：每张图的 prompt 原文，便于改图时直接重跑 image2。

## 参考资料（References）

- [《再见，SSH。》原文与读者留言归档（中文）](references/goodbye-ssh-wechat-article.md)：本系列上一篇的正文与评论区摘录。

## 新增语言 / Adding a language

新建语言目录（如 `ja/`），把中文版复制为 `<slug>.md`，保留 front matter 中的 `slug`、把 `lang` 改为新语言、图片路径保持 `../images/…`，
翻译正文后在上面的语言列表里加一条链接即可。
