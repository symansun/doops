package server

// Tool handlers that run or report commands inside the target: a shell
// command, an SSH command, a background command and its task status.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"
	"unicode/utf8"

	"github.com/user/doops/agent/api"
)

func (gw *Gateway) handleToolShell(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, sessionID, pushProgress, writePolicyDenial := call.ctx, call.reqID, call.args, call.writeJSON, call.sessionID, call.pushProgress, call.writePolicyDenial
	var args api.ShellParams
	json.Unmarshal(argBytes, &args)

	log.Printf("🖥️  WS Exec: [%s]", sessionID)

	// 放弃过度设计的 PTY Sentinel 模式，换用标准 os/exec Streaming
	execCtx, cancelExec, timeout := toolDeadline(ctx, call.gatewayTimeoutMS)
	defer releaseToolDeadline(execCtx, cancelExec)
	cmd, stdoutPipe, stderrPipe, err := executeRawCommand(execCtx, args.Command)
	if err != nil {
		if writePolicyDenial(err) {
			return
		}
		resultText := "Error starting command: " + err.Error()
		writeJSON(buildToolErrorResponse(reqID, resultText))
		return
	}

	cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, true)
	if replyToolTimeout(reqID, ctx, execCtx, timeout, cmdErr, writeJSON) {
		return
	}
	if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
		log.Printf("⚠️  WS outcome unknown: [%s] %v", sessionID, cmdErr)
		writeJSON(buildErrorResponse(reqID, -32603, cmdErr.Error()))
		return
	}
	if execCtx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}
	writeSessionCommandAudit(sessionID, args.Command, currentCommandExit(cmdErr))

	if cmdErr != nil {
		finalText := fmt.Sprintf("Command failed with error: %v", cmdErr)
		writeJSON(buildToolErrorResponse(reqID, finalText))
	} else {
		writeJSON(buildStreamedCommandSuccessResponse(reqID))
	}
}

func (gw *Gateway) handleToolSSH(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, sessionID, pushProgress := call.ctx, call.reqID, call.args, call.writeJSON, call.sessionID, call.pushProgress
	var args api.SSHParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid SSH arguments"))
		return
	}
	log.Printf("🔐 WS SSH: [%s] host=%s options=%d remote_args=%d",
		sessionID, args.Host, len(args.Options), len(args.Command))

	execCtx, cancelExec, timeout := toolDeadline(ctx, call.gatewayTimeoutMS)
	defer releaseToolDeadline(execCtx, cancelExec)
	cmd, stdoutPipe, stderrPipe, err := executeStructuredSSHCommand(execCtx, args)
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, err.Error()))
		return
	}

	cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, true)
	if replyToolTimeout(reqID, ctx, execCtx, timeout, cmdErr, writeJSON) {
		return
	}
	if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
		log.Printf("⚠️  WS outcome unknown: [%s] %v", sessionID, cmdErr)
		writeJSON(buildErrorResponse(reqID, -32603, cmdErr.Error()))
		return
	}
	if execCtx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}
	writeSessionCommandAudit(sessionID, sshAuditSummary(args), currentCommandExit(cmdErr))
	if cmdErr != nil {
		if exitCode, ok := sshExitCode(cmdErr); ok {
			writeJSON(buildStructuredToolErrorResponse(
				reqID,
				fmt.Sprintf("SSH command failed: %v", cmdErr),
				map[string]interface{}{"sshExitCode": exitCode},
			))
		} else {
			writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("SSH command failed: %v", cmdErr)))
		}
	} else {
		writeJSON(buildStreamedCommandSuccessResponse(reqID))
	}
}

func (gw *Gateway) handleToolBackground(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, sessionID, pushProgress, writePolicyDenial := call.ctx, call.reqID, call.args, call.writeJSON, call.sessionID, call.pushProgress, call.writePolicyDenial
	var args api.BgExecParams
	json.Unmarshal(argBytes, &args)
	log.Printf("🚀 WS doops_bg: [%s]", sessionID)
	if ctx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}
	task, err := gw.submitBgTask(sessionID, args.Command, args.LogPath)
	if err != nil {
		if writePolicyDenial(err) {
			return
		}
		writeJSON(buildErrorResponse(reqID, -32603, "Task failed: "+err.Error()))
		return
	}
	defer task.cancel()
	stopCancellation := context.AfterFunc(ctx, task.cancel)
	defer stopCancellation()
	pushProgress(rawNotification(fmt.Sprintf("Task submitted.\nTaskID: %s\nPID: %d\nLog: %s\n", task.TaskID, task.PID, task.LogPath)))
	logReader, logErr := os.Open(task.LogPath)
	if logErr != nil {
		task.cancel()
		<-task.done
	} else {
		defer logReader.Close()
		buffer := make([]byte, 32<<10)
		pendingText := make([]byte, 0, 32<<10)
		publishLog := func(data []byte, final bool) {
			pendingText = append(pendingText, data...)
			complete := len(pendingText)
			if !final {
				complete = 0
				for complete < len(pendingText) && utf8.FullRune(pendingText[complete:]) {
					_, size := utf8.DecodeRune(pendingText[complete:])
					complete += size
				}
			}
			if complete != 0 {
				pushProgress(rawNotification(string(pendingText[:complete])))
				pendingText = append(pendingText[:0], pendingText[complete:]...)
			}
		}
		readLog := func() error {
			for {
				n, err := logReader.Read(buffer)
				if n != 0 {
					publishLog(buffer[:n], false)
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
	streamLogs:
		for {
			if logErr = readLog(); logErr != nil {
				task.cancel()
				<-task.done
				break
			}
			select {
			case <-ticker.C:
			case <-task.done:
				logErr = readLog()
				break streamLogs
			case <-ctx.Done():
				task.cancel()
				<-task.done
				logErr = readLog()
				break streamLogs
			}
		}
		publishLog(nil, true)
	}
	if errors.Is(task.err, errExecutionOutcomeUnknown) {
		writeJSON(buildErrorResponse(reqID, -32603, task.err.Error()))
	} else if ctx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
	} else if logErr != nil {
		writeJSON(buildToolErrorResponse(reqID, "background_log_unavailable"))
	} else if task.err != nil {
		writeJSON(buildToolErrorResponse(reqID, fmt.Sprintf("Task failed.\nTaskID: %s\nExitCode: %d", task.TaskID, task.ExitCode)))
	} else {
		writeJSON(buildSuccessResponse(reqID, fmt.Sprintf("Task complete.\nTaskID: %s\nExitCode: %d", task.TaskID, task.ExitCode)))
	}
}

func (gw *Gateway) handleToolTaskStatus(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	var args api.TaskStatusParams
	json.Unmarshal(argBytes, &args)
	info, err := gw.getTaskStatus(args.TaskID, args.Lines)
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
	} else {
		resultText := fmt.Sprintf("TaskID: %s\nPID: %d\nStatus: %s\nExitCode: %d\nLog: %s\n---\n%s",
			info.TaskID, info.PID, info.Status, info.ExitCode, info.LogPath, info.LogTail)
		writeJSON(buildSuccessResponse(reqID, resultText))
	}
}
