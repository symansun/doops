# DoAgent 准入缺陷修复发布 Runbook（doops v1.3.127 + doagent 0.2.x + zyj Agent 升级）

状态：只读分析产物（2026-11-10）。本文件不执行任何发布/部署动作，只给出可执行命令序列、证据分级与决策点。
范围：zyj 事故（`execution_outcome_unknown: prompt_turn_id_missing`）的根因修复制品（A1 doops 侧、
A3 doagent 侧）从 tag 到 zyj Agent 升级的完整链路。

证据分级（全文命令与结论均带标记）：

- **[确证]**：来自仓库文件/提交/脚本源码，给出 `path:line`。
- **[Git 确证]**：来自本机 git 对象与 ref 的直接读取（`git rev-parse`/`merge-base`/`diff`）。
- **[用户提供]**：来自本次任务书/Lead 现场回读，未在仓库内复核。
- **[推断]**：按代码常量、stage 结构或惯例推断，未经本次执行验证；执行前必须回读确认。

---

## 0. 结论摘要

| 项 | 结论 | 分级 |
|---|---|---|
| 生产线 | 发布只能在 `cnb/main`（v1.3.x 线）。本机 `main` 是 GitHub v0.2.2 线，与 `cnb/main` **无共同 merge-base**，禁止从本机 `main` 打 tag | [Git 确证] |
| 当前生产 tip | `cnb/main` = `66769eef`（2026-11-09 16:23 +08，本地 ref 可能过期，需重新 fetch） | [Git 确证] |
| 最新生产 tag | `v1.3.126` = `76abae08`（完整 `76abae08a36d920c0058486d2d8ab86880c4c121`） | [Git 确证] |
| 下一版本号 | doops `v1.3.127`（任务书建议）；doagent `0.2.45`（建议，因 0.2.43/0.2.44 已被占用；待决策 D1） | [推断]/[待决策] |
| 发布触发 | 打 tag 不再触发发布。必须在 tag 上执行 `cnb build start-build --repo l8ai/ai/doops.sh --tag vX.Y.Z --event api_trigger_release` | [确证] `.cnb.yml:313-316` |
| Release Asset 与 Skill | CLI/Gateway 二进制 + `checksums.txt` 经 CNB Release 分发；Skill/docs 不单独分发，由 `install.sh` 从发布 commit 的本地 git 对象 `git archive` 取出后装入项目 | [确证] `backend/scripts/install.sh`、`publish-cli-release.mjs` |
| do-agent pin 位置 | `backend/runtime-versions.env`（7 行，含 version/revision/index digest/platform digest/image ref/base image）；`backend/Dockerfile` 以 build-arg 注入并写 OCI label | [确证] |
| 镜像链路 | doagent tag push CI 构建 `docker.cnb.cool/l8ai/ai/doagent:v0.2.x`（含 index）→ doops 发布 CI 用 digest pin 构建 `docker.cnb.cool/l8ai/ai/doops.sh:v1.3.127` → zyj 节点拉取该 digest | [确证] 脚本；基线镜像 pin 变更会导致 base-light 重建（约 200MB 重新下载）[确证] `resolve-agent-base-image.sh:1-5,16-23` |
| zyj 基线 | Agent `081f24ab` = v1.3.124；Helm release `doops-agent` rev49；chart `doops-agent-0.3.0`（仓库 chart version 0.3.0 [确证]）；运行镜像 digest `sha256:352fa6ef...` [用户提供] | 见分级 |
| 关键耦合 | 发起升级的 CLI 必须与候选 Agent 镜像同一次发布（同 commit），否则触发 `release commit mismatch` 并在第 3 条保证下自动回滚 | [确证] `BUILD_DEPLOY_SOP.md:253-257`、`CLUSTER_INSTALL_GUIDE.md:34-35` |
| 当前阻塞 | Gateway 用户 token 已过期 `gateway_auth_expired`，执行前必须先重新 `doops login` | [用户提供]（事故记录 6.1） |

---

## 1. 生产线与分支事实（发布前必须回读）

```bash
# [Git 确证] 只读核对；不要用本机 main 打 tag
git -C /Users/wwyz/Documents/l8ai/doops.sh fetch cnb --tags
git rev-parse cnb/main                    # 期望发布基线（本次分析时为 66769eef）
git log --oneline -5 cnb/main
git tag --sort=-v:refname | head -3       # 期望最新为 v1.3.126
git merge-base main cnb/main              # 本机实测：无输出、exit 1 → 两条独立历史线
```

事实：

