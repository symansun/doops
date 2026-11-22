package server

type GatewayAction string

const (
	ActionAll                GatewayAction = "*"
	ActionTargetUse          GatewayAction = "target:use"
	ActionExec               GatewayAction = "exec"
	ActionAsk                GatewayAction = "ask"
	ActionRead               GatewayAction = "read"
	ActionWrite              GatewayAction = "write"
	ActionPush               GatewayAction = "push"
	ActionPull               GatewayAction = "pull"
	ActionInfo               GatewayAction = "info"
	ActionCheck              GatewayAction = "check"
	ActionClean              GatewayAction = "clean"
	ActionProxy              GatewayAction = "proxy"
	ActionAgentUpgrade       GatewayAction = "agent:upgrade"
	ActionTargetsList        GatewayAction = "targets:list"
	ActionCredentialCreate   GatewayAction = "credential:create"
	ActionCredentialMetadata GatewayAction = "credential:metadata"
	ActionCredentialGrant    GatewayAction = "credential:grant"
	ActionCredentialUse      GatewayAction = "credential:use"
	ActionCredentialVerify   GatewayAction = "credential:verify"
	ActionCredentialRotate   GatewayAction = "credential:rotate"
	ActionCredentialRevoke   GatewayAction = "credential:revoke"
	ActionCredentialAudit    GatewayAction = "credential:audit"
	ActionAdmin              GatewayAction = "admin"
	ActionEdgeAdmin          GatewayAction = "edge:admin"
	ActionEdgeView           GatewayAction = "edge:view"
)

var defaultGatewayUserActions = []GatewayAction{
	ActionTargetUse,
}
