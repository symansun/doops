import assert from "node:assert/strict"
import test from "node:test"
import { emptyTargetsCopy, targetSwitcherEmptyCopy } from "./empty-targets.ts"

test("overview empty state keeps the admin copy and deploy guide only for admins", () => {
  const cases = [
    {
      name: "admin",
      isAdmin: true,
      want: {
        title: "还没有纳管的机器",
        description: "Doops 通过在你的服务器上安装一个轻量 agent 来工作。你可以自己部署一台，或联系管理员为你接入。",
        showDeployAgent: true,
        contactTitle: "联系管理员",
        contactHint: "没有服务器权限或不确定 gateway 地址与 cluster/instance？请管理员为你接入机器。",
      },
    },
    {
      name: "ordinary user",
      isAdmin: false,
      want: {
        title: "你当前没有可用的目标",
        description: "可能是你还没有获得任何目标的授权，也可能是已授权的机器目前都不在线。",
        showDeployAgent: false,
        contactTitle: "联系平台管理员",
        contactHint: "请把你的用户名和需要使用的集群/实例告诉平台管理员，确认授权和机器状态。授权生效或机器上线后，点「刷新」即可看到。",
      },
    },
  ]
  for (const { name, isAdmin, want } of cases) {
    assert.deepEqual(emptyTargetsCopy(isAdmin), want, name)
  }
})

test("ordinary users are told both possible causes and never pointed at deploying an Agent", () => {
  const copy = emptyTargetsCopy(false)
  const text = Object.values(copy).filter((value) => typeof value === "string").join("\n")
  assert.match(text, /授权/)
  assert.match(text, /不在线/)
  assert.match(text, /平台管理员/)
  for (const word of ["部署", "agent", "Agent", "纳管", "gateway 地址"]) {
    assert.ok(!text.includes(word), `ordinary user copy must not mention ${JSON.stringify(word)}`)
  }
})

test("target switcher empty label depends on whether the role is known", () => {
  const cases = [
    { name: "admin", isAdmin: true, label: "暂无在线机器", hintHas: ["部署"], hintLacks: ["授权"] },
    { name: "ordinary user", isAdmin: false, label: "暂无可用目标", hintHas: ["授权", "不在线", "平台管理员"], hintLacks: ["部署", "Agent"] },
    { name: "role not passed", isAdmin: undefined, label: "暂无可用目标", hintHas: ["刷新"], hintLacks: ["部署", "Agent", "在线机器"] },
  ]
  for (const { name, isAdmin, label, hintHas, hintLacks } of cases) {
    const copy = targetSwitcherEmptyCopy(isAdmin)
    assert.equal(copy.label, label, name)
    for (const word of hintHas) assert.ok(copy.hint.includes(word), `${name}: hint should mention ${word}`)
    for (const word of hintLacks) assert.ok(!copy.hint.includes(word), `${name}: hint must not mention ${word}`)
  }
})
