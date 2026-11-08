# nginx 巡检（nginx-patrol）

通过 doops 在目标节点执行 nginx 健康巡检：进程状态、配置语法、监听端口、错误日志尾部。

## 前置条件

- 本机已安装 doops CLI 并 `doops login` 到目标 gateway target
- 目标节点运行 nginx（ systemd 或裸进程均可）

## 用法

```bash
bash examples/skills/nginx-patrol/run.sh --target <gateway-target> [--session nginx-patrol]
```

目标节点上 nginx 未运行或 `nginx -t` 失败时脚本以非零退出。

## 测试说明

1. 本地链路：先跑 `bash deploy/e2e/smoke.sh --mode process` 确认 doops 链路可用。
2. 在装了 nginx 的测试节点上注册一个 target（如 `staging`），执行：

   ```bash
   bash examples/skills/nginx-patrol/run.sh --target staging
   ```

   预期输出四段巡检结果并以 `✅ nginx 巡检完成` 结束。
3. 反向用例：停掉 nginx 后重跑，第一段应以非零退出。
