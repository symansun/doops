import { sessionIsAdmin, type Session } from "../client.ts"
import { consoleScopeKey, type StorageLike } from "../console-persistence.ts"
import { consoleFetch } from "../network-error.ts"
import {
  BUILTIN_PLUGIN_CATALOG,
  DEFAULT_PLUGIN_SCENARIO_ID,
  type PluginMutationRequest,
  type PluginMutationResponse,
  type PluginStateResponse,
  type PluginView,
  type SkillPluginManifest,
} from "./types.ts"
import { buildDefaultPluginState, findBuiltinPluginMeta, findScenarioMeta } from "./resolver.ts"

const DEMO_L2_MANIFESTS: SkillPluginManifest[] = [
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-vm-host",
      name: "主机性能与内核体检",
      description: "采集 CPU、内存、去重磁盘容量、物理块设备 I/O、网卡吞吐、GPU 与 M1–M5 内核安全基线",
      category: "observability",
      icon: "server",
      scenarios: ["host-security-ops", "ai-compute-cluster", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-vm-host"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "主机性能体检", group: "extensions", order: 5 },
        target_tab: { enabled: true, label: "主机体检", order: 5 },
        home_widget: {
          enabled: true,
          title: "主机性能与内核体检",
          span: 1,
          metrics: [
            { key: "cpu_percent", label: "CPU 使用率", unit: "%", aggregate: "max" },
            { key: "memory_percent", label: "内存使用率", unit: "%", aggregate: "max" },
            { key: "disk_percent", label: "磁盘最高占比", unit: "%", aggregate: "max" },
            { key: "load_1m", label: "1 分钟负载", unit: "", aggregate: "max" },
          ],
        },
      },
      inputs: [],
      presets: [
        { id: "vm-quick-health", title: "零 Token 全面主机体检", description: "毫秒级采集 CPU、内存、去重磁盘、I/O、网卡与内核安全基线", mode: "script" },
        { id: "vm-ai-bottleneck", title: "AI 深度瓶颈与内核基线分析", description: "结合最新主机指标与 sysctl 参数输出优先级治理方案", mode: "agent", prompt: "请运行 inspect-vm-host Skill 分析主机高负载与内核安全基线。" },
      ],
      actions: [
        { id: "vm-diag", label: "只读瓶颈溯源", description: "深度核查 Top CPU/内存进程与磁盘延迟", mode: "check", skill: "shell", prompt_template: "请只读排查当前目标主机的 CPU、内存与磁盘瓶颈。" },
        { id: "vm-sysctl-plan", label: "内核安全加固规划", description: "针对 M4 sysctl 缺口生成可回滚加固脚本", mode: "plan", skill: "shell", prompt_template: "请针对内核安全基线缺口生成可回滚加固方案。" },
      ],
      views: [
        { type: "kpi_grid", title: "主机核心资源态势", items: [{ key: "cpu_percent", label: "CPU 使用率", unit: "%" }, { key: "memory_percent", label: "内存使用率", unit: "%" }, { key: "disk_percent", label: "磁盘最高占比", unit: "%" }, { key: "load_1m", label: "1 分钟负载", unit: "" }] },
        { type: "resource_table", title: "磁盘挂载点与去重容量明细", source_key: "mounts", row_action_id: "vm-diag", columns: [{ key: "mount", label: "挂载点", kind: "code" }, { key: "source", label: "设备源", kind: "code" }, { key: "total_gib", label: "总容量(GiB)", kind: "number" }, { key: "used_pct", label: "使用率(%)", kind: "number" }] },
        { type: "checks_table", title: "资源与 M1–M8 安全基线核查" },
        { type: "artifact_viewer", title: "完整主机巡检产物" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.machine-workload-discovery",
      name: "应用挖掘与自动打标",
      description: "零 Token 只读挖掘机器已部署的数据库、语言运行时（Java/Python/PHP/Node/Go）、中间件与业务系统及版本，生成可一键同步至机器台账的规范标签",
      category: "ops",
      icon: "sparkles",
      scenarios: ["all-in-one", "app-delivery-web", "host-security-ops", "ai-compute-cluster"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["machine-workload-discovery"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: true, label: "应用挖掘与打标", group: "extensions", order: 8 },
        target_tab: { enabled: true, label: "应用与标签", order: 8 },
        home_widget: { enabled: false, title: "应用挖掘与自动打标", span: 1, metrics: [] },
      },
      inputs: [
        { key: "SCAN_CONTAINERS", label: "扫描运行中容器", type: "boolean", default: true },
        { key: "SCAN_WEBROOTS", label: "扫描站点与应用构建清单", type: "boolean", default: true },
        { key: "INCLUDE_INSTALLED_RUNTIMES", label: "包含未常驻的已安装运行时", type: "boolean", default: false },
      ],
      presets: [
        { id: "workload-fast-mine", title: "零 Token 部署挖掘与打标", description: "秒级识别数据库、语言运行时、中间件、业务系统及版本", mode: "script", inputs: { SCAN_CONTAINERS: true, SCAN_WEBROOTS: true, INCLUDE_INSTALLED_RUNTIMES: false } },
      ],
      actions: [
        { id: "workload-deep-inspect", label: "深入分析所选部署组件", description: "只读核查所选组件版本、端口与依赖", mode: "check", skill: "shell", prompt_template: "请只读分析所选部署组件的精确版本、监听端口与关联依赖。" },
        { id: "workload-tag-plan", label: "完善业务标签与分类建议", description: "梳理符合机器台账规范的业务标签体系", mode: "plan", skill: "shell", prompt_template: "请结合当前部署挖掘结果整理符合 DoOps 机器台账规范的标签方案。" },
      ],
      views: [
        { type: "kpi_grid", title: "部署与标签挖掘概览", items: [{ key: "workloads_total", label: "已识别部署项", unit: "项" }, { key: "databases_detected", label: "数据库与存储", unit: "个" }, { key: "runtimes_detected", label: "语言运行时", unit: "种" }, { key: "apps_detected", label: "业务系统/应用", unit: "个" }, { key: "suggested_tags_count", label: "挖掘推荐标签", unit: "个" }] },
        { type: "resource_table", title: "已部署数据库、运行时与业务系统清单", source_key: "workloads", row_action_id: "workload-deep-inspect", columns: [{ key: "category", label: "分类", kind: "badge" }, { key: "name", label: "组件 / 业务系统", kind: "code" }, { key: "version", label: "版本", kind: "code" }, { key: "runtime_stack", label: "技术栈", kind: "badge" }, { key: "run_mode", label: "部署形态", kind: "badge" }] },
        { type: "resource_table", title: "挖掘标签建议（可直接同步台账）", source_key: "suggested_tags", row_action_id: "workload-tag-plan", columns: [{ key: "tag", label: "推荐标签", kind: "code" }, { key: "category", label: "标签维度", kind: "badge" }, { key: "confidence", label: "置信度", kind: "badge" }, { key: "source", label: "挖掘来源", kind: "text" }] },
        { type: "artifact_viewer", title: "完整部署挖掘产物" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.gpu-governance",
      name: "GPU 算力治理",
      description: "监控 NVIDIA GPU 利用率、显存占用、温度与 ECC 健康，定位异常占卡进程",
      category: "infrastructure",
      icon: "cpu",
      scenarios: ["ai-compute-cluster", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["gpu-governance"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: true, label: "GPU 算力治理", group: "extensions", order: 10 },
        target_tab: { enabled: true, label: "GPU 治理", order: 10 },
        home_widget: {
          enabled: true,
          title: "GPU 算力治理",
          span: 1,
          metrics: [
            { key: "gpu_util_max_pct", label: "最高算力利用", unit: "%", aggregate: "max" },
            { key: "gpu_mem_max_pct", label: "最高显存占用", unit: "%", aggregate: "max" },
            { key: "gpu_temp_max_c", label: "最高卡温", unit: "°C", aggregate: "max" },
            { key: "gpu_count", label: "检出卡数", unit: "张", aggregate: "max" },
          ],
        },
        ask_presets: [
          {
            title: "排查异常占卡与显存泄漏",
            description: "结合 nvidia-smi 与进程状态分析高显存低算力占卡",
            prompt: "请使用 gpu-governance Skill 检查当前目标 GPU 利用率、显存占用和占卡进程，重点排查显存占用高但算力利用率低的僵尸任务。",
          },
        ],
      },
      inputs: [
        { key: "WARN_TEMP_C", label: "温度告警阈值", description: "单卡温度达到该值时触发 warning", type: "number", default: 85, min: 50, max: 105, step: 1, unit: "°C" },
        { key: "WARN_MEM_PCT", label: "显存告警阈值", description: "单卡显存占用达到该比例时触发 warning", type: "number", default: 90, min: 50, max: 99, step: 1, unit: "%" },
        { key: "INCLUDE_PROCESSES", label: "采集占卡进程", description: "只读列出占卡进程的 PID 与显存用量", type: "boolean", default: true },
      ],
      presets: [
        { id: "gpu-quick-health", title: "快速算力体检", description: "按默认阈值秒级巡检 GPU 利用率、显存、温度与 ECC", mode: "script", inputs: { WARN_TEMP_C: 85, WARN_MEM_PCT: 90, INCLUDE_PROCESSES: true } },
        { id: "gpu-strict-thermal", title: "严格温控与显存核查", description: "收紧至 78°C / 80% 显存阈值", mode: "script", inputs: { WARN_TEMP_C: 78, WARN_MEM_PCT: 80, INCLUDE_PROCESSES: true } },
        { id: "gpu-zombie-diagnosis", title: "AI 排查僵尸占卡", description: "在 AI 助手中分析长时间空占显存的任务", mode: "agent", prompt: "请运行 gpu-governance Skill 采集全部 GPU 显存与进程占用情况，识别疑似空闲占卡进程并给出治理建议。" },
      ],
      actions: [
        { id: "gpu-zombie-check", label: "只读核查占卡进程与算力", description: "查看所选 GPU 的活跃计算进程与 Pod 归属", mode: "check", skill: "shell", prompt_template: "请只读核查所选 GPU 设备的 nvidia-smi 计算进程与显存占用。" },
        { id: "gpu-thermal-plan", label: "生成降载与散热治理方案", description: "针对高温或 ECC 报错单卡输出治理方案", mode: "plan", skill: "shell", prompt_template: "请结合所选 GPU 的温度、显存水位与 ECC 计数制定治理方案。" },
      ],
      views: [
        {
          type: "kpi_grid",
          title: "算力关键指标",
          items: [
            { key: "gpu_count", label: "GPU 卡数", unit: "张" },
            { key: "gpu_util_avg_pct", label: "平均利用率", unit: "%" },
            { key: "gpu_mem_max_pct", label: "最高显存占比", unit: "%", warn_threshold: 90, crit_threshold: 98 },
            { key: "gpu_temp_max_c", label: "最高温度", unit: "°C", warn_threshold: 85, crit_threshold: 92 },
            { key: "gpu_ecc_uncorrected", label: "ECC 未纠正错误", unit: "次", warn_threshold: 1, crit_threshold: 1 },
            { key: "gpu_process_count", label: "活跃占卡进程", unit: "个" },
          ],
        },
        {
          type: "resource_table",
          title: "GPU 单卡明细与健康状态",
          source_key: "gpus",
          row_action_id: "gpu-zombie-check",
          columns: [
            { key: "index", label: "卡号", kind: "code" },
            { key: "name", label: "型号", kind: "text" },
            { key: "util_percent", label: "算力(%)", kind: "number" },
            { key: "mem_percent", label: "显存(%)", kind: "number" },
            { key: "temp_c", label: "温度(°C)", kind: "number" },
            { key: "ecc_uncorrected", label: "ECC错", kind: "number" },
          ],
        },
        { type: "charts", title: "单卡利用率与显存分布" },
        { type: "checks_table", title: "健康与阈值核查项" },
        { type: "trend_series", title: "GPU 指标趋势", metric_keys: ["gpu_util_max_pct", "gpu_mem_max_pct", "gpu_temp_max_c"], range: "24h" },
        { type: "artifact_viewer", title: "原始巡检报告与明细" },
      ],
    },
    schedule_template: {
      name: "GPU 算力治理定时巡检",
      description: "每 15 分钟自动采集全集群 NVIDIA GPU 利用率、显存、温度与 ECC 健康指标",
      interval_seconds: 900,
      scope_mode: "all_online",
      inputs: { WARN_TEMP_C: 85, WARN_MEM_PCT: 90, INCLUDE_PROCESSES: true },
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-k8s-cluster",
      name: "K8s 集群深度体检",
      description: "只读巡检 Node 健康压力、异常与高重启 Pod、控制器副本、PVC 绑定、Warning 事件及 Top40 Pod 资源用量",
      category: "infrastructure",
      icon: "layers",
      scenarios: ["ai-compute-cluster", "app-delivery-web", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes"], requires_skills: ["inspect-k8s-cluster"], requires_mcp: ["doops"], companion_agent_skill: "k8s" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "K8s 集群体检", group: "extensions", order: 15 },
        target_tab: { enabled: true, label: "集群体检", order: 15 },
        home_widget: {
          enabled: true,
          title: "K8s 集群深度体检",
          span: 1,
          metrics: [
            { key: "k8s_nodes_total", label: "集群节点数", unit: "个", aggregate: "max" },
            { key: "k8s_nodes_not_ready", label: "异常节点", unit: "个", aggregate: "max" },
            { key: "k8s_pods_abnormal", label: "异常 Pod", unit: "个", aggregate: "max" },
            { key: "k8s_warning_events", label: "Warning 事件", unit: "条", aggregate: "max" },
          ],
        },
      },
      inputs: [],
      presets: [
        { id: "k8s-baseline-health", title: "集群全量基线体检", description: "采集 Node、Pod、PVC、事件与 Top40 Pod 用量", mode: "script" },
        { id: "k8s-ai-triage", title: "AI 异常工作负载根因研判", description: "深挖异常 Pod 日志与调度阻塞事件", mode: "agent", prompt: "请结合 inspect-k8s-cluster 与 mcp_doops_k8s_read 排查异常 Pod 根因。" },
      ],
      actions: [
        { id: "k8s-triage", label: "异常 Pod 根因排查", description: "只读拉取所选 Pod 日志、状态与事件", mode: "check", skill: "k8s", prompt_template: "请使用 mcp_doops_k8s_read 只读检查所选 Pod 日志与事件。" },
        { id: "k8s-remediate-plan", label: "规划扩缩容与资源调优", description: "为高负载工作负载生成调优与回滚方案", mode: "plan", skill: "k8s", prompt_template: "请为所选工作负载制定资源调优与回滚方案。" },
      ],
      views: [
        { type: "kpi_grid", title: "集群健康与资源态势", items: [{ key: "k8s_nodes_total", label: "节点总数", unit: "个" }, { key: "k8s_nodes_not_ready", label: "异常节点", unit: "个" }, { key: "k8s_pods_abnormal", label: "异常 Pod", unit: "个" }, { key: "k8s_warning_events", label: "Warning 事件", unit: "条" }] },
        { type: "resource_table", title: "Top 40 高负载 Pod 实时用量", source_key: "pod_usage", row_action_id: "k8s-triage", columns: [{ key: "namespace", label: "命名空间", kind: "badge" }, { key: "pod", label: "Pod 名称", kind: "code" }, { key: "cpu_millicores", label: "CPU(m)", kind: "number" }, { key: "memory_bytes", label: "内存(Bytes)", kind: "number" }] },
        { type: "checks_table", title: "集群基线检查明细" },
        { type: "artifact_viewer", title: "完整 K8s 巡检产物" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.host-security",
      name: "主机安全治理",
      description: "一站式核查挖矿木马、违规代理穿透、SSH 加固基线与待更新安全补丁",
      category: "security",
      icon: "shield",
      scenarios: ["ai-compute-cluster", "host-security-ops", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["host-security"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: true, label: "主机安全治理", group: "extensions", order: 20 },
        target_tab: { enabled: true, label: "安全治理", order: 20 },
        home_widget: {
          enabled: true,
          title: "主机安全治理",
          span: 1,
          metrics: [
            { key: "sec_risk_total", label: "风险项合计", unit: "项", aggregate: "max" },
            { key: "sec_mining_suspects", label: "疑似挖矿进程", unit: "个", aggregate: "max" },
            { key: "sec_proxy_suspects", label: "违规代理线索", unit: "条", aggregate: "max" },
            { key: "sec_pending_updates", label: "待更新补丁", unit: "个", aggregate: "max" },
          ],
        },
        ask_presets: [
          {
            title: "排查挖矿与可疑外联",
            description: "结合进程、计划任务与网络连接做安全溯源分析",
            prompt: "请使用 host-security Skill 全面核查当前主机的挖矿木马特征、违规代理穿透、SSH 加固配置与高危补丁缺口。",
          },
        ],
      },
      inputs: [
        { key: "CHECK_PROXY", label: "检测违规代理与穿透", description: "扫描 clash/v2ray/frpc 等进程及常见代理监听端口", type: "boolean", default: true },
        { key: "CHECK_UPDATES", label: "核查待更新补丁", description: "只读查询 apt/dnf/yum 待更新安全包概况", type: "boolean", default: true },
        {
          key: "SENSITIVITY",
          label: "扫描灵敏度",
          description: "严格模式额外告警 /tmp 进程与 SSH 密码登录",
          type: "select",
          default: "standard",
          options: [
            { value: "standard", label: "标准基线" },
            { value: "strict", label: "严格审计" },
          ],
        },
      ],
      presets: [
        { id: "sec-standard-scan", title: "标准安全体检", description: "全面扫描挖矿、代理、SSH 与补丁风险", mode: "script", inputs: { CHECK_PROXY: true, CHECK_UPDATES: true, SENSITIVITY: "standard" } },
        { id: "sec-strict-audit", title: "严格合规审计", description: "开启严格模式，核查临时目录执行与 SSH 密码登录", mode: "script", inputs: { CHECK_PROXY: true, CHECK_UPDATES: true, SENSITIVITY: "strict" } },
        { id: "sec-incident-triage", title: "AI 安全事件研判", description: "在 AI 助手中对告警项做只读溯源并生成加固清单", mode: "agent", prompt: "请使用 host-security Skill 执行严格安全核查，针对所有告警项生成只读溯源证据与加固清单。" },
      ],
      actions: [
        { id: "sec-triage-check", label: "只读溯源告警进程与连接", description: "对命中的挖矿、代理、SSH 风险项做只读取证", mode: "check", skill: "shell", prompt_template: "请只读核查当前主机安全巡检命中的高风险进程、计划任务与 SSH 弱配置来源。" },
        { id: "sec-harden-plan", label: "生成安全加固与补丁计划", description: "输出 SSH 加固与补丁升级实施方案与回滚清单", mode: "plan", skill: "shell", prompt_template: "请针对主机安全风险项制定分优先级加固与回滚方案。" },
        { id: "sec-contain-apply", label: "受控执行应急处置与封堵", description: "经人工确认后执行应急处置并复核安全基线", mode: "apply", skill: "shell", prompt_template: "请在确认范围后执行受控安全应急处置并复跑 host-security 验证。" },
      ],
      views: [
        {
          type: "kpi_grid",
          title: "安全态势概览",
          items: [
            { key: "sec_risk_total", label: "风险项合计", unit: "项", warn_threshold: 1, crit_threshold: 3 },
            { key: "sec_mining_suspects", label: "疑似挖矿进程", unit: "个", warn_threshold: 1, crit_threshold: 1 },
            { key: "sec_proxy_suspects", label: "违规代理线索", unit: "条", warn_threshold: 1, crit_threshold: 2 },
            { key: "sec_ssh_issues", label: "SSH 基线缺口", unit: "项", warn_threshold: 1, crit_threshold: 2 },
            { key: "sec_pending_updates", label: "待更新软件包", unit: "个", warn_threshold: 10, crit_threshold: 50 },
          ],
        },
        { type: "findings_list", title: "安全风险发现清单（根因归并）" },
        { type: "checks_table", title: "安全检查明细" },
        { type: "charts", title: "安全风险分类统计" },
        { type: "trend_series", title: "安全风险历史趋势", metric_keys: ["sec_risk_total", "sec_mining_suspects", "sec_proxy_suspects"], range: "7d" },
        { type: "artifact_viewer", title: "完整安全巡检报告" },
      ],
    },
    schedule_template: {
      name: "主机安全治理定时巡检",
      description: "每小时自动执行全集群挖矿、违规代理、SSH 基线与补丁安全核查",
      interval_seconds: 3600,
      scope_mode: "all_online",
      inputs: { CHECK_PROXY: true, CHECK_UPDATES: true, SENSITIVITY: "standard" },
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-container-runtime",
      name: "容器运行时治理",
      description: "自动探测 Docker / nerdctl / Podman 运行时，监控容器 CPU、内存上限占比与 OOM 风险",
      category: "infrastructure",
      icon: "box",
      scenarios: ["app-delivery-web", "host-security-ops", "all-in-one"],
    },
    capabilities: { target_modes: ["docker", "linux"], requires_skills: ["inspect-container-runtime"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "容器运行时治理", group: "extensions", order: 25 },
        target_tab: { enabled: true, label: "容器治理", order: 25 },
        home_widget: {
          enabled: true,
          title: "容器运行时治理",
          span: 1,
          metrics: [
            { key: "containers_running", label: "运行中容器", unit: "个", aggregate: "max" },
            { key: "container_max_mem_percent", label: "最高内存占比", unit: "%", aggregate: "max" },
          ],
        },
      },
      inputs: [],
      presets: [
        { id: "container-quick-stats", title: "容器运行时实时体检", description: "零 Token 采集运行中容器 CPU、内存占比与 OOM 风险", mode: "script" },
      ],
      actions: [
        { id: "container-inspect", label: "排查容器日志与状态", description: "只读检查所选容器状态与尾部日志", mode: "check", skill: "shell", prompt_template: "请只读检查所选容器的运行时状态、限额与尾部日志。" },
        { id: "container-ops-plan", label: "规划限额与维护方案", description: "生成可回滚的容器资源与维护方案", mode: "plan", skill: "shell", prompt_template: "请为所选容器制定可回滚的内存限额与维护方案。" },
      ],
      views: [
        { type: "kpi_grid", title: "容器运行时态势", items: [{ key: "containers_running", label: "运行中容器数", unit: "个" }, { key: "container_max_mem_percent", label: "最高容器内存占比", unit: "%" }] },
        { type: "resource_table", title: "运行中容器资源明细", source_key: "containers", row_action_id: "container-inspect", columns: [{ key: "name", label: "容器名称", kind: "code" }, { key: "cpu_percent", label: "CPU(%)", kind: "number" }, { key: "mem_percent", label: "内存占比(%)", kind: "number" }, { key: "status", label: "状态", kind: "badge" }] },
        { type: "artifact_viewer", title: "完整容器运行时报告" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-app-web",
      name: "Web 可用性与证书巡检",
      description: "自动联动目标网站目录探测 HTTP/HTTPS 可用性、响应延迟、TLS 证书剩余天数与本机 Nginx 语法",
      category: "delivery",
      icon: "activity",
      scenarios: ["app-delivery-web", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-app-web"], catalog_inputs: ["website_urls"], companion_agent_skill: "doweb" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "Web 可用性巡检", group: "extensions", order: 35 },
        target_tab: { enabled: true, label: "Web 巡检", order: 35 },
        home_widget: {
          enabled: true,
          title: "Web 可用性与证书巡检",
          span: 1,
          metrics: [{ key: "tcp_listen_ports", label: "TCP 监听端口", unit: "个", aggregate: "max" }],
        },
      },
      inputs: [],
      presets: [
        { id: "web-avail-probe", title: "站点可用性与证书体检", description: "零 Token 探活全部已登记网站目录 URL 并校验 TLS 与 nginx -t", mode: "script" },
      ],
      actions: [
        { id: "web-avail-check", label: "核查站点与 Nginx 配置", description: "只读检查所选站点的 SiteSpec 与证书状态", mode: "check", skill: "doweb", prompt_template: "请只读核查所选站点的 Nginx 配置与证书状态。" },
        { id: "web-cert-plan", label: "规划证书续期与站点修复", description: "生成受控站点修复与证书续期方案", mode: "plan", skill: "doweb", prompt_template: "请制定所选站点的证书续期与修复回滚计划。" },
      ],
      views: [
        { type: "kpi_grid", title: "Web 运行态势", items: [{ key: "tcp_listen_ports", label: "本机 TCP 监听端口", unit: "个" }] },
        { type: "resource_table", title: "网站目录 HTTP/TLS 探活明细", source_key: "endpoints", row_action_id: "web-avail-check", columns: [{ key: "url", label: "站点 URL", kind: "code" }, { key: "status_code", label: "状态码", kind: "number" }, { key: "latency_ms", label: "延迟(ms)", kind: "number" }, { key: "cert_days_left", label: "证书剩余(天)", kind: "number" }, { key: "status", label: "状态", kind: "badge" }] },
        { type: "artifact_viewer", title: "完整 Web 巡检报告" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.attack-surface-scan",
      name: "外部暴露面扫描",
      description: "低速只读检查已登记站点 OWASP 安全头、敏感路径暴露、公开登录面与覆盖缺口",
      category: "security",
      icon: "radar",
      scenarios: ["host-security-ops", "app-delivery-web", "all-in-one"],
    },
    capabilities: {
      target_modes: ["kubernetes", "docker", "linux"],
      requires_skills: ["attack-surface-scan"],
      catalog_inputs: ["website_urls"],
      companion_agent_skill: "dosecurity",
    },
    ui: {
      slots: {
        sidebar: { enabled: true, label: "外部暴露面扫描", group: "extensions", order: 40 },
        target_tab: { enabled: true, label: "暴露面扫描", order: 40 },
        home_widget: {
          enabled: true,
          title: "外部暴露面扫描",
          span: 1,
          metrics: [
            { key: "surface_risk_total", label: "暴露面风险", unit: "项", aggregate: "max" },
            { key: "surface_exposed_paths", label: "敏感路径命中", unit: "处", aggregate: "max" },
            { key: "surface_missing_csp", label: "缺少 CSP 站点", unit: "个", aggregate: "max" },
          ],
        },
      },
      inputs: [
        { key: "CHECK_SECURITY_HEADERS", label: "核查 OWASP 安全响应头", type: "boolean", default: true },
        { key: "CHECK_SENSITIVE_PATHS", label: "探测常见敏感路径暴露", type: "boolean", default: true },
      ],
      presets: [
        { id: "surface-standard", title: "低速标准暴露面体检", description: "核查安全头与敏感路径暴露", mode: "script", inputs: { CHECK_SECURITY_HEADERS: true, CHECK_SENSITIVE_PATHS: true } },
      ],
      actions: [
        { id: "surface-harden-plan", label: "生成安全加固方案", description: "输出不破坏业务的 Nginx 安全头加固配置", mode: "check", skill: "dosecurity", prompt_template: "请结合暴露面扫描证据输出安全头加固方案（只读）。" },
      ],
      views: [
        { type: "kpi_grid", title: "暴露面安全概览", items: [{ key: "surface_risk_total", label: "风险发现合计", unit: "项" }, { key: "surface_exposed_paths", label: "敏感路径命中", unit: "处" }, { key: "surface_missing_csp", label: "缺少 CSP 站点", unit: "个" }] },
        { type: "findings_list", title: "风险发现、根因评分与覆盖缺口" },
        { type: "resource_table", title: "扫描站点暴露面矩阵", source_key: "resources", row_action_id: "surface-harden-plan", columns: [{ key: "url", label: "站点 URL", kind: "code" }, { key: "status_code", label: "状态码", kind: "number" }, { key: "missing_headers", label: "缺失安全头", kind: "number" }, { key: "status", label: "评级", kind: "badge" }] },
        { type: "artifact_viewer", title: "完整暴露面扫描产物" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-database",
      name: "数据库健康巡检",
      description: "通过受管 DoSql MCP 与目标数据库资产目录只读核查数据库连通性、活跃连接、TPS 与锁等待，凭据不落地",
      category: "data",
      icon: "database",
      scenarios: ["app-delivery-web", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-database"], requires_mcp: ["dosql"], catalog_inputs: ["database_refs"], companion_agent_skill: "dosql" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "数据库健康巡检", group: "extensions", order: 45 },
        target_tab: { enabled: true, label: "数据库巡检", order: 45 },
        home_widget: {
          enabled: true,
          title: "数据库健康巡检",
          span: 1,
          metrics: [
            { key: "active_connections", label: "活跃连接", unit: "个", aggregate: "max" },
            { key: "db_lock_waits", label: "锁等待", unit: "个", aggregate: "max" },
          ],
        },
      },
      inputs: [],
      presets: [
        { id: "db-readonly-health", title: "只读连通与指标体检", description: "通过受管 mcp_dosql_read 采集活跃连接、TPS 与锁等待", mode: "agent", prompt: "请使用 inspect-database Skill 通过 mcp_dosql_read 执行只读数据库巡检。" },
      ],
      actions: [
        { id: "db-health-check", label: "只读核查库连接与锁状态", description: "调用 DoSql 只读通道检查实例版本、活跃连接与锁等待", mode: "check", skill: "dosql", requires_mcp: "dosql", credential_use: "database", prompt_template: "请使用 dosql Skill 只读核查数据库连通性与锁等待。" },
        { id: "db-schema-plan", label: "生成结构治理与索引方案", description: "输出带回滚预案的索引与结构治理方案", mode: "plan", skill: "dosql", requires_mcp: "dosql", credential_use: "database", prompt_template: "请制定数据库结构治理与索引优化回滚方案。" },
      ],
      views: [
        { type: "kpi_grid", title: "数据库运行指标", items: [{ key: "active_connections", label: "活跃连接数", unit: "个" }, { key: "db_tps", label: "事务吞吐 (TPS)", unit: "tx/s" }, { key: "db_lock_waits", label: "锁等待", unit: "个" }] },
        { type: "findings_list", title: "风险与慢查询归因" },
        { type: "checks_table", title: "连通性与只读核查明细" },
        { type: "artifact_viewer", title: "完整数据库巡检报告" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-ssh-host",
      name: "远程 SSH 主机巡检",
      description: "按只读契约核查受管 SSH 通道授权与远程主机资源安全基线，通道未授权时如实回报缺口不伪造健康",
      category: "ops",
      icon: "terminal",
      scenarios: ["host-security-ops", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-ssh-host"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "SSH 远程巡检", group: "extensions", order: 60 },
        target_tab: { enabled: true, label: "SSH 巡检", order: 60 },
        home_widget: { enabled: false, title: "SSH 远程巡检", span: 1, metrics: [] },
      },
      inputs: [],
      presets: [
        { id: "ssh-channel-audit", title: "受管 SSH 通道与基线核查", description: "AI 驱动按契约核查受管 SSH 通道可用性及远程主机基线", mode: "agent", prompt: "请严格按 inspect-ssh-host Skill 核查受管 SSH 通道状态。" },
      ],
      actions: [
        { id: "ssh-channel-check", label: "核查受管 SSH 授权状态", description: "只读核查 SSH 通道依赖状态与接入边界", mode: "check", skill: "shell", prompt_template: "请只读核查受管 SSH 通道可用性与接入指引。" },
      ],
      views: [
        { type: "findings_list", title: "能力缺口与远程风险" },
        { type: "checks_table", title: "SSH 通道与基线检查项" },
        { type: "artifact_viewer", title: "SSH 巡检完整报告" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-package-vuln",
      name: "软件包与补丁基线",
      description: "零 Token 盘点核心系统组件版本、本地待升级包与重启需求",
      category: "security",
      icon: "wrench",
      scenarios: ["host-security-ops", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-package-vuln"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "软件包与补丁", group: "extensions", order: 65 },
        target_tab: { enabled: true, label: "补丁基线", order: 65 },
        home_widget: { enabled: false, title: "软件包与补丁基线", span: 1, metrics: [] },
      },
      inputs: [],
      presets: [
        { id: "pkg-baseline-scan", title: "关键组件与待升级包盘点", description: "零 Token 采集核心组件版本、本地待升级包与重启标记", mode: "script" },
      ],
      actions: [
        { id: "pkg-audit-plan", label: "生成分批补丁升级计划", description: "生成维护窗口升级方案与回滚清单", mode: "plan", skill: "shell", prompt_template: "请为所选软件包制定安全补丁升级与回滚计划。" },
        { id: "pkg-upgrade-apply", label: "受控执行补丁升级", description: "人工确认后执行指定安全补丁升级", mode: "apply", skill: "shell", prompt_template: "请在确认范围后执行指定软件包安全升级并验证。" },
      ],
      views: [
        { type: "kpi_grid", title: "软件包与补丁指标", items: [{ key: "key_packages_tracked", label: "已盘点核心组件", unit: "个" }, { key: "upgradable_packages", label: "本地待升级包", unit: "个" }] },
        { type: "resource_table", title: "核心组件版本清单", source_key: "packages", row_action_id: "pkg-audit-plan", columns: [{ key: "package", label: "软件包", kind: "code" }, { key: "version", label: "当前版本", kind: "code" }, { key: "manager", label: "包管理器", kind: "badge" }] },
        { type: "checks_table", title: "包清单与重启检查" },
        { type: "artifact_viewer", title: "完整补丁基线报告" },
      ],
    },
  },
  {
    api_version: "doops.sh/v1alpha1",
    kind: "SkillPlugin",
    plugin: {
      id: "skill.inspect-proxy-evasion",
      name: "违规代理与穿透检测",
      description: "零 Token 专项扫描违规代理进程、常见代理端口与代理环境变量",
      category: "security",
      icon: "eye",
      scenarios: ["host-security-ops", "all-in-one"],
    },
    capabilities: { target_modes: ["kubernetes", "docker", "linux"], requires_skills: ["inspect-proxy-evasion"], companion_agent_skill: "shell" },
    ui: {
      slots: {
        sidebar: { enabled: false, label: "代理与穿透检测", group: "extensions", order: 70 },
        target_tab: { enabled: true, label: "代理检测", order: 70 },
        home_widget: { enabled: false, title: "违规代理与穿透检测", span: 1, metrics: [] },
      },
      inputs: [],
      presets: [
        { id: "proxy-quick-scan", title: "代理进程与端口快速扫描", description: "零 Token 检查代理进程、端口监听与脱敏环境变量", mode: "script" },
      ],
      actions: [
        { id: "proxy-trace-check", label: "只读溯源可疑进程与连接", description: "只读核查所选进程树、二进制路径与外联端口", mode: "check", skill: "shell", prompt_template: "请只读核查所选疑似代理进程的进程树与持久化来源。" },
        { id: "proxy-contain-apply", label: "受控隔离与清理违规代理", description: "经人工确认后停止未授权代理并复核清零", mode: "apply", skill: "shell", prompt_template: "请在确认取证证据后受控清理违规代理进程并复核。" },
      ],
      views: [
        { type: "kpi_grid", title: "代理穿透态势", items: [{ key: "proxy_suspect_procs", label: "疑似代理进程", unit: "个" }, { key: "proxy_open_ports", label: "代理监听端口", unit: "个" }, { key: "proxy_env_count", label: "代理环境变量", unit: "个" }] },
        { type: "resource_table", title: "疑似代理与穿透进程", source_key: "suspect_processes", row_action_id: "proxy-trace-check", columns: [{ key: "pid", label: "PID", kind: "code" }, { key: "comm", label: "进程名", kind: "code" }, { key: "matched", label: "命中特征", kind: "text" }, { key: "status", label: "级别", kind: "badge" }] },
        { type: "checks_table", title: "进程、端口与环境检查" },
        { type: "artifact_viewer", title: "完整代理检测报告" },
      ],
    },
  },
]

function buildDemoL2Plugins(scenarioId: string, overrides: Record<string, boolean>): PluginView[] {
  return DEMO_L2_MANIFESTS.map((manifest) => {
    const id = manifest.plugin.id
    const slug = id.slice("skill.".length)
    const def = manifest.plugin.opt_in
      ? false
      : (manifest.plugin.scenarios.length === 0 || manifest.plugin.scenarios.includes(scenarioId))
    const hasOv = Object.prototype.hasOwnProperty.call(overrides, id)
    const effective = hasOv ? Boolean(overrides[id]) : def
    return {
      id,
      tier: "declarative",
      skill_slug: slug,
      scope: "system",
      builtin: true,
      can_delete: false,
      owner_is_me: false,
      skill_enabled: true,
      name: manifest.plugin.name,
      description: manifest.plugin.description ?? "",
      category: manifest.plugin.category,
      effective,
      scenario_default: def,
      override: hasOv ? Boolean(overrides[id]) : null,
      dependencies: {
        skills: [{ slug, state: "ready" }],
        mcp: (manifest.capabilities?.requires_mcp ?? []).map((m) => ({ slug: m, state: "ready" })),
      },
      manifest,
      manifest_error: "",
    }
  })
}

const CACHE_PREFIX = "doops:plugins:v1:"

export interface CachedPluginEnvelope {
  etag: string
  state: PluginStateResponse
}

function cacheKey(session: Session): string {
  return `${CACHE_PREFIX}${consoleScopeKey(session)}`
}

export function readCachedPluginState(storage: StorageLike | null, session: Session): CachedPluginEnvelope | null {
  if (!storage || session.demo) return null
  try {
    const raw = storage.getItem(cacheKey(session))
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<CachedPluginEnvelope>
    if (!parsed || typeof parsed !== "object" || !parsed.state || !Array.isArray(parsed.state.plugins)) {
      return null
    }
    return {
      etag: typeof parsed.etag === "string" ? parsed.etag : "",
      state: parsed.state,
    }
  } catch {
    return null
  }
}

export function writeCachedPluginState(
  storage: StorageLike | null,
  session: Session,
  state: PluginStateResponse,
  etag: string,
): void {
  if (!storage || session.demo) return
  try {
    storage.setItem(cacheKey(session), JSON.stringify({ etag, state }))
  } catch {
    // Ignore storage quota or private browsing errors.
  }
}

export function clearCachedPluginState(storage: StorageLike | null, session?: Session | null): void {
  if (!storage) return
  try {
    if (session) storage.removeItem(cacheKey(session))
  } catch {
    // Ignore storage errors.
  }
}

// In-memory plugin state for Demo mode (session-scoped, resets on page reload).
interface DemoPluginStore {
  configured: boolean
  scenarioId: string
  revision: number
  overrides: Record<string, boolean>
  updatedBy: string
  updatedAtMs: number
}

let demoStore: DemoPluginStore = {
  configured: false,
  scenarioId: DEFAULT_PLUGIN_SCENARIO_ID,
  revision: 0,
  overrides: {},
  updatedBy: "",
  updatedAtMs: 0,
}

export function resetDemoPluginStore(): void {
  demoStore = {
    configured: false,
    scenarioId: DEFAULT_PLUGIN_SCENARIO_ID,
    revision: 0,
    overrides: {},
    updatedBy: "",
    updatedAtMs: 0,
  }
}

function demoPluginStateSnapshot(): PluginStateResponse {
  return buildDefaultPluginState({
    supported: true,
    configured: demoStore.configured,
    scenarioId: demoStore.scenarioId,
    overrides: demoStore.overrides,
    revision: demoStore.revision,
    updatedBy: demoStore.updatedBy,
    updatedAtMs: demoStore.updatedAtMs,
    extraPlugins: buildDemoL2Plugins(demoStore.scenarioId, demoStore.overrides),
  })
}

export type PluginFetchResult =
  | { kind: "ok"; state: PluginStateResponse; etag: string }
  | { kind: "not_modified"; etag: string }
  | { kind: "unsupported"; state: PluginStateResponse }
  | { kind: "unauthorized" }
  | { kind: "error"; message: string }

export async function fetchPluginState(
  session: Session,
  options: { etag?: string; signal?: AbortSignal } = {},
): Promise<PluginFetchResult> {
  if (session.demo) {
    return {
      kind: "ok",
      state: demoPluginStateSnapshot(),
      etag: `"demo-r${demoStore.revision}"`,
    }
  }
  try {
    const headers: Record<string, string> = {}
    if (options.etag) headers["If-None-Match"] = options.etag
    const response = await consoleFetch("/api/plugins", {
      method: "GET",
      headers,
      cache: "no-store",
      signal: options.signal,
    })
    const etag = response.headers.get("etag") ?? options.etag ?? ""
    if (response.status === 304) {
      return { kind: "not_modified", etag }
    }
    if (response.status === 401) {
      return { kind: "unauthorized" }
    }
    if (response.status === 404) {
      return {
        kind: "unsupported",
        state: buildDefaultPluginState({ supported: false, configured: false }),
      }
    }
    if (!response.ok) {
      const errBody = await response.json().catch(() => null) as { message?: string; error?: string } | null
      return { kind: "error", message: errBody?.message || errBody?.error || `读取插件配置失败（HTTP ${response.status}）` }
    }
    const state = (await response.json()) as PluginStateResponse
    return { kind: "ok", state, etag }
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      return { kind: "not_modified", etag: options.etag ?? "" }
    }
    return { kind: "error", message: err instanceof Error ? err.message : "读取插件配置失败" }
  }
}

export type PluginMutationResult =
  | { ok: true; data: PluginMutationResponse }
  | { ok: false; status: number; code?: string; message: string; conflict?: boolean }

function computeDemoDiff(
  beforeScenario: string,
  beforeOverrides: Record<string, boolean>,
  afterScenario: string,
  afterOverrides: Record<string, boolean>,
) {
  const before = buildDefaultPluginState({
    scenarioId: beforeScenario,
    overrides: beforeOverrides,
    extraPlugins: buildDemoL2Plugins(beforeScenario, beforeOverrides),
  })
  const after = buildDefaultPluginState({
    scenarioId: afterScenario,
    overrides: afterOverrides,
    extraPlugins: buildDemoL2Plugins(afterScenario, afterOverrides),
  })
  const beforeMap = new Map(before.plugins.map((p) => [p.id, p.effective]))
  const show: string[] = []
  const hide: string[] = []
  for (const p of after.plugins) {
    const prev = beforeMap.get(p.id) ?? false
    if (!prev && p.effective) show.push(p.id)
    if (prev && !p.effective) hide.push(p.id)
  }
  const dropped_overrides = Object.keys(beforeOverrides)
    .filter((id) => !Object.prototype.hasOwnProperty.call(afterOverrides, id))
    .sort()
  const affected_schedules = hide.includes("doops.inspection") || hide.includes("doops.automation")
    ? [{ id: "sched_demo01", name: "全策略定时巡检", skills: ["inspect-vm-host"], note: "定时计划将继续按原周期运行，不会被自动停用或删除" }]
    : []
  return { show, hide, dropped_overrides, affected_schedules }
}

export async function mutatePluginState(
  session: Session,
  req: PluginMutationRequest,
): Promise<PluginMutationResult> {
  if (session.demo) {
    if (!sessionIsAdmin(session)) {
      return { ok: false, status: 403, code: "forbidden", message: "仅平台管理员可修改插件与场景配置" }
    }
    if (req.expected_revision !== demoStore.revision) {
      return {
        ok: false,
        status: 409,
        code: "plugin_revision_conflict",
        message: "插件配置已被其他管理员修改，请刷新后重试",
        conflict: true,
      }
    }
    let nextScenario = demoStore.scenarioId
    let nextOverrides: Record<string, boolean> = { ...demoStore.overrides }
    if (req.action === "apply_scenario") {
      if (!findScenarioMeta(req.scenario_id)) {
        return { ok: false, status: 400, code: "unknown_scenario", message: "未找到指定的场景包" }
      }
      nextScenario = req.scenario_id
      nextOverrides = {}
    } else if (req.action === "reset") {
      nextScenario = DEFAULT_PLUGIN_SCENARIO_ID
      nextOverrides = {}
    } else if (req.action === "set_plugin") {
      const demoL2 = DEMO_L2_MANIFESTS.find((m) => m.plugin.id === req.plugin_id)
      if (!findBuiltinPluginMeta(req.plugin_id) && !demoL2) {
        return { ok: false, status: 400, code: "invalid_plugin_id", message: "插件标识无效或不可关闭" }
      }
      const def = demoL2
        ? (demoL2.plugin.opt_in
            ? false
            : (demoL2.plugin.scenarios.length === 0 || demoL2.plugin.scenarios.includes(nextScenario)))
        : BUILTIN_PLUGIN_CATALOG.scenarios.find((s) => s.id === nextScenario)!.defaults.includes(req.plugin_id)
      if (req.enabled === def) delete nextOverrides[req.plugin_id]
      else nextOverrides[req.plugin_id] = req.enabled
    } else if (req.action === "clear_override") {
      delete nextOverrides[req.plugin_id]
    }
    const diff = computeDemoDiff(demoStore.scenarioId, demoStore.overrides, nextScenario, nextOverrides)
    const dryRun = "dry_run" in req && Boolean(req.dry_run)
    const customized = Object.keys(nextOverrides).length > 0
    if (dryRun) {
      return {
        ok: true,
        data: { dry_run: true, revision: demoStore.revision, scenario_id: nextScenario, customized, diff },
      }
    }
    demoStore = {
      configured: true,
      scenarioId: nextScenario,
      revision: demoStore.revision + 1,
      overrides: nextOverrides,
      updatedBy: session.username || "demo-admin",
      updatedAtMs: Date.now(),
    }
    return {
      ok: true,
      data: { dry_run: false, revision: demoStore.revision, scenario_id: nextScenario, customized, diff },
    }
  }

  try {
    const response = await consoleFetch("/api/admin/plugins", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
      cache: "no-store",
    })
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { code?: string; message?: string; error?: string } | null
      return {
        ok: false,
        status: response.status,
        code: body?.code,
        message: body?.message || body?.error || `保存插件配置失败（HTTP ${response.status}）`,
        conflict: response.status === 409 || body?.code === "plugin_revision_conflict",
      }
    }
    const data = (await response.json()) as PluginMutationResponse
    return { ok: true, data }
  } catch (err) {
    return {
      ok: false,
      status: 0,
      message: err instanceof Error ? err.message : "保存插件配置失败",
    }
  }
}
