package main

import (
	"fmt"
	"regexp"
	"strings"
)

var conversationReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,255}$`)

func appendConversationReferences(instruction string, databaseRefs, repositoryRefs []string) (string, error) {
	databaseRefs, err := validateConversationReferences(databaseRefs, "database")
	if err != nil {
		return "", err
	}
	repositoryRefs, err = validateConversationReferences(repositoryRefs, "repository")
	if err != nil {
		return "", err
	}
	if len(databaseRefs) == 0 && len(repositoryRefs) == 0 {
		return strings.TrimSpace(instruction), nil
	}
	lines := []string{"[DoOps 任务引用]"}
	if len(databaseRefs) > 0 {
		lines = append(lines, "数据库引用："+strings.Join(databaseRefs, ", "))
	}
	if len(repositoryRefs) > 0 {
		lines = append(lines, "代码仓库引用："+strings.Join(repositoryRefs, ", "))
	}
	lines = append(lines, "", "用户任务：", strings.TrimSpace(instruction))
	return strings.Join(lines, "\n"), nil
}

func validateConversationReferences(values []string, kind string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !conversationReferencePattern.MatchString(value) {
			return nil, fmt.Errorf("--%s-ref 只能填写数据库资产 ID、仓库 ID 或 ID@分支，不得包含凭据或路径转义", kind)
		}
		if strings.Contains(value, "://") || strings.ContainsAny(value, "?#\\") {
			return nil, fmt.Errorf("--%s-ref 只能填写 metadata 引用，不接受 URL、查询参数或路径转义", kind)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