- `v1.3.126..cnb/main` 之间有 58 个 commit，但**树差异仅 19 个文件**（README/LICENSE/图片 + 3 处运行代码修复 + 测试）：
  `git diff --stat v1.3.126 cnb/main` → `19 files changed, 2470 insertions(+), 183 deletions(-)` [Git 确证]。
  即下一 tag 除 A1 外还会携带：`dosql_direct_exec.go`（MySQL dry-run 拒绝）、`tunnel_hub.go`（relay cancel 饥饿）、`pull.go`（远端工作区快照）三项未发布修复。
- A1 分支 `doops-admission-remediation`（worktree `.worktrees/a1-remediation`）HEAD = `1ef51421`，基于 `cnb/main` tip（`git rev-list --count cnb/main..doops-admission-remediation` = 11，且 `cnb/main` 是其后代祖先）[Git 确证]。
  ⚠ 该判断在本次分析时成立；发布前必须重新 fetch 并重算，不能沿用本记录。
- A1 的 11 个 commit 含 3 个修复 + 2 个测试 + 6 个 docs/design 记录（`git rev-list --oneline cnb/main..doops-admission-remediation`）[Git 确证]。

---

## 2. do-agent pin 与镜像链路

### 2.1 pin 位置（全部确证）

`backend/runtime-versions.env` 当前 7 行：

```text
DO_AGENT_VERSION=0.2.42
DO_AGENT_REVISION=08ecb0193767f559844d985cc7365484c61bf9c5
DO_AGENT_INDEX_DIGEST=sha256:af945b8e1f983cff44fbcd35df14b26d79654ede3738a7184f3f4cbb78050371
DO_AGENT_PLATFORM=linux/amd64
DO_AGENT_PLATFORM_DIGEST=sha256:6dd8a5a2f2ed243d5efe11d023748bba81ed69dfb85faad8d0cd1e2869ee9a0f
DO_AGENT_IMAGE=docker.cnb.cool/l8ai/ai/doagent:v0.2.42@sha256:af945b...
DOOPS_AGENT_BASE_IMAGE=docker.cnb.cool/l8ai/ai/doops.sh/base-light@sha256:906cbefa...
```

使用方式：

- `backend/Dockerfile:1-9`：`ARG DO_AGENT_IMAGE/DO_AGENT_VERSION/DO_AGENT_REVISION/DO_AGENT_INDEX_DIGEST/DO_AGENT_PLATFORM/DO_AGENT_PLATFORM_DIGEST`。
- `backend/Dockerfile:34-45`：doagent 阶段 `FROM ${DO_AGENT_IMAGE}`，并写 label（`org.l8ai.doagent.*`）用于溯源。
- `backend/scripts/build-agent-image.sh`：CI 内先 `docker pull` 并执行 `do-agent --version`、比对 `image config revision` 与 pin，再构建 `doops.sh:<tag>` 并（发布/主 CI 时）推 `latest`。
- 契约测试 `backend/test/test_doagent_runtime_lock.py:13-25`：断言 revision 40 位、`DO_AGENT_VERSION` 等于 **硬编码 `0.2.42`**、`DO_AGENT_IMAGE` 以 `@DO_AGENT_INDEX_DIGEST` 结尾。
  ⚠ **bump pin 的同一个 commit 必须把该硬编码改为新版本号**，否则契约测试失败、发布流水线在第一道 stage 就失败。
- `backend/scripts/resolve-agent-base-image.sh:16-23`：base-light 输入 hash = `Dockerfile.base.light` + `runtime-versions.env`（去掉 `DOOPS_AGENT_BASE_IMAGE`）。**bump doagent pin 会改变输入 hash → base-light 重建**，每个 target 需重新下载约 200MB（脚本注释 `resolve-agent-base-image.sh:1-5`）[确证]。
  → 因此 **不要**手工改 `DOOPS_AGENT_BASE_IMAGE`（CI 用 resolve 的 override，见 `build-agent-image.sh` 中 `DOOPS_AGENT_BASE_IMAGE_OVERRIDE`）。

### 2.2 doagent 侧发布（doagent 仓库）

动作（由 A3 负责人在 doagent 仓库执行；本 runbook 不执行）：

1. 把 A3 分支 rebase 到 doagent `origin/main`（当前 `0.2.44`，tag `v0.2.44` = `b30d9e0`），解决冲突。
   A3 的 3 个 commit：`bef0d9e`、`8cfd28a`、`30cadc8`，基于 `08ecb01`（0.2.42）。
   **与 0.2.43/0.2.44 的改动文件有重叠**（`src/acp/handlers/session.rs`、`http_server.rs`、`tests.rs` 等），rebase 冲突与测试适配概率高 [Git 确证]。
