# E2E-only test image for Gateway + Agent login -> exec -> audit acceptance.
# It is separate from the published Agent image and does not include an AI runtime.
FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
WORKDIR /src
COPY backend/agent/ ./backend/agent/
COPY backend/gateway/ ./backend/gateway/
COPY deploy/e2e/seed.go ./deploy/e2e/seed.go
WORKDIR /src/backend/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/doops-gateway . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/doops-e2e-seed /src/deploy/e2e/seed.go
WORKDIR /src/backend/agent
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/doops-agent ./cmd/agent

FROM alpine:3.20
RUN apk add --no-cache bash curl ca-certificates
COPY --from=build /out/doops-gateway /app/doops-gateway
COPY --from=build /out/doops-agent /app/doops-agent
COPY --from=build /out/doops-e2e-seed /app/doops-e2e-seed
