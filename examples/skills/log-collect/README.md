# 日志收集打包（log-collect）

通过 doops 在目标节点打包最近有修改的 `.log` 文件，放入会话工作区后用 `doops pull` 拉回本机。

## 前置条件

- 本机已安装 doops CLI 并 `doops login` 到目标 gateway target
- 目标节点 agent 的会话工作区为默认路径 `/root/ws/<session>`；若目标设置过 `DOOPS_WORKSPACE_ROOT`，请先修改 `run.sh` 中的 `WS` 变量

## 用法

```bash
bash examples/skills/log-collect/run.sh \
  --target <gateway-target> \
  [--dir /var/log] [--mtime -1] [--out ./logs-collected] [--session log-collect]
```

流程：

1. 目标节点 `find <dir> -name '*.log' -mtime <n>` 找出最近修改的日志并 `tar -czf` 到会话工作区
2. `doops pull` 把整个会话工作区拉到 `--out` 目录（tar.gz 是二进制，不要走 `read`/`exec base64`）
3. 清理目标节点上的临时归档

没有找到符合条件的日志时脚本以退出码 3 结束。

## 测试说明

1. 在测试节点上造一条新日志：`doops -session log-collect exec --target <t> --cmd 'echo test >> /var/log/doops-demo.log'`
2. 运行脚本，预期在 `--out` 目录下得到包含 `doops-demo.log` 的 tar.gz，解开可见内容。
3. 反向：`--mtime 0`（今天无修改的目录，如 `--dir /opt`）应输出 `no recent .log files` 并以退出码 3 结束。
