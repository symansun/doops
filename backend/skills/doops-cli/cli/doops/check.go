package main

import (
	"fmt"
	"strings"
)

// CheckDeployment 在目标节点上通过 kubectl 校验 Deployment 主容器镜像是否与期望一致。
func CheckDeployment(client *MCPClient, namespace, deployment, wantImage string) error {
	out, err := client.CallAndCapture("doops_check_deployment", map[string]interface{}{
		"namespace":  namespace,
		"deployment": deployment,
		"image":      wantImage,
	})
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(out))
	return nil
}