2. 提交版本 bump（仅 `Cargo.toml` + `Cargo.lock`，参照 `v0.2.44` 的 `b30d9e0`）到新版本号（建议 `0.2.45`）。
3. 打 annotated tag `v0.2.45`（tag message 惯例：`DoAgent 0.2.45: <summary>`）并 push。
4. tag push 自动触发 `tag_push` 流水线（`.cnb.yml:27-130`，确证）：
   - `validate release version`：`Cargo.toml` version 必须等于 tag（`v<version>`）；
   - `run release gate`：完整测试（cap 30m）；
   - 桌面端交叉编译与打包（cap 40m）；Linux 原生构建（cap 20m）；
   - `reject existing version image`：同名 tag 镜像已存在则失败（tag 不可复用）；
   - 构建/验证/推送 `docker.cnb.cool/l8ai/ai/doagent:v0.2.45`、`v0.2.45-<short>`、`<short>`、`latest`；
   - 创建 CNB release 并上传桌面制品。
5. 发布后解析 digest（**推断**，仓库内无记录该步骤的脚本/文档；本机 docker+buildx 可用 [确证] 本机工具，docker 已登录 `docker.cnb.cool`）：
   ```bash
   docker buildx imagetools inspect docker.cnb.cool/l8ai/ai/doagent:v0.2.45
   # 记录 Digest:（index）与 linux/amd64 的 Digest:（platform manifest）
   ```
6. 在 doops 仓库提交 release prep：更新 `backend/runtime-versions.env` 的 6 个 doagent 字段 + 更新 `test_doagent_runtime_lock.py` 版本断言。源 commit 记录为 doagent tag 的 commit（即 `DO_AGENT_REVISION`）。

### 2.3 Harbor 复制（仅在目标确认走 Harbor 时）

- 规范 `backend/docs/HARBOR_REPLICATION_SPEC.md` 状态是 **草案**；V1-V7 只在 `gw-edu-coder` 实测，腾讯 ZYJ Harbor `repo.aiedulab.cn` 只出现在矩阵（`HARBOR_REPLICATION_SPEC.md:12-16`），**未验证** [确证]。
- 文档曾引用 `sync_cnb_release_image`/`promote-staged-images.sh`，但当前树中不存在（本机审计 `.local/doc-audit/core-findings.md` F26 判为 STALE）[确证]。
- 结论：**若 zyj 现有 Deployment 的 image 指向 `repo.aiedulab.cn`，本 runbook 的镜像链路需增加一段未验证的 Harbor 复制步骤，必须先升级为决策项 D5，不得直接执行。**

---

## 3. doops v1.3.127 发布链（CI 内完成，人工只做 tag 与触发）

### 3.1 前置 gate（全部 [确证] 或 [推断] 标注）

1. [决定/确证] 新 tag 号与 tag message：任务书给 `v1.3.127`；tag 惯例 `DoOps v1.3.127: <summary>`（历史 tag 均为 annotated，tag message 见 `git for-each-ref refs/tags/v1.3.126`）。
2. [确证] A1 已合入 `cnb/main`，且 `cnb/main` = 待发布 commit。
3. [确证] doagent pin bump + 契约测试已同步合入。
4. [推断] `cnb/main` push CI 绿（候选镜像以 40 位 commit 为 tag，见 `.cnb.yml:56-118`）。若团队节省时间，可直接以 tag 发布流水线为准，但 SOP 要求先有 main 候选构建（`BUILD_DEPLOY_SOP.md:160-166`）。
5. [推断] 执行者需要 `l8ai/ai/doops.sh` 的 tag push 与 pipeline 触发权限（仓库内无权限矩阵文档；本机 `cnb` CLI 已安装 [确证] 本机工具，登录态与权限未验证）。
6. [确证] 发布前检查：`python3 -m pytest backend/test -q`、`go test ./...`（三个模块）、`pnpm test:gateway-policy && pnpm test:console && pnpm exec tsc --noEmit && pnpm build` 与 CI 相同（`.cnb.yml:348-369`）。
   ⚠ 本机 `backend/test/__pycache__` 存在与源码不一致的缓存（本次分析实测：`.cnb.yml` 相关断言未执行却被 pytest 判 PASSED）→ **本地测试结果不可作为发布证据，以 CNB CI 为准** [本机观察]。

### 3.2 打 tag 并触发发布

```bash
# 1) 在正确的基线上创建 annotated tag（示例，执行前确认 <commit> = cnb/main）
git -C /Users/wwyz/Documents/l8ai/doops.sh fetch cnb --tags
git -C /Users/wwyz/Documents/l8ai/doops.sh rev-parse cnb/main
git -C /Users/wwyz/Documents/l8ai/doops.sh tag -a v1.3.127 -m "DoOps v1.3.127: prompt admission classification and queue cancellation"
git -C /Users/wwyz/Documents/l8ai/doops.sh push cnb v1.3.127

# 2) 在 tag 上启动版本发布流水线（push tag 本身不触发发布）
cnb build start-build --repo l8ai/ai/doops.sh --tag v1.3.127 --event api_trigger_release
```

