import assert from "node:assert/strict"
import test from "node:test"
import { cleanPath, extractDigests, imageBuildRequest, imageCopyRequest, registrySyncRequest } from "./image.ts"

const build = { context: "/root/ws/image", dockerfile: "", platform: "linux/amd64,linux/arm64", output: "oci", outputPath: "/tmp/image.oci", image: "", push: false }
const copy = { source: "docker://registry.example/source/app:release", destination: "oci-archive:/tmp/app.oci", sourceAuthfile: "/run/source-auth.json", destinationAuthfile: "", all: true, verify: true, timeout: 600 }
const sync = { sourceRegistry: "registry.example.com/source/team", targetRegistry: "registry.example.com/mirror/team", tag: "release-20261004", images: "api, worker", sourceAuthfile: "/run/doops/source-auth.json", targetAuthfile: "/run/doops/target-auth.json", timeout: 600 }

const PAYLOAD_KEYS = ["payload", "password", "token", "secret", "credential_payload", "username", "data"]

function assertNoPayload(request) {
  assert.deepEqual(Object.keys(request).sort(), ["arguments", "tool"])
  assert.deepEqual(Object.keys(request.arguments).sort(), ["command", "session_id"])
  for (const key of PAYLOAD_KEYS) assert.equal(key in request.arguments, false)
}

test("image build maps form fields to the CLI BuildKit command", () => {
  const cases = [
    {
      name: "oci archive with default Dockerfile",
      input: build,
      want: ["command -v buildctl", "test -S /run/buildkit/buildkitd.sock", "buildctl --addr 'unix:///run/buildkit/buildkitd.sock' build --progress=plain --frontend dockerfile.v0 --local context='/root/ws/image' --local dockerfile='/root/ws/image' --opt filename='Dockerfile'", "--opt 'platform=linux/amd64,linux/arm64'", "--output 'type=oci,dest=/tmp/image.oci'"],
    },
    {
      name: "image output pushed with explicit Dockerfile",
      input: { ...build, dockerfile: "/root/ws/image/docker/Dockerfile.prod", output: "image", outputPath: "", image: "registry.example/team/app:v1", push: true },
      want: ["--local dockerfile='/root/ws/image/docker' --opt filename='Dockerfile.prod'", "--output 'type=image,name=registry.example/team/app:v1,push=true'"],
    },
    {
      name: "no platform omits the platform opt",
      input: { ...build, platform: "" },
      unwanted: ["platform="],
    },
  ]
  for (const c of cases) {
    const request = imageBuildRequest("img-test", c.input, true)
    assert.equal(request.tool, "doops_shell", c.name)
    assert.equal(request.arguments.session_id, "img-test", c.name)
    for (const want of c.want || []) assert.ok(request.arguments.command.includes(want), `${c.name}: missing ${want}`)
    for (const unwanted of c.unwanted || []) assert.ok(!request.arguments.command.includes(unwanted), `${c.name}: unexpected ${unwanted}`)
    assertNoPayload(request)
  }
})

test("image build rejects inputs the CLI rejects", () => {
  for (const [name, invalid] of [
    ["missing context", { context: " " }],
    ["oci without path", { outputPath: "" }],
    ["image without reference", { output: "image", image: "" }],
    ["push with oci", { push: true }],
    ["unknown output", { output: "tar" }],
    ["dockerfile names a directory", { dockerfile: "/" }],
    ["newline injection", { context: "/root/ws\nrm -rf /tmp/x" }],
  ]) {
    assert.throws(() => imageBuildRequest("img-test", { ...build, ...invalid }, true), undefined, name)
  }
})

test("image copy maps flags, authfiles and verification like the CLI", () => {
  const cases = [
    {
      name: "verified copy with source authfile",
      input: copy,
      want: ["command -v skopeo", "command -v sha256sum", "timeout 600s 'skopeo' 'copy' '--all' '--retry-times' '3' '--src-authfile' '/run/source-auth.json' 'docker://registry.example/source/app:release' 'oci-archive:/tmp/app.oci'", "'skopeo' 'inspect' '--raw' '--authfile' '/run/source-auth.json'", "image manifest digest mismatch", "manifest_digest=sha256:"],
      unwanted: ["--dest-authfile"],
    },
    {
      name: "single manifest without verification",
      input: { ...copy, all: false, verify: false, sourceAuthfile: "", destinationAuthfile: "/run/dst.json", timeout: 30 },
      want: ["timeout 30s 'skopeo' 'copy' '--retry-times' '3' '--dest-authfile' '/run/dst.json'"],
      unwanted: ["'--all'", "sha256sum", "inspect", "--src-authfile"],
    },
  ]
  for (const c of cases) {
    const request = imageCopyRequest("img-test", c.input, true)
    for (const want of c.want) assert.ok(request.arguments.command.includes(want), `${c.name}: missing ${want}`)
    for (const unwanted of c.unwanted) assert.ok(!request.arguments.command.includes(unwanted), `${c.name}: unexpected ${unwanted}`)
    assertNoPayload(request)
  }
  for (const invalid of [{ source: "" }, { destination: "" }, { timeout: 0 }, { timeout: 1.5 }]) {
    assert.throws(() => imageCopyRequest("img-test", { ...copy, ...invalid }, true))
  }
})

test("registry sync copies every image and checks each digest", () => {
  const request = registrySyncRequest("img-test", sync, true)
  const command = request.arguments.command
  for (const want of [
    "skopeo copy --all --retry-times 3 --src-authfile '/run/doops/source-auth.json' --dest-authfile '/run/doops/target-auth.json'",
    "'docker://registry.example.com/source/team/api:release-20261004'",
    "'docker://registry.example.com/mirror/team/worker:release-20261004'",
    "skopeo inspect --raw --authfile '/run/doops/source-auth.json'",
    "registry manifest digest mismatch for worker",
  ]) assert.ok(command.includes(want), `missing ${want}`)
  assertNoPayload(request)

  const bare = registrySyncRequest("img-test", { ...sync, images: "api", sourceAuthfile: "", targetAuthfile: "" }, true).arguments.command
  for (const unwanted of ["--src-authfile", "--dest-authfile", "--authfile"]) assert.ok(!bare.includes(unwanted))

  for (const invalid of [{ images: "" }, { tag: "" }, { tag: "v1;rm" }, { sourceRegistry: "reg example" }, { images: "api $(id)" }, { timeout: -1 }]) {
    assert.throws(() => registrySyncRequest("img-test", { ...sync, ...invalid }, true))
  }
})

test("no request is built before the user confirms", () => {
  assert.throws(() => imageBuildRequest("img-test", build, false), /确认/)
  assert.throws(() => imageCopyRequest("img-test", copy, false), /确认/)
  assert.throws(() => registrySyncRequest("img-test", sync, false), /确认/)
})

test("shell quoting keeps single quotes inside one argument", () => {
  const command = imageCopyRequest("img-test", { ...copy, source: "docker://r/a:it's" }, true).arguments.command
  assert.ok(command.includes(`'docker://r/a:it'"'"'s'`))
})

test("path cleaning and digest extraction", () => {
  assert.equal(cleanPath("/root/ws//image/./Dockerfile"), "/root/ws/image/Dockerfile")
  assert.equal(cleanPath("ctx/../Dockerfile"), "Dockerfile")
  const digest = "sha256:" + "c".repeat(64)
  assert.deepEqual(extractDigests(`manifest_digest=${digest}\nexporting ${digest}`), [digest])
})
