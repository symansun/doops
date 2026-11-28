import assert from "node:assert/strict"
import test from "node:test"
import {
  buildSuggestionId,
  classifyTerminalInput,
  classifyTerminalShortcut,
  formatBracketedPaste,
  looksLikeNaturalLanguageMistype,
  resolveTerminalIntentTarget,
} from "./terminal-intent.ts"

test("classifyTerminalInput handles deterministic # and ?/？ (suggest) and @ai / ## (assistant) prefixes regardless of autoDetect", () => {
  const h = classifyTerminalInput("# 为什么 nginx 一直重启", { autoDetect: false, defaultAiMode: "assistant" })
  assert.equal(h.kind, "ai")
  assert.equal(h.trigger, "prefix_hash")
  assert.equal(resolveTerminalIntentTarget(h, "assistant"), "suggest")
  assert.equal(h.question, "为什么 nginx 一直重启")

  const q1 = classifyTerminalInput("? check why pod is crashing", { autoDetect: false })
  assert.equal(q1.kind, "ai")
  assert.equal(q1.trigger, "prefix_question")
  assert.equal(resolveTerminalIntentTarget(q1, "assistant"), "suggest")
  assert.equal(q1.question, "check why pod is crashing")

  const q2 = classifyTerminalInput("？帮我看下磁盘占用", { autoDetect: false })
  assert.equal(q2.kind, "ai")
  assert.equal(q2.trigger, "prefix_question")
  assert.equal(q2.question, "帮我看下磁盘占用")

  // Mode 2 deterministic prefixes: @ai and ## always route to AI assistant regardless of default mode.
  const a1 = classifyTerminalInput("@ai 排查 prod 命名空间异常 Pod", { autoDetect: false, defaultAiMode: "suggest" })
  assert.equal(a1.kind, "ai")
  assert.equal(a1.trigger, "prefix_agent")
  assert.equal(resolveTerminalIntentTarget(a1, "suggest"), "assistant")
  assert.equal(a1.question, "排查 prod 命名空间异常 Pod")

  const a2 = classifyTerminalInput("## 检查宿主机磁盘占用", { autoDetect: false, defaultAiMode: "suggest" })
  assert.equal(a2.kind, "ai")
  assert.equal(a2.trigger, "prefix_agent")
  assert.equal(resolveTerminalIntentTarget(a2, "suggest"), "assistant")
  assert.equal(a2.question, "检查宿主机磁盘占用")

  // Bare @ai or ## without a task must stay shell.
  assert.equal(classifyTerminalInput("@ai", { autoDetect: true }).kind, "shell")
  assert.equal(classifyTerminalInput("##", { autoDetect: true }).kind, "shell")

  // Auto-detected natural language follows the active defaultAiMode.
  const autoAssistant = classifyTerminalInput("帮我看下为什么 pod 一直在重启？", { autoDetect: true, defaultAiMode: "assistant" })
  assert.equal(autoAssistant.kind, "ai")
  assert.equal(autoAssistant.trigger, "auto")
  assert.equal(resolveTerminalIntentTarget(autoAssistant, "assistant"), "assistant")

  // autoDetect=false 时自然语言保持 shell（不拦截）
  const off = classifyTerminalInput("帮我看下为什么 pod 一直在重启？", { autoDetect: false })
  assert.equal(off.kind, "shell")
})

