package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

const testNL = "\n"

// terminalTestDrain 持续从游标读取，直到输出包含 need 或超时。
func terminalTestDrain(t *testing.T, gw *Gateway, sessionID, termID string, cursor *int64, need string) string {
	t.Helper()
	var collected strings.Builder
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, next, _, alive, _, err := gw.terminals.read(sessionID, termID, *cursor)
		if err != nil {
			t.Fatalf("read terminal: %v", err)
		}
		*cursor = next
		collected.Write(data)
		if need == "" || strings.Contains(collected.String(), need) {
			return collected.String()
		}
		if !alive {
			time.Sleep(50 * time.Millisecond)
			if data, next, _, _, _, err := gw.terminals.read(sessionID, termID, *cursor); err == nil {
				*cursor = next
				collected.Write(data)
			}
			return collected.String()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q, got %q", need, collected.String())
	return ""
}

func TestTerminalSessionKeepsCwdAndEnv(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("session-a", 120, 32)
	if err != nil {
		t.Fatalf("open terminal: %v", err)
	}
	defer gw.terminals.close("session-a", session.id)

	// 探针用 printf 自带前缀与换行：tty 自己的换行在 CI runner 上并不可靠
	// （见 terminalPTYDevice 的注释），断言不该依赖邻居字节。
	script := "cd /tmp && export DOOPS_TERM_MARK=interactive-ok && echo PWD:$(pwd) && echo MARK:$DOOPS_TERM_MARK && printf 'PTY=%s\\n' \"$(tty)\" && echo TTY_READY_$((40+2)) && sleep 30" + testNL
	// 探针脚本里的标签必须与解析器用的那个常量同源，否则改名后两边各走一边：
	// 脚本打印新标签、解析器还在等旧标签，断言就会以"没有 pty"的形式假红。
	if !strings.Contains(script, "printf '"+terminalPTYDeviceProbe+"=%s") {
		t.Fatalf("probe script %q does not print the label %q the parser looks for", script, terminalPTYDeviceProbe)
	}
	if err := gw.terminals.write("session-a", session.id, []byte(script)); err != nil {
		t.Fatalf("write: %v", err)
	}

	var cursor int64
	// 等到 tty 之后那条回声都出现，而不是 MARK 一出现就收手：CI 上就是因为
	// 提前返回，缓冲里还没有 tty 的输出行。
	out := terminalTestDrain(t, gw, "session-a", session.id, &cursor, "TTY_READY_42")
	if !terminalOutputHasLine(out, "PWD:/tmp") {
		t.Fatalf("expected pwd to report /tmp in the same shell, got %q", out)
	}
	if !terminalOutputHasLine(out, "MARK:interactive-ok") {
		t.Fatalf("expected the exported marker to survive into the shell, got %q", out)
	}
	if terminalOutputHasLine(out, "not a tty") {
		t.Fatalf("terminal is not a pty: %q", out)
	}
	if dev := terminalPTYDevice(out); dev == "" {
		t.Fatalf("expected the tty probe to name a pty device, got %q", out)
	} else if !strings.HasPrefix(dev, "/dev/") {
		t.Fatalf("probe reported %q, want a /dev/ device", dev)
	}

	// Ctrl-C 走原始字节输入，与键盘一致：中断 sleep，但 shell 仍然活着。
	if err := gw.terminals.write("session-a", session.id, []byte{3}); err != nil {
		t.Fatalf("write ctrl-c: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_, next, _, live, _, err := gw.terminals.read("session-a", session.id, cursor)
	if err != nil {
		t.Fatalf("read after ctrl-c: %v", err)
	}
	cursor = next
	if !live {
		t.Fatalf("ctrl-c should interrupt sleep, not kill the shell")
	}
}

// TestTerminalSessionCtrlCInterruptsWhenAgentInheritsSigIgnore 复现 Agent 被
// systemd/Kubernetes/包装脚本以"忽略 SIGINT"的方式拉起时的继承关系：exec 出来的
// shell 会继承 SIG_IGN，Ctrl-C 只剩一个 ^C 回显，sleep 不中断、提示符不返回，
// 控制台上看起来就是"Ctrl-C 没反应"。终端必须在启动 shell 前恢复默认处置。
func TestTerminalSessionCtrlCInterruptsWhenAgentInheritsSigIgnore(t *testing.T) {
	signal.Ignore(os.Interrupt)
	t.Cleanup(func() { signal.Reset(os.Interrupt) })

	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("session-sigint", 100, 30)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = gw.terminals.close("session-sigint", session.id) }()

	var cursor int64
	if err := gw.terminals.write("session-sigint", session.id, []byte("echo READY_$((1+1)) && sleep 30"+testNL)); err != nil {
		t.Fatalf("write: %v", err)
	}
	terminalTestDrain(t, gw, "session-sigint", session.id, &cursor, "READY_2")

	if err := gw.terminals.write("session-sigint", session.id, []byte{3}); err != nil {
		t.Fatalf("write ctrl-c: %v", err)
	}
	// 只有 sleep 真被 SIGINT 打断，这条命令才会在 15 秒内执行并打印结果；
	// 若 shell 仍忽略 SIGINT，它会一直排在 sleep 后面，drain 超时即失败。
	if err := gw.terminals.write("session-sigint", session.id, []byte("echo AFTER_$((20+22))"+testNL)); err != nil {
		t.Fatalf("write after ctrl-c: %v", err)
	}
	out := terminalTestDrain(t, gw, "session-sigint", session.id, &cursor, "AFTER_42")
	if !strings.Contains(out, "AFTER_42") {
		t.Fatalf("ctrl-c did not interrupt the foreground job: %q", out)
	}
}

func TestTerminalSessionsAreIsolated(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("session-owner", 80, 24)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer gw.terminals.close("session-owner", session.id)

	if _, _, _, _, _, err := gw.terminals.read("session-intruder", session.id, 0); err != errTerminalForbidden {
		t.Fatalf("expected forbidden for cross-session read, got %v", err)
	}
	if err := gw.terminals.write("session-intruder", session.id, []byte("id"+testNL)); err != errTerminalForbidden {
		t.Fatalf("expected forbidden for cross-session input, got %v", err)
	}
	if err := gw.terminals.resize("session-intruder", session.id, 100, 30); err != errTerminalForbidden {
		t.Fatalf("expected forbidden for cross-session resize, got %v", err)
	}
	if err := gw.terminals.close("session-intruder", session.id); err != errTerminalForbidden {
		t.Fatalf("expected forbidden for cross-session close, got %v", err)
	}

	// 越权调用之后属主终端必须照常可用
	if err := gw.terminals.write("session-owner", session.id, []byte("echo OWNER_OK"+testNL)); err != nil {
		t.Fatalf("owner write: %v", err)
	}
	var cursor int64
	if out := terminalTestDrain(t, gw, "session-owner", session.id, &cursor, "OWNER_OK"); !strings.Contains(out, "OWNER_OK") {
		t.Fatalf("owner terminal did not echo marker: %q", out)
	}
}

func TestTerminalReadReportsExitAndCursor(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("session-exit", 100, 30)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = gw.terminals.close("session-exit", session.id) }()

	if err := gw.terminals.write("session-exit", session.id, []byte("echo BYE && exit 7"+testNL)); err != nil {
		t.Fatalf("write: %v", err)
	}

	var cursor int64
	if out := terminalTestDrain(t, gw, "session-exit", session.id, &cursor, "BYE"); !strings.Contains(out, "BYE") {
		t.Fatalf("expected BYE marker, got %q", out)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		_, next, _, live, code, err := gw.terminals.read("session-exit", session.id, cursor)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		cursor = next
		if !live {
			if code != 7 {
				t.Fatalf("expected exit code 7, got %d", code)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal did not report exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTerminalTruncationAdvancesCursor(t *testing.T) {
	session := &terminalSession{id: "term_test", sessionID: "s", lastUsed: time.Now()}
	session.appendLocked([]byte("0123456789"))
	if _, next, truncated, _, _ := session.readFrom(0); truncated || next != 10 {
		t.Fatalf("unexpected fresh read: next=%d truncated=%v", next, truncated)
	}
	session.appendLocked(make([]byte, terminalBufferLimit))
	data, next, truncated, _, _ := session.readFrom(0)
	if !truncated {
		t.Fatalf("expected truncated read after buffer overflow")
	}
	if int64(len(data)) != next-session.base {
		t.Fatalf("cursor and payload disagree: len=%d next=%d base=%d", len(data), next, session.base)
	}
	session.mu.Lock()
	session.closed = true
	session.mu.Unlock()
}

func TestTerminalManagerReapsIdleAndExited(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	idle, err := gw.terminals.open("session-reap", 80, 24)
	if err != nil {
		t.Fatalf("open idle: %v", err)
	}
	idle.mu.Lock()
	idle.lastUsed = time.Now().Add(-2 * terminalIdleLimit)
	idle.mu.Unlock()

	exited, err := gw.terminals.open("session-reap", 80, 24)
	if err != nil {
		t.Fatalf("open exited: %v", err)
	}
	exited.mu.Lock()
	exited.exited = true
	exited.mu.Unlock()

	if closed := gw.terminals.reap(time.Now()); closed != 2 {
		t.Fatalf("expected 2 terminals reaped, got %d", closed)
	}
	if _, _, _, _, _, err := gw.terminals.read("session-reap", idle.id, 0); err != errTerminalNotFound {
		t.Fatalf("expected idle terminal removed, got %v", err)
	}
}

func TestTerminalManagerEnforcesPerSessionLimit(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	for i := 0; i < terminalMaxPerSess; i++ {
		if _, err := gw.terminals.open("session-limit", 80, 24); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}
	if _, err := gw.terminals.open("session-limit", 80, 24); err != errTerminalLimit {
		t.Fatalf("expected limit error, got %v", err)
	}
	if _, err := gw.terminals.open("session-other", 80, 24); err != nil {
		t.Fatalf("other session should still open: %v", err)
	}
}

func TestTerminalToolActionAndSummary(t *testing.T) {
	cases := map[string]GatewayAction{
		"open":   ActionExec,
		"input":  ActionExec,
		"read":   ActionInfo,
		"resize": ActionInfo,
		"close":  ActionInfo,
	}
	for op, want := range cases {
		args := json.RawMessage([]byte("{\"op\":\"" + op + "\"}"))
		if got := terminalToolAction(args); got != want {
			t.Fatalf("action for %q = %q, want %q", op, got, want)
		}
	}
	summary := terminalToolSummary(map[string]interface{}{"op": "open", "term": "term_abc"}, nil)
	if !strings.Contains(summary, "op=open") || !strings.Contains(summary, "term_abc") {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func terminalTestCall(t *testing.T, gw *Gateway, sessionID, args string) api.JSONRPCResponse {
	t.Helper()
	var captured *api.JSONRPCResponse
	gw.handleToolTerminal(toolInvocation{
		reqID: 1,
		args:  json.RawMessage([]byte(args)),
		writeJSON: func(v interface{}) {
			resp := v.(api.JSONRPCResponse)
			captured = &resp
		},
		sessionID: sessionID,
	})
	if captured == nil {
		t.Fatalf("handler wrote no response for %s", args)
	}
	return *captured
}

func terminalTestPayload(t *testing.T, resp api.JSONRPCResponse) map[string]interface{} {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	content, _ := result["content"].([]map[string]interface{})
	if len(content) == 0 {
		t.Fatalf("missing content: %+v", result)
	}
	text, _ := content[0]["text"].(string)
	payload := map[string]interface{}{}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode payload %q: %v", text, err)
	}
	return payload
}

func TestHandleToolTerminalRoundTrip(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	opened := terminalTestPayload(t, terminalTestCall(t, gw, "session-rpc",
		"{\"session_id\":\"session-rpc\",\"op\":\"open\",\"cols\":110,\"rows\":30}"))
	term, _ := opened["term"].(string)
	if term == "" {
		t.Fatalf("open did not return a terminal id: %+v", opened)
	}
	defer terminalTestCall(t, gw, "session-rpc", "{\"session_id\":\"session-rpc\",\"op\":\"close\",\"term\":\""+term+"\"}")

	input := base64.StdEncoding.EncodeToString([]byte("echo RPC_MARK_OK" + testNL))
	terminalTestCall(t, gw, "session-rpc", "{\"session_id\":\"session-rpc\",\"op\":\"input\",\"term\":\""+term+"\",\"data\":\""+input+"\"}")

	cursor := int64(0)
	var collected strings.Builder
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		payload := terminalTestPayload(t, terminalTestCall(t, gw, "session-rpc",
			"{\"session_id\":\"session-rpc\",\"op\":\"read\",\"term\":\""+term+"\",\"cursor\":"+strconv.FormatInt(cursor, 10)+"}"))
		encoded, _ := payload["data"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode read data: %v", err)
		}
		collected.Write(decoded)
		if next, ok := payload["cursor"].(float64); ok {
			cursor = int64(next)
		}
		if strings.Contains(collected.String(), "RPC_MARK_OK") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(collected.String(), "RPC_MARK_OK") {
		t.Fatalf("terminal input did not reach the shell: %q", collected.String())
	}

	denied := terminalTestCall(t, gw, "session-other",
		"{\"session_id\":\"session-other\",\"op\":\"read\",\"term\":\""+term+"\"}")
	if denied.Error == nil {
		t.Fatalf("expected cross-session denial, got %+v", denied)
	}
}

// terminalOutputHasLine 判断去掉 ANSI 噪声后是否存在整行等于 want 的行。
// 用整行比较而不是 strings.Contains：PTY 回显的命令行本身就包含 "MARK:$DOOPS_TERM_MARK"
// 这类文本，Contains 会把"命令被回显"误当成"命令执行结果"。
func terminalOutputHasLine(output, want string) bool {
	for _, line := range strings.Split(stripTerminalANSI(output), "\n") {
		if strings.TrimRight(strings.TrimSpace(line), "\r") == want {
			return true
		}
	}
	return false
}

// ciTerminalProbeTranscript 复刻 run 45416198758 上真实的回卷：OSC 3008 序列 +
// 私有模式 CSI + 提示符 + 被回显的整条命令，然后是各条命令自己的输出。关键细节是
// tty 的设备名后面**没有换行**，直接和紧随其后的 echo 粘成
// "PTY=/dev/pts/0TTY_READY_42"：INT-99 的"整行就是设备名"解析在这段文本上必然判空，
// 这就是同一个用例在 CI 上继续红的原因。
const ciTerminalProbeTranscript = "\x1b]3008;start=9e081817-6acc-4652-b42d-b2e9b953c0af;type=shell;user=runner;hostname=runnervm0hi8g\x1b\\\x1b[?2004h" +
	"runner@runnervm0hi8g:~/work/doops/doops.sh$ cd /tmp && printf 'PTY=%s\\n' \"$(tty)\" && echo TTY_READY_$((40+2)) && sleep 30\r\n" +
	"PWD:/tmp\r\n" +
	"MARK:interactive-ok\r\n" +
	"\x1b]3008;start=11042f60-c658-48aa-a223-6ecebf5e12af;type=command;exit=0\x1b\\" +
	"PTY=/dev/pts/0TTY_READY_42\r\n"

// ciTerminalProbeTranscriptEchoOnly 只有回显、没有任何输出：探针前缀在回显里是字面量
// PTY=%s，所以这里必须判不存在，否则断言等于没写。
const ciTerminalProbeTranscriptEchoOnly = "\x1b]3008;start=9e081817-6acc-4652-b42d-b2e9b953c0af;type=shell;cwd=/\x1b\\\x1b[?2004h" +
	"runner@runnervm0hi8g:~/work/doops/doops.sh$ cd /tmp && printf 'PTY=%s\\n' \"$(tty)\" && sleep 30\r\n"

// TestTerminalPTYDeviceFromShellIntegrationTranscript 证明四件事：
//  1. 整行匹配（INT-99 的实现）在真实 CI 回卷上判空——设备名和后面的 echo 粘在同一行；
//  2. 探针解析从同一段文本里取出 /dev/pts/0，不依赖后面有没有换行；
//  3. 只有回显、没有执行时必须判空；
//  4. 回显里出现 /dev/ 路径时也不能算 pty（旧的 strings.Contains 会在这里误判）。
func TestTerminalPTYDeviceFromShellIntegrationTranscript(t *testing.T) {
	wholeLine := ""
	for _, line := range strings.Split(stripTerminalANSI(ciTerminalProbeTranscript), "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), "\r")
		if terminalPTYDeviceLine.MatchString(line) {
			wholeLine = line
		}
	}
	if wholeLine != "" {
		t.Fatalf("fixture no longer reproduces the CI failure: whole-line matching found %q", wholeLine)
	}
	t.Logf("CI 真实回卷: 整行匹配=%q（INT-99 的实现在此判红）", wholeLine)

	if got := terminalPTYDevice(ciTerminalProbeTranscript); got != "/dev/pts/0" {
		t.Fatalf("device from the shell-integration transcript = %q, want /dev/pts/0", got)
	}
	if got := terminalPTYDevice(ciTerminalProbeTranscriptEchoOnly); got != "" {
		t.Fatalf("an echoed probe with no output must not count as a pty, got %q", got)
	}
	if got := terminalPTYDevice(ciTerminalProbeTranscriptEchoOnly + "$ cat /dev/pts/9\r\n"); got != "" {
		t.Fatalf("a /dev/ path inside an echoed command was taken for a pty device: %q", got)
	}
}

// TestTerminalPTYDeviceRejectsEchoedPath 证明断言没有被削弱成"随便哪出现 /dev/ 就
// 算过"：回显的命令里带 /dev/pts/9、tty 却明确报 "not a tty" 时，必须判不存在。
func TestTerminalPTYDeviceRejectsEchoedPath(t *testing.T) {
	transcript := "$ cat /dev/pts/9 && printf 'PTY=%s\\n' \"$(tty)\"\r\nnot a tty\r\n"
	if !strings.Contains(transcript, "/dev/") {
		t.Fatalf("fixture must contain /dev/ so the weak check would pass")
	}
	if got := terminalPTYDevice(transcript); got != "" {
		t.Fatalf("echoed path was taken for a pty device: %q", got)
	}
}

// TestTerminalPTYDeviceStripsOSCOnDeviceLine OSC 序列直接贴在探针输出前面时也要求能
// 解析出来；去掉 OSC 剥离会退化成 ""。
func TestTerminalPTYDeviceStripsOSCOnDeviceLine(t *testing.T) {
	transcript := "\x1b]3008;start=752439c7;type=shell\x1b\\PTY=/dev/pts/7\r\n"
	if got := terminalPTYDevice(transcript); got != "/dev/pts/7" {
		t.Fatalf("OSC-prefixed probe line = %q, want /dev/pts/7", got)
	}
}

// TestTerminalPTYDeviceCleanAndNonDeviceLines 覆盖探针形态、整行兜底形态与必须拒绝的近邻形态。
func TestTerminalPTYDeviceCleanAndNonDeviceLines(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"probe glued to next output", "PTY=/dev/pts/0TTY_READY_42\r\n", "/dev/pts/0"},
		{"probe plain", "PTY=/dev/pts/3\r\n", "/dev/pts/3"},
		{"probe tty", "PTY=/dev/tty1\r\n", "/dev/tty1"},
		{"probe not a tty", "PTY=not a tty\r\n", ""},
		{"probe echo only", "printf 'PTY=%s\\n' \"$(tty)\"\r\n", ""},
		{"probe last wins", "PTY=/dev/pts/2\r\nPTY=/dev/pts/5\r\n", "/dev/pts/5"},
		{"whole line fallback", "/dev/pts/4\r\n", "/dev/pts/4"},
		{"not a tty", "not a tty\n", ""},
		{"pty with trailing text", "/dev/pts/3 (deleted)\n", ""},
		{"device inside a word", "x/dev/pts/3\n", ""},
		{"echoed command line", "cd /tmp && tty && echo /dev/pts/3\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := terminalPTYDevice(tc.input); got != tc.want {
			t.Fatalf("%s: terminalPTYDevice(%q) = %q, want %q", tc.name, tc.input, got, tc.want)
		}
	}
}

func TestTerminalReadWaitClampsToMax(t *testing.T) {
	if got := clampTerminalReadWait(0); got != 0 {
		t.Fatalf("clamp(0) = %v, want 0", got)
	}
	if got := clampTerminalReadWait(-50); got != 0 {
		t.Fatalf("clamp(-50) = %v, want 0", got)
	}
	if got := clampTerminalReadWait(2500); got != 2500*time.Millisecond {
		t.Fatalf("clamp(2500) = %v, want 2.5s", got)
	}
	if got := clampTerminalReadWait(60000); got != terminalReadWaitMax {
		t.Fatalf("clamp(60000) = %v, want %v", got, terminalReadWaitMax)
	}
}

func TestTerminalReadWaitBlocksUntilTimeoutAndWakesOnOutput(t *testing.T) {
	session := &terminalSession{
		id:        "term_lp",
		sessionID: "s",
		notify:    make(chan struct{}, 1),
		lastUsed:  time.Now(),
	}

	// 1. 无新数据时阻塞到 wait 超时。
	start := time.Now()
	data, next, truncated, alive, _ := session.readWait(context.Background(), 0, 100*time.Millisecond)
	elapsed := time.Since(start)
	if len(data) != 0 || next != 0 || truncated || !alive {
		t.Fatalf("idle readWait = (%q, %d, %v, %v), want empty alive", data, next, truncated, alive)
	}
	if elapsed < 80*time.Millisecond {
		t.Fatalf("idle readWait returned too early: %v", elapsed)
	}

	// 2. 有新输出时立刻唤醒，远早于 3s 上限。
	go func() {
		time.Sleep(35 * time.Millisecond)
		session.mu.Lock()
		session.appendLocked([]byte("hello-lp"))
		session.mu.Unlock()
	}()
	start = time.Now()
	data, next, _, alive, _ = session.readWait(context.Background(), 0, 3*time.Second)
	elapsed = time.Since(start)
	if string(data) != "hello-lp" || next != 8 || !alive {
		t.Fatalf("output readWait = (%q, %d, %v), want hello-lp/8/true", data, next, alive)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("output readWait did not wake promptly: %v", elapsed)
	}

	// 3. 进程退出时立刻唤醒并汇报 alive=false 与 exitCode。
	go func() {
		time.Sleep(35 * time.Millisecond)
		session.mu.Lock()
		session.exited = true
		session.exitCode = 5
		session.signalLocked()
		session.mu.Unlock()
	}()
	start = time.Now()
	_, _, _, alive, code := session.readWait(context.Background(), next, 3*time.Second)
	elapsed = time.Since(start)
	if alive || code != 5 {
		t.Fatalf("exit readWait = (alive=%v, code=%d), want false/5", alive, code)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("exit readWait did not wake promptly: %v", elapsed)
	}
}

func TestTerminalToolOpenAndReadAdvertiseLongPollFeature(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	invoke := func(payload map[string]interface{}) map[string]interface{} {
		rawArgs, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		var resp api.JSONRPCResponse
		gw.handleToolTerminal(toolInvocation{
			ctx:       context.Background(),
			reqID:     1,
			sessionID: "sess-lp",
			args:      rawArgs,
			writeJSON: func(v interface{}) {
				b, _ := json.Marshal(v)
				_ = json.Unmarshal(b, &resp)
			},
		})
		if resp.Error != nil {
			t.Fatalf("tool error: %+v", resp.Error)
		}
		resMap, _ := resp.Result.(map[string]interface{})
		content, _ := resMap["content"].([]interface{})
		if len(content) == 0 {
			t.Fatalf("empty tool content: %+v", resp.Result)
		}
		text, _ := content[0].(map[string]interface{})["text"].(string)
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("unmarshal terminal json %q: %v", text, err)
		}
		return out
	}

	opened := invoke(map[string]interface{}{"session_id": "sess-lp", "op": "open", "cols": 80, "rows": 24})
	termID, _ := opened["term"].(string)
	if termID == "" {
		t.Fatalf("open missing term id: %+v", opened)
	}
	openFeatures, _ := opened["features"].(map[string]interface{})
	if openFeatures["long_poll"] != true {
		t.Fatalf("open features.long_poll = %v, want true", openFeatures["long_poll"])
	}

	readOut := invoke(map[string]interface{}{"session_id": "sess-lp", "op": "read", "term": termID, "cursor": 0, "wait_ms": 80})
	readFeatures, _ := readOut["features"].(map[string]interface{})
	if readFeatures["long_poll"] != true {
		t.Fatalf("read features.long_poll = %v, want true", readFeatures["long_poll"])
	}
	if readFeatures["input_source"] != true {
		t.Fatalf("read features.input_source = %v, want true", readFeatures["input_source"])
	}
	_ = invoke(map[string]interface{}{"session_id": "sess-lp", "op": "close", "term": termID})
}

func TestTerminalRCFileWrittenAndCleanedUp(t *testing.T) {
	rcPath, err := writeTerminalRCFile("term_rc_test")
	if err != nil {
		t.Fatalf("writeTerminalRCFile: %v", err)
	}
	raw, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rcfile: %v", err)
	}
	content := string(raw)
	for _, needle := range []string{"133;A", "133;B", "133;C", "133;D", "7;file://", "PROMPT_COMMAND", "return $ec"} {
		if !strings.Contains(content, needle) {
			t.Fatalf("rcfile missing %q", needle)
		}
	}
	session := &terminalSession{id: "term_rc_test", rcFile: rcPath, cmd: &exec.Cmd{}}
	session.shutdown()
	if _, err := os.Stat(rcPath); !os.IsNotExist(err) {
		t.Fatalf("expected rcfile %s to be removed on shutdown, err=%v", rcPath, err)
	}
}

func TestTerminalAISuggestionBlockedByCommandDenyRules(t *testing.T) {
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("sess-deny", 80, 24)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = gw.terminals.close("sess-deny", session.id) }()

	// 排空初始提示符，记录拒绝前的游标。
	time.Sleep(100 * time.Millisecond)
	_, cursorBefore, _, _, _, err := gw.terminals.read("sess-deny", session.id, 0)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	// T2 #5: source=ai_suggestion 且命中内置 deny_rm_root_filesystem（含 bracketed paste 包装）必须拒绝且 0 字节写入 PTY。
	dangerous := "\x15\x1b[200~rm -rf /\x1b[201~\r"
	rawArgs, _ := json.Marshal(map[string]interface{}{
		"session_id":    "sess-deny",
		"op":            "input",
		"term":          session.id,
		"data":          base64.StdEncoding.EncodeToString([]byte(dangerous)),
		"source":        "ai_suggestion",
		"suggestion_id": "sug_bad_1",
	})
	var resp api.JSONRPCResponse
	gw.handleToolTerminal(toolInvocation{
		ctx:       context.Background(),
		reqID:     1,
		sessionID: "sess-deny",
		args:      rawArgs,
		writeJSON: func(v interface{}) {
			b, _ := json.Marshal(v)
			_ = json.Unmarshal(b, &resp)
		},
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "blocked_command") {
		t.Fatalf("expected blocked_command error for ai_suggestion rm -rf /, got %+v", resp)
	}
	time.Sleep(100 * time.Millisecond)
	afterData, _, _, _, _, err := gw.terminals.read("sess-deny", session.id, cursorBefore)
	if err != nil {
		t.Fatalf("read after block: %v", err)
	}
	if strings.Contains(string(afterData), "rm -rf /") {
		t.Fatalf("blocked ai_suggestion leaked into PTY: %q", string(afterData))
	}

	// T2 #1: 安全命令带 source=ai_suggestion 正常写入 PTY 并执行。
	safe := "\x1b[200~echo AI_SAFE_OK\x1b[201~\r"
	safeArgs, _ := json.Marshal(map[string]interface{}{
		"session_id":    "sess-deny",
		"op":            "input",
		"term":          session.id,
		"data":          base64.StdEncoding.EncodeToString([]byte(safe)),
		"source":        "ai_suggestion",
		"suggestion_id": "sug_ok_1",
	})
	var okResp api.JSONRPCResponse
	gw.handleToolTerminal(toolInvocation{
		ctx:       context.Background(),
		reqID:     2,
		sessionID: "sess-deny",
		args:      safeArgs,
		writeJSON: func(v interface{}) {
			b, _ := json.Marshal(v)
			_ = json.Unmarshal(b, &okResp)
		},
	})
	if okResp.Error != nil {
		t.Fatalf("expected safe ai_suggestion to succeed, got %+v", okResp.Error)
	}
	var c = cursorBefore
	out := terminalTestDrain(t, gw, "sess-deny", session.id, &c, "AI_SAFE_OK")
	if !strings.Contains(out, "AI_SAFE_OK") {
		t.Fatalf("expected AI_SAFE_OK in PTY output, got %q", out)
	}
}

func TestTerminalAISuggestionSensitiveFilterAndAuditSummary(t *testing.T) {
	// T2 #4 + #8: 仅 source=ai_suggestion 的 input 提取敏感词段，用户直输不提取。
	aiBytes := base64.StdEncoding.EncodeToString([]byte("\x15\x1b[200~curl http://forbidden-canary.local\x1b[201~\r"))
	aiArgs, _ := json.Marshal(map[string]interface{}{
		"session_id":    "s1",
		"op":            "input",
		"term":          "term_1",
		"data":          aiBytes,
		"source":        "ai_suggestion",
		"suggestion_id": "sug_7",
	})
	segs := sensitiveFilterSegmentsForTool("doops_terminal", aiArgs)
	if len(segs) != 1 || segs[0].scope != sensitiveScopeCommand || segs[0].text != "curl http://forbidden-canary.local" {
		t.Fatalf("unexpected sensitive segments for ai_suggestion: %+v", segs)
	}

	userArgs, _ := json.Marshal(map[string]interface{}{
		"session_id": "s1",
		"op":         "input",
		"term":       "term_1",
		"data":       aiBytes,
	})
	if userSegs := sensitiveFilterSegmentsForTool("doops_terminal", userArgs); len(userSegs) != 0 {
		t.Fatalf("user direct PTY input should not produce sensitive segments, got %+v", userSegs)
	}

	// 审计摘要包含 source 与 suggestion id
	var m map[string]interface{}
	_ = json.Unmarshal(aiArgs, &m)
	summary := terminalToolSummary(m, aiArgs)
	if summary != "doops_terminal op=input term=term_1 source=ai_suggestion suggestion=sug_7" {
		t.Fatalf("unexpected terminalToolSummary: %q", summary)
	}
}

// M3 regression: bash 版本探测子进程必须剥离控制面敏感环境变量。
func TestTerminalBashVersionProbeScrubsSensitiveEnv(t *testing.T) {
	const (
		enrollCanary = "synthetic-probe-enroll-canary"
		mutCanary    = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	)
	t.Setenv("DOOPS_AGENT_ENROLL_TOKEN", enrollCanary)
	t.Setenv("DO_AGENT_MUTATION_AUTHORITY_SECRET", mutCanary)

	cmd := newBashVersionProbeCmd(context.Background(), "/bin/bash")
	if len(cmd.Env) == 0 {
		t.Fatalf("expected newBashVersionProbeCmd to set sanitized Env explicitly, got empty")
	}
	for _, kv := range cmd.Env {
		if strings.Contains(kv, enrollCanary) || strings.Contains(kv, mutCanary) ||
			strings.HasPrefix(kv, "DOOPS_AGENT_ENROLL_TOKEN=") ||
			strings.HasPrefix(kv, "DO_AGENT_MUTATION_AUTHORITY_SECRET=") {
			t.Fatalf("bash version probe leaked sensitive env entry: %q", kv)
		}
	}
}

// L5 regression: 过期孤儿 term_*.rc 清理不触碰活跃或无关文件。
func TestCleanupStaleTerminalRCFilesInDir(t *testing.T) {
	dir := t.TempDir()
	stalePath := dir + "/term_stale1.rc"
	freshPath := dir + "/term_fresh1.rc"
	otherPath := dir + "/keep.txt"
	for _, p := range []string{stalePath, freshPath, otherPath} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	now := time.Now()
	oldTime := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stalePath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}
	removed := cleanupStaleTerminalRCFilesInDir(dir, 24*time.Hour, now)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale rcfile was not deleted")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh rcfile should remain: %v", err)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("unrelated file should remain: %v", err)
	}
}