来源：`.cnb.yml:313-316` 明确给出该命令与"pushed tag no longer starts the release"。[确证]（命令文本来自注释；本机未执行）
触发命令的额外参数（如指定分支/名称）未经本次验证 → [推断]。

### 3.3 发布流水线 stage 与验收项（`.cnb.yml:317-404`，[确证]）

| # | stage | 作用 |
|---|---|---|
| 1 | require a release tag | 必须是 tag 且形如 `vX.Y.Z`（`.cnb.yml:341-345`） |
| 2 | prepare tools | bash/curl/docker-cli/git/helm/node/npm/python3/pytest |
| 3 | release contract tests | `python3 -m pytest backend/test -q` |
| 4 | agent tests | `cd backend/agent && go test ./...` |
| 5 | gateway tests | `cd backend/gateway && go test ./...` |
| 6 | cli tests | `cd backend/cli && go test ./...` |
| 7 | web build | `pnpm install --frozen-lockfile && pnpm test:gateway-policy && pnpm test:console && pnpm exec tsc --noEmit && pnpm build` |
| 8 | resolve release agent base | base-light 输入 hash 命中则复用，否则构建（bump pin 后必重建） |
| 9 | build release agent image | `bash backend/scripts/build-agent-image.sh`（server-side 校验 doagent 版本/revision） |
| 10 | build release web image | `bash backend/scripts/build-web-image.sh` |
| 11 | build CLI release assets | `bash backend/scripts/build-cli.sh --all`（4 平台 CLI + gateway 资产） |
| 12 | publish CLI release assets | `node backend/scripts/publish-cli-release.mjs --repo l8ai/ai/doops.sh --tag $CNB_BRANCH --commit $CNB_COMMIT --dir ... --image docker.cnb.cool/l8ai/ai/doops.sh:$CNB_BRANCH`（使用 CI 内置 `CNB_TOKEN`） |
| 13 | verify published CLI release assets | `bash backend/scripts/verify-published-cli-release.sh`：逐个下载 5 个二进制 + checksums，校验 sha256，并执行 linux 二进制校验 `release=<tag> commit=<40位commit>` 与 `doops.sh/cli-contract/v3` |

Release Asset 清单（`publish-cli-release.mjs:7-15`）：`doops-darwin-amd64`、`doops-darwin-arm64`、`doops-linux-amd64`、`doops-linux-arm64`、`doops-gateway-linux-amd64`、`checksums.txt`。
下载 URL 形态：`https://cnb.cool/<repo>/-/releases/download/<tag>/<asset>`（`verify-published-cli-release.sh:23-28`）。

### 3.4 发布后取镜像 digest（[推断] 命令，工具本机可用 [确证]）

```bash
docker buildx imagetools inspect docker.cnb.cool/l8ai/ai/doops.sh:v1.3.127
# 记录：
#   Digest:（OCI index，参考 v1.3.123 记录：index 5c6055a7...，平台镜像 fd20c510...）
#   linux/amd64 Digest:（平台 manifest）
```

历史记录（`docs/reviews/2026-11-08-doagent-042-all-environments.md:18-21`）同时记录平台镜像 digest 与对应 OCI index；两者都可能被 containerd 作为 CRI `imageID` 上报（"Oilan、ZYJ 的 containerd 报告 index，其他节点报告平台 manifest"，同文 24-26）。升级 Job 的校验同时接受"固定 digest"与"Job 运行时 digest"（`self_upgrade.go:1098-1132`）[确证]。
→ 建议 pin **OCI index digest**（若存在），并同时记录平台 manifest digest；两者差异不构成发布失败。

### 3.5 CLI / Skill 分发（操作员机器）

```bash
# [确证] 先 fetch tag，脚本不会自动 fetch；缺少发布提交会报错退出
cd /Users/wwyz/Documents/l8ai/doops.sh
git fetch cnb --tags
bash backend/scripts/install.sh --version v1.3.127 --project <项目绝对路径>

# [确证] 安装结果
~/.local/bin/doops version        # release=v1.3.127 commit=<CNB_COMMIT 40 位>
~/.local/share/doops/install.txt  # skill_commit=<同一 commit>；记录全部 skill_path
```

