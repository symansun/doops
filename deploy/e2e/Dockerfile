# E2E 冒烟专用镜像：仅包含 doops-gateway 与 doops-agent 两个二进制。
# 与生产镜像（agent/Dockerfile，依赖 doops-base 沙箱底座）不同，本镜像追求最小可跑拓扑：
# gateway 使用内嵌 SQLite，agent 的 doops_shell 只需 bash，因此 alpine + bash 即可跑通
# login -> exec -> 审计落盘 的完整链路。
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY agent/ ./agent/
WORKDIR /src/agent
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/doops-gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/doops-agent ./cmd/agent

FROM alpine:3.20
RUN apk add --no-cache bash curl ca-certificates
COPY --from=build /out/doops-gateway /app/doops-gateway
COPY --from=build /out/doops-agent /app/doops-agent