// T2 D4 端到端: Gateway 敏感词过滤拦截 source=ai_suggestion 的终端输入且不回显命中词，
// 而普通用户手动键入同一字节流不受阻挡。
func TestTerminalAISuggestionBlockedByGatewaySensitiveFilter(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	startClusterAgent(t, fixture, "dev")
	client := fixture.client(t, fixture.admin)

	const canaryRule = "FORBIDDEN_TERM_CANARY_987"
	if _, _, err := fixture.store.AddSensitiveRules([]SensitiveRuleInput{
		{Text: canaryRule, Scope: "command"},
	}); err != nil {
		t.Fatalf("add sensitive rule: %v", err)
	}
	if err := fixture.store.SetSensitiveFilterEnabled(true); err != nil {
		t.Fatalf("enable sensitive filter: %v", err)
	}

	payload := base64.StdEncoding.EncodeToString([]byte("\x15\x1b[200~echo " + canaryRule + "\x1b[201~\r"))

	// 1. source=ai_suggestion 必须在到达 Agent 前被 Gateway 敏感词拦截且不回显命中词
	lifecycleSend(t, client, 11, "doops_terminal", "term-sf-sess", map[string]any{
		"op":            "input",
		"term":          "term_sf_1",
		"data":          payload,
		"source":        "ai_suggestion",
		"suggestion_id": "sug_sf_1",
	})
	blockedResp := readK8SFunctionalResponse(t, client, 11)
	rpcErr, _ := blockedResp["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"] != float64(-32003) || rpcErr["message"] != "sensitive_command_blocked" {
		t.Fatalf("expected sensitive_command_blocked for ai_suggestion, got %#v", blockedResp)
	}
	if blockedResp["result"] != nil {
		t.Fatalf("denied ai_suggestion must not reach the Agent: %#v", blockedResp)
	}
	if msg, _ := rpcErr["message"].(string); strings.Contains(msg, canaryRule) {
		t.Fatalf("sensitive rule leaked in response: %q", msg)
	}

	// 2. 用户直接键入（无 source=ai_suggestion）不触发 Gateway 敏感词拦截
	lifecycleSend(t, client, 12, "doops_terminal", "term-sf-sess", map[string]any{
		"op":   "input",
		"term": "term_sf_1",
		"data": payload,
	})
	allowedResp := readK8SFunctionalResponse(t, client, 12)
	if errObj, _ := allowedResp["error"].(map[string]any); errObj != nil && errObj["message"] == "sensitive_command_blocked" {
		t.Fatalf("direct user terminal input must not be blocked by sensitive filter: %#v", allowedResp)
	}
}