- Skill/docs 来源：`install.sh` 用 `git archive <release commit> -- backend/skills/SKILL.md backend/docs` 从**本地 git 对象**取出，不复制当前工作区（`install.sh:263-298`；`CLUSTER_INSTALL_GUIDE.md:49-60,75-99`）[确证]。
- 安装目标：`~/.local/bin/doops`、`<项目>/.agent/skills/doops/`、`~/.agent/skills/doops/`，并更新已存在的 `.codex/skills/doops`/`.agents/skills/doops`（`CLUSTER_INSTALL_GUIDE.md:80-85`）[确证]。
- 回滚：替换失败时按逆序恢复原 CLI/Skill/回执；首次安装移除新增文件（`CLUSTER_INSTALL_GUIDE.md:93-98`）[确证]。

---

## 4. zyj Agent 升级（可执行步骤 + 验证 + 回滚点）

### 4.1 目标基线（执行前必须回读）

| 项 | 值 | 分级 |
|---|---|---|
| Gateway target | configured target `zyj`（`doops targets` 显示 CLUSTER `doops-zyj` / INSTANCE `zyj`） | 事故记录/证据 [确证] |
| Helm release | `doops-agent`，namespace `ai`，rev49（升级后应为 rev50） | [用户提供]；rev50 为 [推断] |
| Chart | `doops-agent-0.3.0` | [用户提供]；仓库 `Chart.yaml` version 0.3.0 [确证] |
| 当前 Agent commit | `081f24ab4e92535bce87c23889e242a1ee5f0886`（v1.3.124） | 证据 `targets-zyj-after-recovery.txt` [确证] |
| 当前运行镜像 | `sha256:352fa6eff36cc19602612bd7f00d2bfa84c47c7694bed482033fafa2c9cd2a6a` | [用户提供]，仓库无此值 |
| 当前 CLI | 管理机已装 `v1.3.126` = `76abae08` | `doops version` [确证] |

### 4.2 前置条件

1. **Gateway 登录恢复**：当前 token 已过期（`gateway_auth_expired`）。执行 `doops login --target zyj` 重新登录；同一 Gateway 的 targets 共用登录态（`CLUSTER_INSTALL_GUIDE.md:37-47`；事故记录 6.1）[确证]。
2. **CLI 与候选镜像同版**：先按 3.5 安装 `v1.3.127` CLI；`doops version` 必须显示 `release=v1.3.127 commit=<候选镜像构建 commit>`。否则升级必然在 `release commit mismatch` 处失败并回滚（`BUILD_DEPLOY_SOP.md:253-257`；`CLUSTER_INSTALL_GUIDE.md:34-35`）[确证]。
3. **确认 zyj 运行镜像来源**（决定是否需要 Harbor 复制）：只读回读当前 Deployment，不允许改任何资源：
   ```bash
   # [确证] doops k8s 只读入口；Secret/ServiceAccount 不开放读取
   doops -session zyj-upgrade k8s --target zyj get deployment doops-agent -n ai \
     -o jsonpath='{.spec.template.spec.containers[?(@.name=="doops-agent")].image}'
   doops -session zyj-upgrade k8s --target zyj get deployment doops-agent -n ai \
     -o jsonpath='{.spec.template.spec.imagePullSecrets}'
   doops -session zyj-upgrade k8s --target zyj helm history doops-agent -n ai
   doops -session zyj-upgrade k8s --target zyj helm status doops-agent -n ai
   ```
   - image 指向 `docker.cnb.cool/...` → 走 4.3。
   - image 指向 `repo.aiedulab.cn...` → 先停，走决策 D5（Harbor 复制未验证）。
4. **能力与基线自检**（只读）：
   ```bash
   doops -session zyj-upgrade targets --target zyj          # RELEASE_COMMIT 应为 081f24ab...；STATUS idle
   doops -session zyj-upgrade capabilities --target zyj --require structuredSSH   # protocol=structuredSSH status=ready
   doops -session zyj-upgrade exec --target zyj --cmd "/usr/local/bin/do-agent --version"   # 当前应为 0.2.42
   ```
   [确证] 命令；输出值需现场确认（exec 需 structuredSSH 能力）。

### 4.3 预演与正式升级

```bash
# (a) 参数预演：只打印参数，不验证 live release/权限，不创建 Job
doops -session zyj-upgrade upgrade --target zyj --release doops-agent --namespace ai \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<OCI index digest 或平台 manifest digest> --dry-run

# (b) 正式升级（自动先跑 preflight Job，再跑真实 apply Job）
doops -session zyj-upgrade upgrade --target zyj --release doops-agent --namespace ai \
  --image docker.cnb.cool/l8ai/ai/doops.sh@sha256:<digest>
```

机制（[确证] 代码/SOP）：

- CLI 先对每个 target 提交 `preflight=true` 的 Job：以**候选镜像**启动（证明节点能拉取），重复 apply 前全部检查，并对同一 values 做 `helm upgrade --dry-run=server`；失败则"nothing was changed"（`upgrade.go:92-96,170+`；`self_upgrade.go:1618-1623`）。
  注意：preflight 会创建 Job —— 这是**第一次集群写入**，但不动 release；失败只留 Job 记录。
