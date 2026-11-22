package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/user/doops/agent/api"
	"path/filepath"
	"strings"
)

const (
	maxAgentPromptAttachments     = 4
	maxAgentPromptAttachmentBytes = 25 * 1024 * 1024
)

func validateAgentPromptAttachments(attachments []api.AgentPromptAttachment) error {
	if len(attachments) > maxAgentPromptAttachments {
		return fmt.Errorf("一次最多上传 %d 个附件", maxAgentPromptAttachments)
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Filename) == "" || strings.ContainsAny(attachment.Filename, `/\\`) || strings.ContainsAny(attachment.Filename, "\x00\r\n") {
			return fmt.Errorf("附件文件名无效")
		}
		if strings.TrimSpace(attachment.MediaType) == "" || strings.ContainsAny(attachment.MediaType, "\x00\r\n") {
			return fmt.Errorf("附件媒体类型无效")
		}
		decoded, err := base64.StdEncoding.DecodeString(attachment.Base64Data)
		if err != nil {
			return fmt.Errorf("附件编码无效")
		}
		if len(decoded) == 0 {
			return fmt.Errorf("附件不能为空")
		}
		if len(decoded) > maxAgentPromptAttachmentBytes {
			return fmt.Errorf("附件超过 25 MiB 限制")
		}
	}
	return nil
}

func (gw *Gateway) uploadAgentPromptAttachments(ctx context.Context, doagentURL, doopsSessionID string, attachments []api.AgentPromptAttachment) ([]map[string]interface{}, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	if filepath.Base(doopsSessionID) != doopsSessionID || strings.Contains(doopsSessionID, "..") {
		return nil, fmt.Errorf("invalid agent session id")
	}
	workspaceResp, err := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "workspace-" + doopsSessionID,
		"method":  "workspace/open",
		"params":  map[string]interface{}{"path": filepath.Join("/root/ws", doopsSessionID)},
	})
	if err != nil {
		return nil, fmt.Errorf("open doagent workspace: %w", err)
	}
	workspaceResult, ok := workspaceResp["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("doagent workspace response is invalid")
	}
	workspaceID, _ := workspaceResult["id"].(string)
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("doagent workspace id is missing")
	}

	blocks := make([]map[string]interface{}, 0, len(attachments))
	for index, attachment := range attachments {
		response, uploadErr := doagentRPCContext(ctx, doagentURL, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      fmt.Sprintf("attachment-%s-%d", doopsSessionID, index),
			"method":  "attachment/upload",
			"params": map[string]interface{}{
				"workspaceId": workspaceID,
				"filename":    attachment.Filename,
				"mediaType":   attachment.MediaType,
				"base64Data":  attachment.Base64Data,
			},
		})
		if uploadErr != nil {
			return nil, fmt.Errorf("upload attachment %q: %w", attachment.Filename, uploadErr)
		}
		result, ok := response["result"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("attachment upload response is invalid")
		}
		attachmentID, _ := result["attachment_id"].(string)
		if strings.TrimSpace(attachmentID) == "" {
			return nil, fmt.Errorf("attachment upload id is missing")
		}
		blocks = append(blocks, map[string]interface{}{
			"type":          "attachment",
			"attachment_id": attachmentID,
			"workspaceId":   workspaceID,
		})
	}
	return blocks, nil
}