test("classifyTerminalInput autoDetect has 0 false positives on 40+ shell commands and catches natural language", () => {
  const shellCases = [
    "ls",
    "ls -la /var/log",
    "ls 为什么这么慢",
    "cd /srv/app",
    "pwd",
    "kubectl get pods -A",
    "kubectl -n web logs nginx-7c9 --tail=100",
    "kubectl describe pod my-pod",
    "helm list -A",
    "helm upgrade --install app ./chart",
    "docker ps -a",
    "docker logs -f api",
    "git status",
    "git log --oneline -n 10",
    "grep -rn '错误' /var/log/nginx",
    "find /etc -name '*.conf'",
    "ps aux | grep nginx",
    "df -h",
    "du -sh *",
    "free -m",
    "ss -tulpn",
    "curl -I http://127.0.0.1:8080/health",
    "systemctl status nginx",
    "journalctl -u doops-agent -n 50 --no-pager",
    "nginx -t && nginx -s reload",
    "psql -U postgres -c 'select 1'",
    "redis-cli ping",
    "tail -f /var/log/syslog",
    "cat /etc/hosts",
    "echo '帮我看下日志'",
    "FOO=bar ./run.sh",
    "./scripts/deploy.sh --dry-run",
    "/usr/local/bin/doops targets",
    "~/bin/tool --help",
    "sudo systemctl restart docker",
    "python3 -m http.server 8000",
    "go test ./...",
    "pnpm build",
    "top",
    "vim /etc/nginx/nginx.conf",
    "tar -czf backup.tar.gz /data",
    "a",
  ]
  for (const cmd of shellCases) {
    const res = classifyTerminalInput(cmd, { autoDetect: true })
    assert.equal(res.kind, "shell", `shell command misclassified as AI: ${cmd}`)
  }

  const aiCases = [
    "为什么 nginx 一直重启？",
    "帮我看下磁盘空间为什么满了",
    "怎么查看最近 5 分钟的错误日志？",
    "排查一下 8080 端口为什么连不上",
    "请帮我检查当前集群有哪些异常的容器",
    "如何清理旧的镜像释放磁盘空间",
    "查一下数据库连接池为什么耗尽了",
    "解释一下刚才那条命令的报错原因",
    "帮我找出占用内存最高的前五个进程",
    "诊断一下为什么服务响应特别慢？",
    "分析下当前机器的负载情况",
    "看看最近有哪些服务重启过",
    "总结一下刚刚的错误堆栈",
    "检查一下证书是不是快过期了",
    "告诉我怎么重启这个服务最安全",
    "如何查看所有命名空间下的异常负载？",
    "为什么刚才的构建失败了",
    "帮忙写一条查找大文件的命令",
    "对比一下这两个配置文件的区别",
    "列出最近修改过的配置文件",
    "我想查一下系统的cpu",
    "我要看下内存使用情况",
    "查看80端口占用",
    "系统负载高怎么看",
    "磁盘空间满了",
    "为什么连不上 mysql://root:CanaryTerm05Pass!@10.0.0.1 查一下端口",
    "帮我看看 https://api.example.com/v1/health/check 超时原因",
    "查看 /var/log/nginx/error.log 为什么一直报错",
    "how do I check cpu usage",
    "why is nginx failing?",
  ]
  for (const q of aiCases) {
    const res = classifyTerminalInput(q, { autoDetect: true })
    assert.equal(res.kind, "ai", `natural language not detected as AI: ${q}`)
    assert.equal(res.trigger, "auto")
    assert.equal(looksLikeNaturalLanguageMistype(q), true)
  }
  assert.equal(looksLikeNaturalLanguageMistype("kubectl get pods"), false)
})

test("formatBracketedPaste wraps multiline commands safely", () => {
  assert.equal(
    formatBracketedPaste("echo a\necho b", { run: false, clearLine: true }),
    "\x15\x1b[200~echo a\necho b\x1b[201~",
  )
  assert.equal(
    formatBracketedPaste("kubectl get pods", { run: true, clearLine: false }),
    "\x1b[200~kubectl get pods\x1b[201~\r",
  )
})

test("M1 regression: shortcuts never hijack bare Ctrl+I (Tab) or alternate screen keys", () => {
  // Bare Ctrl+I is Tab (0x09) in Linux/Windows terminals -> must return null
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "i", ctrlKey: true }, false), null)
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "ArrowUp", ctrlKey: true }, false), null)

  // Normal screen: Cmd+I and Ctrl+Shift+I open inline AI; Cmd+Up / Ctrl+Shift+Up jump
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "i", metaKey: true }, false), "inline_ai")
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "I", ctrlKey: true, shiftKey: true }, false), "inline_ai")
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "ArrowUp", metaKey: true }, false), "jump_prev")
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "ArrowDown", ctrlKey: true, shiftKey: true }, false), "jump_next")

  // Alternate screen (vim/top/less): every shortcut passes through
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "i", metaKey: true }, true), null)
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "I", ctrlKey: true, shiftKey: true }, true), null)
  assert.equal(classifyTerminalShortcut({ type: "keydown", key: "ArrowUp", metaKey: true }, true), null)
})

test("M6 regression: buildSuggestionId produces unique auditable IDs across turns", () => {
  const id1 = buildSuggestionId("msg-turn-001", 0, "kubectl get pods")
  const id2 = buildSuggestionId("msg-turn-002", 0, "kubectl get pods")
  const id3 = buildSuggestionId("msg-turn-001", 1, "kubectl describe pod foo")
  assert.notEqual(id1, id2)
  assert.notEqual(id1, id3)
  for (const id of [id1, id2, id3]) {
    assert.match(id, /^[A-Za-z0-9._-]{1,64}$/)
  }
})
