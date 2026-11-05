// 没有可用目标时的空状态文案。
// /v1/targets 对普通用户已按授权过滤，Web 分不清「没有授权」和「已授权但机器都不在线」，
// 所以普通用户的文案同时说明两种可能；部署 Agent 的引导只对管理员有意义。
export interface EmptyTargetsCopy {
  title: string
  description: string
  showDeployAgent: boolean
  contactTitle: string
  contactHint: string
}

const ADMIN_COPY: EmptyTargetsCopy = {
  title: "还没有纳管的机器",
  description: "Doops 通过在你的服务器上安装一个轻量 agent 来工作。你可以自己部署一台，或联系管理员为你接入。",
  showDeployAgent: true,
  contactTitle: "联系管理员",
  contactHint: "没有服务器权限或不确定 gateway 地址与 cluster/instance？请管理员为你接入机器。",
}

const USER_COPY: EmptyTargetsCopy = {
  title: "你当前没有可用的目标",
  description: "可能是你还没有获得任何目标的授权，也可能是已授权的机器目前都不在线。",
  showDeployAgent: false,
  contactTitle: "联系平台管理员",
  contactHint: "请把你的用户名和需要使用的集群/实例告诉平台管理员，确认授权和机器状态。授权生效或机器上线后，点「刷新」即可看到。",
}

export function emptyTargetsCopy(isAdmin: boolean): EmptyTargetsCopy {
  return isAdmin ? ADMIN_COPY : USER_COPY
}

export interface TargetSwitcherEmptyCopy {
  label: string
  hint: string
}

// isAdmin 未传入时（调用方没有会话信息）用对两种角色都成立的说法，不给部署引导。
export function targetSwitcherEmptyCopy(isAdmin?: boolean): TargetSwitcherEmptyCopy {
  if (isAdmin === true) return { label: "暂无在线机器", hint: "还没有在线的 Agent，部署或接入后点刷新" }
  if (isAdmin === false) return { label: "暂无可用目标", hint: `${USER_COPY.description}请联系平台管理员确认授权。` }
  return { label: "暂无可用目标", hint: "没有在线且你有权使用的机器，点刷新重新查询" }
}
