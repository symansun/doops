import assert from "node:assert/strict"
import test from "node:test"
import { formatReadyList, labelSelectorString, parseAlignedColumns } from "./workbench-parse.ts"

test("aligned columns keep single spaces inside a cell", () => {
  const text = [
    "NS   NAME   SCHEDULE   SUSPEND",
    "apps   nightly   0 2 * * *   false",
  ].join("\n")
  assert.deepEqual(parseAlignedColumns(text), [["apps", "nightly", "0 2 * * *", "false"]])
})

test("aligned columns turn <none> into empty and accept a header-only list", () => {
  assert.deepEqual(parseAlignedColumns("NS   NAME   READY\napps   orders   <none>\n"), [["apps", "orders", ""]])
  assert.deepEqual(parseAlignedColumns("NAME   STATUS\n"), [])
})

test("aligned columns reject a ragged row", () => {
  assert.equal(parseAlignedColumns("NS   NAME\napps\n"), null)
})

test("selector JSON from kubectl becomes an exact label selector", () => {
  const cases = [
    { name: "match labels", input: '{"matchLabels":{"k8s-app":"kube-dns"}}', want: "k8s-app=kube-dns" },
    { name: "prefixed key", input: '{"matchLabels":{"app.kubernetes.io/name":"orders","tier":"api"}}', want: "app.kubernetes.io/name=orders,tier=api" },
    {
      name: "labels and expressions",
      input: '{"matchExpressions":[{"key":"track","operator":"In","values":["stable","canary"]},{"key":"legacy","operator":"DoesNotExist"},{"key":"zone","operator":"NotIn","values":["b"]},{"key":"gpu","operator":"Exists"}],"matchLabels":{"app":"orders"}}',
      want: "app=orders,track in (stable,canary),!legacy,zone notin (b),gpu",
    },
    { name: "empty label value", input: '{"matchLabels":{"canary":""}}', want: "canary=" },
  ]
  for (const tc of cases) assert.equal(labelSelectorString(tc.input), tc.want, tc.name)
})

test("selector that cannot be written exactly is not sent", () => {
  const cases = [
    { name: "empty output", input: "" },
    { name: "not JSON", input: "app=orders," },
    { name: "empty selector", input: "{}" },
    { name: "empty labels", input: '{"matchLabels":{}}' },
    { name: "array", input: '["app"]' },
    { name: "value with comma", input: '{"matchLabels":{"app":"a,b"}}' },
    { name: "value with space", input: '{"matchLabels":{"app":"a b"}}' },
    { name: "non-string value", input: '{"matchLabels":{"replicas":2}}' },
    { name: "bad key", input: '{"matchLabels":{"-app":"x"}}' },
    { name: "unknown operator", input: '{"matchExpressions":[{"key":"app","operator":"Gt","values":["1"]}]}' },
    { name: "In without values", input: '{"matchExpressions":[{"key":"app","operator":"In","values":[]}]}' },
    { name: "Exists with values", input: '{"matchExpressions":[{"key":"app","operator":"Exists","values":["x"]}]}' },
    { name: "expression value with paren", input: '{"matchExpressions":[{"key":"app","operator":"In","values":["a)"]}]}' },
  ]
  for (const tc of cases) assert.equal(labelSelectorString(tc.input), null, tc.name)
})

test("ready list counts true flags", () => {
  assert.equal(formatReadyList("true,false,true"), "2/3")
  assert.equal(formatReadyList("Running"), "Running")
  assert.equal(formatReadyList(""), "")
})
