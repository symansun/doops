# 证书到期巡检（cert-expiry-check）

通过 doops 在目标节点用 `openssl s_client` 检查一组域名的 TLS 证书剩余有效期，剩余天数低于阈值时输出 WARN 并以非零退出。

## 前置条件

- 本机已安装 doops CLI 并 `doops login` 到目标 gateway target
- 目标节点有 `openssl` 且能访问被巡检域名的 443 端口

## 用法

```bash
bash examples/skills/cert-expiry-check/run.sh \
  --target <gateway-target> \
  --domains example.com,api.example.com \
  [--warn-days 30] [--session cert-expiry-check]
```

输出形如：

```text
OK    example.com 剩余 71 天 (到期: Jan 18 12:00:00 2027 GMT)
WARN  api.example.com 剩余 9 天 (到期: Nov 17 12:00:00 2026 GMT)
```

有任何 WARN 或 ERROR 行时退出码为 1，可直接接入定时巡检/告警。

## 测试说明

1. 正向：对一个有效期充足的真实域名（如本组织站点）运行，预期全部 OK、退出码 0。
2. 反向：把 `--warn-days` 调到大于证书剩余天数（如 `--warn-days 400`），预期出现 WARN、退出码 1。
3. 不可达域名（如 `--domains nonexistent.invalid`）应输出 ERROR 行且退出码 1。
