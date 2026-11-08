# 平滑发布/灰度（blue-green-release）

双容器 blue-green + nginx upstream 切换的零停机发布参考实现，通过 doops 在目标节点驱动全流程。

```
用户 -> nginx(:80) -> upstream myapp { blue(:8081) | green(:8082) }
                          ^
                    发布 = 改写这个 upstream 并 reload
```

## 发布流程

1. 读状态文件 `/var/lib/bluegreen/<app>` 确认活跃槽位，新版本部署到 standby 槽位；
2. standby 槽位本地健康检查（不经过 LB，未接流量）——失败即中止，线上无感；
3. 可选灰度：`--canary 10` 让新槽位按权重接 10% 流量，观察 `--canary-seconds`；
4. 全量切换：upstream 只指向新槽位，`nginx -t && nginx -s reload`（平滑，不断连接）；
5. 经 LB 端口二次健康检查，写状态文件；**旧槽位容器保留**，回滚就是切回 upstream：

```bash
bash run.sh --target <target> --app <app> --rollback
```

## 前置条件

- 本机已 doops login 到 `--target`；生产 target 建议配置 gateway 审批管控
  （`-approval-targets "prod/*"`），本脚本的变更类 exec 会自动触发审批闸——
  按提示 `doops admin approve` 后加 `--approval` 重放；也可把本脚本作为
  [release-flow](../release-flow/) 示例的 deploy 命令嵌入规范流程；
- 目标节点已装 docker 与 nginx；
- 一次性初始化（每个 app 一次）：

```bash
# 1) 启动两个槽位容器（初始版本 v0.0.1 示例）
docker run -d --name myapp-blue  -p 127.0.0.1:8081:8080 registry.example.com/myapp:v0.0.1
docker run -d --name myapp-green -p 127.0.0.1:8082:8080 registry.example.com/myapp:v0.0.1

# 2) nginx upstream 初始指向 blue
cat > /etc/nginx/conf.d/myapp-upstream.conf <<'EOF'
upstream myapp { server 127.0.0.1:8081; }
EOF

# 3) server 块代理到 upstream（按实际域名/端口调整）
cat > /etc/nginx/conf.d/myapp.conf <<'EOF'
server {
    listen 80;
    location / { proxy_pass http://myapp; }
}
EOF
nginx -t && nginx -s reload

# 4) 状态文件（blue 为初始活跃槽位）
mkdir -p /var/lib/bluegreen && echo blue > /var/lib/bluegreen/myapp
```

## 用法

```bash
# 直接全量切换
bash examples/skills/blue-green-release/run.sh \
  --target prod --image registry.example.com/myapp --version v1.4.2

# 先灰度 10% 观察 60s 再全量
bash examples/skills/blue-green-release/run.sh \
  --target prod --image registry.example.com/myapp --version v1.4.2 \
  --canary 10 --canary-seconds 60

# 秒级回滚
bash examples/skills/blue-green-release/run.sh --target prod --rollback
```

默认参数：`--app myapp`、`--blue-port 8081`、`--green-port 8082`、`--container-port 8080`（容器内服务端口）、`--lb-port 80`、`--health-path /health`，均可覆盖。

任一环节失败脚本以非零退出：standby 健康检查失败时线上无影响；切换后 LB 检查失败时立即执行 `--rollback` 即可恢复。

## 故障处理

| 现象 | 原因与处置 |
| --- | --- |
| `[1/5]` 拉取镜像失败 | 脚本立即中止，线上无影响。检查镜像名/tag 是否存在、目标节点到 registry 的连通性与凭据 |
| `[2/5]` standby 健康检查超时 | 新容器未正常提供 `/health`。`docker logs <app>-<slot>` 排查；确认 `--container-port` 与镜像内服务端口一致 |
| `nginx -t` 报错 | upstream 配置语法错误，reload 不会执行，线上无影响。检查 `cat /etc/nginx/conf.d/<app>-upstream.conf` |
| 灰度期指标异常 | Ctrl-C 中止后执行 `--rollback` 切回旧槽位（旧容器全程保留） |
| 状态文件丢失/损坏 | 默认回落 blue 为活跃槽位；与实际不符时手工修正：`echo green > /var/lib/bluegreen/<app>` |
| `--canary 100` 被拒绝 | 100% 灰度与直接全量等价，省略 `--canary` 即可 |

## 测试说明

1. 链路自检：先跑 `bash deploy/e2e/smoke.sh --mode process` 确认 doops 链路可用。
2. 本地演练（需 docker + nginx）：按上文「一次性初始化」准备环境，可用任意带
   `/health` 的测试镜像（或 `nginx:alpine` 替代，`--health-path /`）。
3. 发布新版本：

   ```bash
   bash examples/skills/blue-green-release/run.sh \
     --target staging --image nginx --version 1.27-alpine --health-path /
   ```

   预期输出 5 段步骤并以 `✅ 发布完成` 结束；`docker ps` 可见两个槽位容器，
   `cat /etc/nginx/conf.d/myapp-upstream.conf` 指向下一个槽位。
4. 回滚用例：发布后执行 `--rollback`，upstream 应切回原槽位。
5. 灰度用例：`--canary 20 --canary-seconds 5`，观察灰度期 upstream 文件含两条
   带 weight 的 server 行，全量后只剩新槽位。
6. 审批联动：target 命中 gateway `-approval-targets` 时，首次 exec 被拦截并生成
   `apr_...` 审批单，approve 后重放（参见 release-flow 示例的自动轮询写法）。