- 通过后提交真实 apply Job：在 Agent 进程外执行 Helm upgrade；Job 挂载 Agent 的 kubeconfig，固定 Agent 节点与 runtime class；Chart 固定为镜像内 `/app/deploy/helm/doops-agent`（`self_upgrade.go:32`），values 来自当前 release 的 `helm get values` + 镜像 override（`self_upgrade.go:1332-1340`）。
- Chart 采用 RollingUpdate `maxSurge=1/maxUnavailable=0`（`templates/deployment.yaml:15-21`），新旧 Pod 交替、Gateway 重连后删旧副本；replicas=1，升级窗口内不出现 0 副本（[确证] chart）。
- **四条保证**（SOP:306-316）：① 改动前备份 Helm revision/values/manifest；② 校验新 Pod Ready + imageID 与固定 digest 一致 + Gateway 重连 + `release_commit` 正确；③ 校验失败自动恢复备份；④ 全程走 Gateway 审计路径。
- 升级后 CLI 自己再做两次 probe：`structuredSSH` 与 `release_commit == CLI commit`；任一失败自动提交 rollback Job（`upgrade.go:255-268`）。
- `--image` 同时接受 `repository:tag`，但 **tag 不是不可变证据，不要用于生产升级**（SOP:242-243）；统一用 digest。
- 已知边界：zyj 若在 Helm 之外有额外挂载（历史记录提到 live 额外保留 `kubernetes-config` 卷/挂载、toleration、args），Job 会用 `helm template` 对比上一 revision，列出并备份/移除指向被删卷的 stale mount，回滚时恢复（`self_upgrade.go:1491-1508`；SOP:392-403）[确证]；预检只列出不改，失败分类 `stale_volume_mounts_unresolved`（`self_upgrade.go:1169-1183`）。**不要手工 kubectl patch 挂载**。

### 4.4 升级后验证点（全部只读）

```bash
doops -session zyj-upgrade targets --target zyj
#   RELEASE_COMMIT == v1.3.127 的 40 位 commit；STATUS idle；QUEUED 0

doops -session zyj-upgrade capabilities --target zyj --require structuredSSH
#   protocol=structuredSSH status=ready

doops -session zyj-upgrade k8s --target zyj get deployment doops-agent -n ai \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="doops-agent")].image}'
#   等于本次 pin 的 repository@sha256:<digest>

doops -session zyj-upgrade k8s --target zyj helm history doops-agent -n ai
#   最新 revision（预期 50）状态 deployed；上一 revision 49 仍在历史中（回滚点）

doops -session zyj-upgrade k8s --target zyj rollout status deployment/doops-agent -n ai --timeout 120s

doops -session zyj-upgrade exec --target zyj --cmd "/usr/local/bin/do-agent --version"
#   新 doagent 版本（如 0.2.45）

# 功能验收（只读 ask，不 --apply）
doops -session zyj-smoke-20261110 ask --target zyj \
  --msg '只读诊断：不要修改任何资源。请只回答 OK。'
#   期望 OK；若再出现 prompt admission 错误，应变为可重试的明确准入错误（doops_prompt_queued 类），
#   而不是 execution_outcome_unknown: prompt_turn_id_missing
```

验收证据落点：Job 把已验收 revision 的 image/runtimeDigest/podUID/node 写入 ConfigMap `doops-upgrade-verified-<release>`（`self_upgrade.go:1135-1161`）[确证]；手工回滚只允许使用有验收证据的历史 revision（SOP:388-389）。

### 4.5 回滚点

| 场景 | 动作 | 分级 |
|---|---|---|
| apply Job 内校验失败 | Job 自动 `helm rollback` 到上一 revision，恢复 stale mount，并复验 | [确证] `self_upgrade.go:1610-1710`、SOP:306-316 |
| Job 成功但 CLI probe 失败（structuredSSH/commit） | CLI 自动提交 rollback Job | [确证] `upgrade.go:255-268` |
| 人工回滚 | `doops -session <s> upgrade --target zyj --rollback`（不接受 `--image`，不能与 `--dry-run` 同用；默认回到"有验收证据的上一成功 revision"） | [确证] flags |
| 回滚基线镜像缺失 | preflight 以 `rollback_baseline_image_unavailable` 失败，**改动前停止**；默认 fail closed | [确证] SOP:276-287 |
| `--allow-missing-rollback-baseline` | 只在运维明确拒绝更换 cluster/instance 且接受"失败后无法拉回旧镜像"时使用，日志留 `warning:` | [确证] SOP:284-287；**本任务不建议** |

不可用回滚路径（硬性）：Agent 不能在自身进程里替换/回滚自身；所有替换只能由独立 Job 或外部脚本执行（SOP:306-316）。

