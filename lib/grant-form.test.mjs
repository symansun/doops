import assert from "node:assert/strict"
import test from "node:test"
import {
  GRANT_ALL,
  grantInstanceChoices,
  grantScopeOptions,
  grantScopeWarning,
  validateGrantForm,
} from "./grant-form.ts"

const INSTANCES = [
  { cluster: "staging", instance: "db-01", status: "offline", busy: false, active_ops: 0, queued_ops: 0 },
  { cluster: "prod-cn", instance: "web-02", status: "online", busy: true, active_ops: 2, queued_ops: 1 },
  { cluster: "prod-cn", instance: "web-01", status: "online", busy: false, active_ops: 1, queued_ops: 0 },
  { cluster: "prod-cn", instance: "web-01", status: "online", busy: false, active_ops: 0, queued_ops: 0 },
  { cluster: " ", instance: "orphan", status: "offline", busy: false, active_ops: 0, queued_ops: 0 },
  { cluster: "*", instance: "web-09", status: "offline", busy: false, active_ops: 0, queued_ops: 0 },
  { cluster: "edge", instance: "*", status: "offline", busy: false, active_ops: 0, queued_ops: 0 },
]
const SCOPE = grantScopeOptions(INSTANCES)

test("instance list becomes sorted, de-duplicated cluster/instance choices without blanks or wildcards", () => {
  assert.deepEqual(SCOPE, [
    { name: "prod-cn", instances: ["web-01", "web-02"] },
    { name: "staging", instances: ["db-01"] },
  ])
  assert.deepEqual(grantScopeOptions([]), [])
})

test("instance choices follow the selected cluster", () => {
  const cases = [
    { cluster: "", want: [] },
    { cluster: "prod-cn", want: ["web-01", "web-02"] },
    { cluster: "staging", want: ["db-01"] },
    { cluster: "unknown", want: [] },
    { cluster: GRANT_ALL, want: [] },
  ]
  for (const { cluster, want } of cases) {
    assert.deepEqual(grantInstanceChoices(SCOPE, cluster), want, `cluster=${JSON.stringify(cluster)}`)
  }
})

test("grant form only submits an explicitly chosen user, cluster and instance", () => {
  const cases = [
    { name: "nothing chosen", input: { userId: "", cluster: "", instance: "" }, error: "请选择用户" },
    { name: "cluster defaults to empty", input: { userId: "usr_ops01", cluster: "", instance: "" }, error: "请选择集群" },
    { name: "blank cluster", input: { userId: "usr_ops01", cluster: "  ", instance: "web-01" }, error: "请选择集群" },
    { name: "instance defaults to empty", input: { userId: "usr_ops01", cluster: "prod-cn", instance: "" }, error: "请选择实例" },
    { name: "blank instance", input: { userId: "usr_ops01", cluster: "prod-cn", instance: " " }, error: "请选择实例" },
    { name: "all clusters with one instance", input: { userId: "usr_ops01", cluster: GRANT_ALL, instance: "web-01" }, error: "选择全部集群时，实例只能选全部" },
    { name: "cluster not in instance list", input: { userId: "usr_ops01", cluster: "prod-us", instance: "web-01" }, error: "所选集群不在实例列表中，请刷新后重新选择" },
    { name: "instance from another cluster", input: { userId: "usr_ops01", cluster: "staging", instance: "web-01" }, error: "所选实例不在该集群中，请重新选择" },
    { name: "one listed instance", input: { userId: "usr_ops01", cluster: "prod-cn", instance: "web-02" }, body: { user_id: "usr_ops01", cluster: "prod-cn", instance: "web-02" } },
    { name: "values are trimmed", input: { userId: " usr_ops01 ", cluster: " staging ", instance: " db-01 " }, body: { user_id: "usr_ops01", cluster: "staging", instance: "db-01" } },
    { name: "explicit whole cluster", input: { userId: "usr_ops01", cluster: "prod-cn", instance: GRANT_ALL }, body: { user_id: "usr_ops01", cluster: "prod-cn", instance: "*" } },
    { name: "explicit everything", input: { userId: "usr_ops01", cluster: GRANT_ALL, instance: GRANT_ALL }, body: { user_id: "usr_ops01", cluster: "*", instance: "*" } },
    { name: "explicit everything without any instance listed", input: { userId: "usr_ops01", cluster: GRANT_ALL, instance: GRANT_ALL }, scope: [], body: { user_id: "usr_ops01", cluster: "*", instance: "*" } },
    { name: "named cluster without any instance listed", input: { userId: "usr_ops01", cluster: "prod-cn", instance: GRANT_ALL }, scope: [], error: "所选集群不在实例列表中，请刷新后重新选择" },
  ]
  for (const { name, input, scope = SCOPE, error, body } of cases) {
    const result = validateGrantForm(input, scope)
    if (error) {
      assert.deepEqual(result, { ok: false, error }, name)
    } else {
      assert.deepEqual(result, { ok: true, body }, name)
    }
  }
})

test("a valid grant form never carries an empty cluster or instance", () => {
  const values = ["", " ", GRANT_ALL, "prod-cn", "staging", "web-01", "db-01"]
  for (const cluster of values) {
    for (const instance of values) {
      const result = validateGrantForm({ userId: "usr_ops01", cluster, instance }, SCOPE)
      if (!result.ok) continue
      assert.notEqual(result.body.cluster, "", `cluster=${JSON.stringify(cluster)} instance=${JSON.stringify(instance)}`)
      assert.notEqual(result.body.instance, "", `cluster=${JSON.stringify(cluster)} instance=${JSON.stringify(instance)}`)
    }
  }
})

test("wildcard choices show an explicit warning before submission", () => {
  const cases = [
    { cluster: "", instance: "", includes: [] },
    { cluster: "prod-cn", instance: "", includes: [] },
    { cluster: "prod-cn", instance: "web-01", includes: [] },
    { cluster: GRANT_ALL, instance: GRANT_ALL, includes: ["将授予该用户全部集群/实例", "cluster=*", "instance=*"] },
    { cluster: "prod-cn", instance: GRANT_ALL, includes: ["将授予该用户集群 prod-cn 的全部实例", "instance=*"] },
  ]
  for (const { cluster, instance, includes } of cases) {
    const warning = grantScopeWarning(cluster, instance)
    const label = `cluster=${JSON.stringify(cluster)} instance=${JSON.stringify(instance)}`
    if (!includes.length) {
      assert.equal(warning, "", label)
      continue
    }
    for (const text of includes) assert.ok(warning.includes(text), `${label}: ${warning}`)
  }
})
