# Oilan DoOps Agent Release

The Oilan release has one build declaration, `deploy/deploy.yaml`, using
`doops.sh/v3 BuildDefinition`. The deployment target, image digest, reason,
and acceptance checks are supplied by the current `$doops-cicd` task.

## Artifact And Credentials

CNB CI builds the DoOps image with the full source commit as its immutable tag.
The environment service then pins the registry digest used by Helm and the
runtime Deployment.

Agent reverse registration declares only the configured cluster and instance;
it does not mount, read, or send a registration token. Gateway user tokens
remain the authorization boundary for operator actions.

`docker.cnb.cool/l8ai/ai/doops.sh` is public, so Oilan does not require a
runtime registry credential. User credential management remains a separate
Gateway API and is not a release protocol.

## Runtime Contract

The image contains the pinned doagent release. Agent registration declares
only cluster and instance. Formal deployment is executed by a controller
outside the Agent workload; direct Agent self-replacement is rejected.

## Execution

```bash
doops -session doops-oilan-release ask \
  --allow-mutate \
  --target gw-oilan-node \
  --msg '$doops-cicd 部署服务 doops-agent；镜像=<digest>; 原因=<reason>; 验收=<checks>; 已授权执行变更'
```

The task must name one service and one target. The Agent uses only runtime
tools available on that target and must return real health evidence.

The Helm release is `doops-agent-live` in namespace `kz-ops`. The Deployment
uses rolling update with zero unavailable replicas and becomes ready only
after its Gateway registration is healthy. Model provider configuration remains
in the `doagent-model-settings/settings.json` Secret and is not duplicated in
the build declaration.
