ARG DOOPS_AGENT_BASE_IMAGE=invalid.invalid/doops-agent-base-image-must-be-specified
ARG DO_AGENT_IMAGE=invalid.invalid/doagent-image-must-be-specified
ARG DO_AGENT_VERSION
ARG DO_AGENT_REVISION
ARG DOOPS_RELEASE_TAG=dev
ARG DOOPS_SOURCE_COMMIT=unknown

# Stage 1: Build doops-agent (Go gateway)
FROM golang:1.24-alpine AS builder
ARG DOOPS_RELEASE_TAG
ARG DOOPS_SOURCE_COMMIT
USER root
WORKDIR /app
COPY . .
WORKDIR /app/agent
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/user/doops/agent/pkg/release.Version=${DOOPS_RELEASE_TAG} -X github.com/user/doops/agent/pkg/release.Commit=${DOOPS_SOURCE_COMMIT}" \
    -o /app/doops-agent ./cmd/agent

FROM ${DO_AGENT_IMAGE} AS doagent-runtime

# Stage 2: Lightweight app image.
# The base image owns sandbox/doagent/buildkit/system packages. This layer only
# changes when doops code, skills, docs, or entrypoints change.
FROM ${DOOPS_AGENT_BASE_IMAGE}
ARG DOOPS_AGENT_BASE_IMAGE
ARG DO_AGENT_IMAGE
ARG DO_AGENT_VERSION
ARG DO_AGENT_REVISION
ARG DOOPS_RELEASE_TAG
ARG DOOPS_SOURCE_COMMIT
ARG TARGETARCH
ARG KUBECTL_VERSION=v1.33.1
LABEL org.opencontainers.image.base.name="${DOOPS_AGENT_BASE_IMAGE}"
LABEL org.l8ai.doagent.image="${DO_AGENT_IMAGE}"
LABEL org.l8ai.doagent.version="${DO_AGENT_VERSION}"
LABEL org.l8ai.doagent.revision="${DO_AGENT_REVISION}"

COPY --from=builder /app/doops-agent /app/doops-agent
COPY --from=doagent-runtime /usr/local/bin/do-agent /usr/local/bin/do-agent
COPY --from=builder /app/agent/skills /app/skills
COPY --from=builder /app/agent/agent-entrypoint.sh /app/agent-entrypoint.sh
COPY --from=builder /app/agent/sandbox-entrypoint.sh /app/sandbox-entrypoint.sh
COPY --from=builder /app/agent/configure_doagent_settings.py /app/configure_doagent_settings.py
COPY --from=builder /app/agent/configure_kubeconfig.py /app/configure_kubeconfig.py
COPY --from=builder /app/deploy /app/deploy
COPY --from=builder /app/README.md /app/self-docs/README.md
COPY --from=builder /app/docs /app/self-docs/docs

RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) kubectl_sha256="5de4e9f2266738fd112b721265a0c1cd7f4e5208b670f811861f699474a100a3" ;; \
        arm64) kubectl_sha256="d595d1a26b7444e0beb122e25750ee4524e74414bbde070b672b423139295ce6" ;; \
        *) echo "unsupported kubectl architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl --fail --location --silent --show-error \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
        --output /usr/local/bin/kubectl; \
    echo "${kubectl_sha256}  /usr/local/bin/kubectl" | sha256sum --check --status; \
    chmod 0755 /usr/local/bin/kubectl; \
    chmod +x /app/agent-entrypoint.sh /app/sandbox-entrypoint.sh; \
    /usr/local/bin/kubectl version --client >/dev/null \
    && /app/doops-agent -help >/dev/null \
    && test "$(/app/doops-agent -version)" = "release=${DOOPS_RELEASE_TAG} commit=${DOOPS_SOURCE_COMMIT}" \
    && test "$(/usr/local/bin/do-agent --version)" = "do-agent ${DO_AGENT_VERSION}" \
    && /usr/local/bin/do-agent --help >/dev/null \
    && buildctl --version \
    && python3 -c 'import yaml' \
    && node --check /app/skills/doweb/scripts/doweb.mjs \
    && helm version --short \
    && ssh -V \
    && ! command -v sshd >/dev/null 2>&1

ENTRYPOINT ["/app/sandbox-entrypoint.sh"]