---

## 5. 耗时预估（全部 [推断]，按 stage 结构/code 常量）

| 阶段 | 预计 | 依据 |
|---|---|---|
| doagent rebase + bump 合入（A3 负责人） | 数小时（冲突与测试适配风险） | 与 0.2.44 重叠文件 [Git 确证]，工时推断 |
| doagent tag CI | 40–75 分钟 | release gate cap 30m、桌面 40m cap、Linux 20m cap（`.cnb.yml:41-130`） |
| doops main push CI（可选 gate） | 20–35 分钟 | 三模块 go test + web build + 候选镜像 |
| doops release CI | 30–50 分钟 | 13 个 stage；bump pin 后 base-light 额外重建 |
| `install.sh`（操作员机器） | < 1 分钟 + 下载 | 发布资产小 |
| zyj upgrade（preflight + apply + 验证） | 5–15 分钟（典型）；CLI 最多等 21 分钟 | `upgrade.go:28-29`（poll 5s / wait 21m）、verify ≤600s、helm timeout 300s |
| zyj 回滚（若触发） | 3–10 分钟 | 同上 |
| 端到端（doagent tag → zyj 验收） | 2–3 小时 CI + 人工 gate | 合计推断 |

---

## 6. 阻塞 / 风险 / 待决策

### 6.1 阻塞（必须先解决）

| id | 项 | 说明 | 归属 |
|---|---|---|---|
| B1 | Gateway token 过期 | 事故记录 6.1；所有 Gateway 操作（含升级）前必须 `doops login --target zyj`。**不要在我方再尝试远端命令，先向用户要登录窗口** | 用户/Lead |
| B2 | doagent 新版本基线未定 | A3 基于 0.2.42，而 doagent `main` 已到 0.2.44；0.2.43/0.2.44 镜像 tag 已被占用（tag 不可复用，tag 流水线会 `reject existing version image`）。必须先 rebase 并确定 0.2.45 | A3 负责人 + 用户（D1） |
| B3 | zyj 运行镜像来源未复核 | repo 内无 `352fa6ef...` 记录；需只读回读 Deployment image。若为 `repo.aiedulab.cn` Harbor，则复制链路未验证（D5） | 执行人 |
| B4 | CNB 发布授权未落实 | tag push + `cnb build start-build` 需要 `l8ai/ai/doops.sh` 权限；仓库内无记录哪个账号/是否有审批 | 用户（D3） |

### 6.2 风险

| id | 风险 | 缓解 |
|---|---|---|
| R1 | 下一 tag 除 A1 外还包含 3 项未发布运行代码修复（dosql / tunnel_hub relay cancel / pull snapshot） | 发布前逐 commit review + 全量 CI 绿；不要为了只发 A1 而挑 commit 打 tag |
| R2 | A1 改变 Agent 对 queued prompt 的分类与 session 清理 | 依赖 CI 全量测试 + zyj 只读 ask 验收；失败即回滚 Job |
| R3 | bump doagent pin 后忘记改 `test_doagent_runtime_lock.py` 硬编码 0.2.42 | release prep commit 一并改；CI 第一道 stage 即暴露 |
| R4 | bump pin 触发 base-light 重建 → 所有 target 重新下载约 200MB | 预留 zyj 拉取时间；preflight 阶段即验证节点可拉取；带宽低时可能接近 preflight 超时 |
| R5 | CLI 与候选镜像不同 commit → `release commit mismatch` 自动回滚 | 严格按 3.5 安装 v1.3.127 CLI 后再升级；禁止用 v1.3.126/开发链接版 CLI |
| R6 | zyj live/values 漂移（历史记录：重复 `nerdctl-data` volume、额外 `kubernetes-config` 卷/挂载、toleration、args） | 预检 server-side dry-run + stale mount 检查会 fail closed（`stale_volume_mounts_unresolved`）；失败不手工热修，按 SOP 把修正进 Chart/values 再走升级路径（SOP:534） |
| R7 | 回滚基线（旧镜像 `352fa6ef...`）在 registry 不可拉取（Agent 节点凭据视角） | preflight 的 `rollback_baseline_*` 分类会在改动前失败；不得用 `--allow-missing-rollback-baseline` 绕过（需 D7 决策） |
| R8 | Harbor 复制路径是草案且 ZYJ 未验证；旧同步脚本已不存在 | 升级前确认 image 源；若走 Harbor，先决策 D5 并独立验证复制与 digest 保留 |
| R9 | 发布期间 `latest` 移动、他人用 latest 升级 | 生产升级只用 tag 对应的不可变 digest |
| R10 | 全量 target 升级的连带风险 | 用户已要求过一次 13 target 全量 campaign；本次建议先 zyj（D4），其他 target 另行授权 |
| R11 | 本机 pytest 缓存可掩盖断言（本次实测） | 发布证据以 CNB CI 为准；本地如需可信结果先清理 `backend/test/__pycache__` 再跑 |