// B1/B2/M4 真实 bash >= 4.4（Linux 容器）端到端验证：OSC 133 A/B/C/D、OSC 7、PS1 覆盖恢复与 rcfile 清理。
func TestTerminalShellIntegrationEmitsOSC133AndOSC7OnBash44Plus(t *testing.T) {
	shell := terminalShell()
	if !bashSupportsShellIntegration(shell) {
		t.Skipf("shell %s is not bash >= 4.4 on this host; run in Linux bash 5.x container", shell)
	}
	gw := NewGateway()
	t.Cleanup(gw.terminals.closeAll)

	session, err := gw.terminals.open("sess-osc133", 120, 32)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if session.shellIntegration != "osc133/v1" {
		t.Fatalf("shellIntegration = %q, want osc133/v1", session.shellIntegration)
	}
	rcPath := session.rcFile
	if rcPath == "" {
		t.Fatalf("expected rcFile to be generated")
	}
	info, err := os.Stat(rcPath)
	if err != nil {
		t.Fatalf("stat rcfile: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("rcfile perm = %o, want 0600", perm)
	}

	var cursor int64
	// 先等待 bash rcfile 初始化完成并输出首个 OSC 133;B 提示符，避免在 bash 启动前写入导致仅读到 PTY 回显。
	out := terminalTestDrain(t, gw, "sess-osc133", session.id, &cursor, "\x1b]133;B\x07")
	// 同时验证：
	// - B1: true (exit 0) / false (exit 1) / cd /tmp 的 OSC 133 A/B/C/D 与 OSC 7
	// - B2: 已有 PROMPT_COMMAND 钩子（如 _doops_audit）在 __doops_si_prompt 之后仍拿到真实退出码 $?
	// - L6: 含空格与 '%' 的目录名经 __doops_si_urlencode 正确编码
	// - M4: 模拟 starship/oh-my-bash 覆盖 PS1 后仍自动重包 133;A / 133;B
	// 使用算术展开 OSC_DONE_$((90+9)) 避免输入回显本身命中 OSC_DONE_99（与 INT-99 同类守卫）。
	script := "mkdir -p '/tmp/doops si %dir'\n" +
		"_test_audit_hook() { local ec=$?; printf 'AUDIT_EC=%d\\n' \"$ec\"; }\n" +
		"PROMPT_COMMAND=\"${PROMPT_COMMAND};_test_audit_hook\"\n" +
		"true\n" +
		"false\n" +
		"cd '/tmp/doops si %dir'\n" +
		"PS1='custom> '\n" +
		"echo OSC_DONE_$((90+9))\n"
	if err := gw.terminals.write("sess-osc133", session.id, []byte(script)); err != nil {
		t.Fatalf("write: %v", err)
	}
	out += terminalTestDrain(t, gw, "sess-osc133", session.id, &cursor, "OSC_DONE_99")
	// 确定性等待最后一条提示符（含重写后的 PS1 与 OSC 133;A / 133;B）落齐。
	if !strings.Contains(out, "\x1b]133;A\x07custom> \x1b]133;B\x07") {
		out += terminalTestDrain(t, gw, "sess-osc133", session.id, &cursor, "\x1b]133;A\x07custom> \x1b]133;B\x07")
	}
	t.Cleanup(func() { _ = os.RemoveAll("/tmp/doops si %dir") })

	for _, seq := range []string{
		"\x1b]133;A\x07",
		"\x1b]133;B\x07",
		"\x1b]133;C\x07",
		"\x1b]133;D;0\x07",
		"\x1b]133;D;1\x07",
		"\x1b]7;file://",
		"/tmp/doops%20si%20%25dir\x07",
		"AUDIT_EC=0",
		"AUDIT_EC=1",
	} {
		if !strings.Contains(out, seq) {
			t.Fatalf("OSC transcript missing %q; raw=%q", seq, out)
		}
	}
	// 验证 PS1 被改写成 'custom> ' 后，__doops_si_wrap_ps1 仍重新包裹了 133;A 与 133;B
	if !strings.Contains(out, "\x1b]133;A\x07custom> \x1b]133;B\x07") {
		t.Fatalf("expected overwritten PS1 to be re-wrapped with OSC 133 A/B; raw=%q", out)
	}

	if err := gw.terminals.close("sess-osc133", session.id); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(rcPath); !os.IsNotExist(err) {
		t.Fatalf("rcfile %s still exists after close", rcPath)
	}
}

// A2 + D2 端到端: 同一条复用 WS 上，token 吊销、用户停用或目标 grant 撤回后，
// 下一次 doops_terminal 调用必须立即被 Gateway 拒绝且绝不转发给 Agent。
func TestTerminalPooledWSBlocksRevokedTokenAndRevokedGrant(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(t *testing.T, f *executionLifecycleFixture, auth TokenAuth)
		wantCode float64
		wantPref string
	}{
		{
			name: "token_revoked",
			mutate: func(t *testing.T, f *executionLifecycleFixture, auth TokenAuth) {
				if err := f.store.RevokeToken(auth.TokenID); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: -32001,
			wantPref: "gateway_auth_revoked",
		},
		{
			name: "user_disabled",
			mutate: func(t *testing.T, f *executionLifecycleFixture, auth TokenAuth) {
				if err := f.store.SetUserDisabled(auth.UserID, true); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: -32001,
			wantPref: "gateway_auth_user_disabled",
		},
		{
			name: "grant_revoked",
			mutate: func(t *testing.T, f *executionLifecycleFixture, auth TokenAuth) {
				if _, err := f.store.db.Exec(`DELETE FROM grants WHERE user_id = ? AND cluster = ? AND instance = ?`, auth.UserID, "dev", "local"); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: -32003,
			wantPref: "forbidden:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			token := f.user(t, "term-op-"+tc.name, ScopeGrant{Cluster: "dev", Instance: "local"})
			auth, err := f.store.VerifyUserToken(token.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			agent, client := f.agent(t, "term-ws-"+tc.name), f.client(t, token)

			callTerminal := func(id int, args map[string]any) (map[string]any, bool) {
				if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				defer client.SetReadDeadline(time.Time{})
				lifecycleSend(t, client, id, "doops_terminal", "sess-pool", args)
				type reply struct {
					msg map[string]any
					err error
				}
				replies := make(chan reply, 1)
				go func() {
					for {
						var m map[string]any
						err := client.ReadJSON(&m)
						if err != nil || m["id"] == float64(id) {
							replies <- reply{msg: m, err: err}
							return
						}
					}
				}()
				forwarded := false
				for {
					select {
					case call := <-agent.calls:
						forwarded = true
						tokenLifecycleReplyText(t, agent, call, `{"ok":true,"term":"t1","cursor":0,"alive":true}`, false)
					case rep := <-replies:
						if rep.err != nil {
							t.Fatal(rep.err)
						}
						return rep.msg, forwarded
					}
				}
			}

			// 1. 变更前同一条复用 WS 正常转发到 Agent
			before, forwardedBefore := callTerminal(2, map[string]any{"op": "read", "term": "t1", "cursor": 0})
			if !forwardedBefore || before["error"] != nil {
				t.Fatalf("initial pooled doops_terminal failed: forwarded=%t resp=%#v", forwardedBefore, before)
			}

			// 2. 管理员吊销 token / 停用用户 / 撤回 grant
			tc.mutate(t, f, auth)

			// 3. 同一条 WS 再发 doops_terminal op=input：必须立即拒绝且绝不转发给 Agent
			after, forwardedAfter := callTerminal(3, map[string]any{
				"op": "input", "term": "t1", "data": base64.StdEncoding.EncodeToString([]byte("id\n")),
			})
			if forwardedAfter {
				t.Fatalf("%s: denied doops_terminal call was forwarded to Agent", tc.name)
			}
			rpcErr, _ := after["error"].(map[string]any)
			msg, _ := rpcErr["message"].(string)
			if rpcErr == nil || rpcErr["code"] != tc.wantCode || !strings.HasPrefix(msg, tc.wantPref) {
				t.Fatalf("%s: got error %#v, want code=%v prefix=%q", tc.name, rpcErr, tc.wantCode, tc.wantPref)
			}
		})
	}
}

func TestTerminalWorkdirPrefersExistingSessionWorkspaceAndFallsBackSafely(t *testing.T) {
	wsRoot := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", wsRoot)

	existingSession := "console-term-cwd-1"
	existingDir := filepath.Join(wsRoot, existingSession)
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir session workspace: %v", err)
	}

	if got := terminalWorkdirForSession(existingSession); got != existingDir {
		t.Fatalf("terminalWorkdirForSession(existing) = %q, want %q", got, existingDir)
	}

	fallback := terminalWorkdir()
	if got := terminalWorkdirForSession("console-missing-dir"); got != fallback {
		t.Fatalf("terminalWorkdirForSession(missing) = %q, want fallback %q", got, fallback)
	}
	if got := terminalWorkdirForSession("../escape"); got != fallback {
		t.Fatalf("terminalWorkdirForSession(traversal) = %q, want fallback %q", got, fallback)
	}

	mgr := newTerminalManager()
	defer mgr.closeAll()
	sess, err := mgr.open(existingSession, 80, 24)
	if err != nil {
		t.Fatalf("open session terminal: %v", err)
	}
	if sess.initialCwd != existingDir {
		t.Fatalf("opened terminal initialCwd = %q, want %q", sess.initialCwd, existingDir)
	}
}