### 6.3 待用户/Lead 决策

| id | 决策 | 选项/建议 |
|---|---|---|
| D1 | doagent 新版本号与发布基线 | 建议 rebase 到 doagent `main`（0.2.44）后发 `v0.2.45`；不建议从 0.2.42 直接发"更高版本号"（会丢 0.2.43/0.2.44 内容，版本线不自洽） |
| D2 | doops 版本号与 tag message | 任务书给 `v1.3.127`；tag message 格式见 3.1 |
| D3 | CNB 发布执行人与授权方式 | 由用户确认账号/审批；是否允许直接 push main + tag，还是走 PR |
| D4 | 升级范围 | 建议只 zyj；若全量，需用户明确授权并逐 target 走同一四条保证（回滚基线逐台不同） |
| D5 | zyj 镜像来源与 Harbor 复制 | 若为 CNB registry：无需复制；若为 Harbor：Harbor 复制尚未验证，需单独立项 |
| D6 | 是否接受一次性跳过回滚基线（`--allow-missing-rollback-baseline`） | 默认不接受；仅在用户书面接受"失败后无法拉回旧镜像"时 |
| D7 | 发布后是否顺带升级 Gateway/Web | CLI/Gateway 资产同批发布，但 zyj 修复只需要 Agent 镜像；Gateway/Web 部署另行决策 |

---

## 7. 明确禁止（本 runbook 未执行，执行时也不得越界）

- 不得从本机 `main`（GitHub 线）打 tag、派生发布。
- 不得手工修改已部署集群的 Helm values / Deployment / ConfigMap / Secret 来"绕过"预检或回滚。
- 不得在同一 target 上并发两个升级 Job，或用 `latest` tag 升级。
- 不得把凭据 payload、token、kubeconfig 明文写入命令参数、文档、日志或 issue。
- 不得用开发链接版 CLI（`dev-link.sh`）执行 Agent 升级（`CLUSTER_INSTALL_GUIDE.md:115-119`）。
- 不得在无授权情况下升级 zyj 之外的其他 target。

---

## 8. 证据索引

- `.cnb.yml:56-118`（main push ci：契约/agent/gateway/cli 测试 + web build + 以 40 位 commit 为 tag 的候选镜像）、`:240-287`（master 兼容线 push ci，仅测试）、`:313-316`（发布触发命令）、`:317-404`（api_trigger_release 发布 13 stage）。
- `backend/runtime-versions.env`（doagent/base pin）。
- `backend/Dockerfile:1-9,34-45`；`backend/scripts/build-agent-image.sh`；`backend/scripts/resolve-agent-base-image.sh:1-5,16-38`；`backend/scripts/build-agent-base-image.sh`。
- `backend/scripts/publish-cli-release.mjs:7-15`；`backend/scripts/verify-published-cli-release.sh:23-77`；`backend/scripts/build-cli.sh:4-22`；`backend/scripts/install.sh:263-298, 425-537`。
- `backend/test/test_doagent_runtime_lock.py:13-25`（版本硬编码契约）。
- `backend/docs/BUILD_DEPLOY_SOP.md:160-180,236-256,276-287,306-316,385-403`。
- `backend/docs/CLUSTER_INSTALL_GUIDE.md:12-35,49-99,115-132`。
- `backend/docs/HARBOR_REPLICATION_SPEC.md:1-16,426-429`。
- `backend/cli/cli/doops/upgrade.go:28-29,36-96,170-268`；`main.go:1014-1055`（upgrade flags）；`doops k8s --help`（只读白名单）。
- `backend/agent/internal/server/self_upgrade.go:32,1169-1183,1332-1340,1491-1508,1618-1623`。
- doagent 仓库：`.cnb.yml:1-130`（snapshot + tag_push 发布流水线）、`Cargo.toml`（HEAD 0.2.42 / origin/main 0.2.44）。
- 事故与基线：`docs/reviews/2026-11-10-zyj-prompt-admission-block.md`；`docs/reviews/2026-11-08-doagent-042-release.md`；`docs/reviews/2026-11-08-doagent-042-all-environments.md:16-26,30-38`；`docs/reviews/evidence/2026-11-10-zyj-admission-block/targets-zyj-after-recovery.txt`。
- 分支/commit：`cnb/main=66769eef`、`v1.3.126=76abae08`、A1 `doops-admission-remediation=1ef51421`（基于 `cnb/main`，+11 commits）、zyj Agent `081f24ab`。
