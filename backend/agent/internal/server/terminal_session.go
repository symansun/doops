package server

// 交互式终端（PTY）：为控制台「终端」页提供真实 shell。
//
// 为什么不是 doops_shell：doops_shell 每条命令起一个新进程，没有 TTY、没有
// stdin、cwd/env 不保持，top/vim 这类全屏程序无法运行。这里用 creack/pty 起一个
// 长驻 shell，把 PTY 输出写进环形缓冲，客户端按绝对字节游标增量读取。
//
// 传输方式刻意不用长连接 RPC：read/input 都是普通的工具调用，因此刷新页面、
// 网络抖动不会杀掉 shell；停止一次轮询也不影响远端进程。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// terminalBufferLimit 是单个终端的输出回卷上限；超出后丢弃最旧的字节，
	// 并把丢弃量记进 base，使游标语义保持绝对位置。
	terminalBufferLimit = 512 << 10
	// terminalReadChunk 限制单次 read 返回的字节数，避免一次轮询拖垮响应。
	terminalReadChunk  = 128 << 10
	terminalInputLimit = 64 << 10
	// terminalReadWaitMax 限制单次长轮询 read 的最大阻塞时长，远低于 Gateway
	// operation-timeout，避免被 toolDeadline 误判超时。
	terminalReadWaitMax = 10 * time.Second
	terminalIdleLimit   = 30 * time.Minute
	terminalMaxPerSess  = 4
	terminalMaxTotal    = 32
	terminalMinCols     = 20
	terminalMaxCols     = 400
	terminalMinRows     = 5
	terminalMaxRows     = 200
	terminalCloseGrace  = 2 * time.Second
)

var (
	errTerminalNotFound  = errors.New("terminal not found or already closed")
	errTerminalForbidden = errors.New("terminal does not belong to this session")
	errTerminalLimit     = errors.New("this session already has the maximum number of terminals")
	errTerminalCapacity  = errors.New("gateway terminal capacity reached")
	errTerminalClosed    = errors.New("terminal is closed")
)

type terminalSession struct {
	id               string
	sessionID        string
	rcFile           string
	shellIntegration string
	initialCwd       string

	cmd  *exec.Cmd
	ptmx *os.File

	// notify 在有新输出、进程退出或会话关闭时非阻塞唤醒正在等待的长轮询 read。
	notify chan struct{}

	mu       sync.Mutex
	buf      []byte
	base     int64
	closed   bool
	exited   bool
	exitCode int
	lastUsed time.Time
	cols     int
	rows     int
}

func clampTerminalReadWait(waitMS int) time.Duration {
	if waitMS <= 0 {
		return 0
	}
	wait := time.Duration(waitMS) * time.Millisecond
	if wait > terminalReadWaitMax {
		return terminalReadWaitMax
	}
	return wait
}

func terminalFeatures(shellIntegration string) map[string]interface{} {
	features := map[string]interface{}{
		"long_poll":    true,
		"input_source": true,
	}
	if shellIntegration != "" {
		features["shell_integration"] = shellIntegration
	}
	return features
}

const terminalShellIntegrationRC = `# Managed by doops-agent for interactive terminal OSC 133/7 integration.
[ -r /etc/profile ] && . /etc/profile
if   [ -r ~/.bash_profile ]; then . ~/.bash_profile
elif [ -r ~/.bash_login ];   then . ~/.bash_login
elif [ -r ~/.profile ];      then . ~/.profile
fi
# 仅当 profile 尚未加载 ~/.bashrc（且 _doops_audit 未定义）时兜底加载一次，避免重复 source。
if [ -z "$__DOOPS_BASHRC_DONE" ] && ! declare -F _doops_audit >/dev/null 2>&1 && [ -r ~/.bashrc ]; then
  __DOOPS_BASHRC_DONE=1
  . ~/.bashrc
fi

if [ -z "$__DOOPS_SI" ]; then
  __DOOPS_SI=1
  __doops_si_urlencode() {
    local s="$1"
    s="${s//%/%25}"
    s="${s// /%20}"
    s="${s//#/%23}"
    s="${s//\?/%3F}"
    printf '%s' "$s"
  }
  __doops_si_wrap_ps1() {
    case "$PS1" in
      *$'\033]133;A'*$'\033]133;B'*) ;;
      *) PS1='\[\033]133;A\007\]'"${PS1}"'\[\033]133;B\007\]' ;;
    esac
  }
  __doops_si_prompt() {
    local ec=$?
    [ -n "$__doops_si_started" ] && printf '\033]133;D;%s\007' "$ec"
    __doops_si_started=1
    printf '\033]7;file://%s%s\007' "${HOSTNAME:-localhost}" "$(__doops_si_urlencode "$PWD")"
    __doops_si_wrap_ps1
    return $ec
  }
  PROMPT_COMMAND="__doops_si_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
  __doops_si_wrap_ps1
  PS0=$'\033]133;C\007'"${PS0}"
fi
`

var (
	bashVersionCacheMu sync.Mutex
	bashVersionCache   = map[string]bool{}
)

// newBashVersionProbeCmd 构造用于探测 bash 版本的受控子进程，统一剥离 Agent 控制面令牌。
func newBashVersionProbeCmd(ctx context.Context, shell string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, shell, "-c", `printf '%s.%s' "${BASH_VERSINFO[0]}" "${BASH_VERSINFO[1]}"`)
	cmd.Env = agentSanitizedChildEnv()
	return cmd
}

// bashSupportsShellIntegration 检查 shell 是否为支持 PS0（OSC 133;C）的 bash >= 4.4。
func bashSupportsShellIntegration(shell string) bool {
	if filepath.Base(shell) != "bash" {
		return false
	}
	bashVersionCacheMu.Lock()
	if cached, ok := bashVersionCache[shell]; ok {
		bashVersionCacheMu.Unlock()
		return cached
	}
	bashVersionCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := newBashVersionProbeCmd(ctx, shell).Output()
	supported := false
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ".")
		if len(parts) >= 2 {
			major, err1 := strconv.Atoi(parts[0])
			minor, err2 := strconv.Atoi(parts[1])
			if err1 == nil && err2 == nil && (major > 4 || (major == 4 && minor >= 4)) {
				supported = true
			}
		}
	}

	bashVersionCacheMu.Lock()
	bashVersionCache[shell] = supported
	bashVersionCacheMu.Unlock()
	return supported
}

func terminalRCBaseDir() string {
	const preferred = "/root/.doops/term"
	if err := os.MkdirAll(preferred, 0o700); err == nil {
		return preferred
	}
	fallback := filepath.Join(os.TempDir(), "doops-term")
	_ = os.MkdirAll(fallback, 0o700)
	return fallback
}

func writeTerminalRCFile(termID string) (string, error) {
	dir := terminalRCBaseDir()
	path := filepath.Join(dir, termID+".rc")
	if err := os.WriteFile(path, []byte(terminalShellIntegrationRC), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// cleanupStaleTerminalRCFiles 删除超出 maxAge 的历史残留 term_*.rc 文件。
func cleanupStaleTerminalRCFiles(maxAge time.Duration) int {
	return cleanupStaleTerminalRCFilesInDir(terminalRCBaseDir(), maxAge, time.Now())
}

func cleanupStaleTerminalRCFilesInDir(dir string, maxAge time.Duration, now time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "term_") || !strings.HasSuffix(name, ".rc") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= maxAge {
			if os.Remove(filepath.Join(dir, name)) == nil {
				removed++
			}
		}
	}
	return removed
}

type terminalManager struct {
	mu       sync.Mutex
	sessions map[string]*terminalSession
}

func newTerminalManager() *terminalManager {
	// 启动时惰性清理超过 24 小时的孤儿 rcfile（Agent 崩溃残留），不触碰活跃会话文件。
	go cleanupStaleTerminalRCFiles(24 * time.Hour)
	return &terminalManager{sessions: make(map[string]*terminalSession)}
}

// terminalShell 选一个能提供 login shell 语义的解释器。
func terminalShell() string {
	for _, candidate := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "/bin/sh"
}

// terminalSignalGuardOnce 保证子 shell 拿到默认的 SIGINT 处置。
//
// Agent 可能被 systemd、Kubernetes 或包装脚本以"忽略 SIGINT"的方式拉起，
// fork/exec 出来的 shell 会继承 SIG_IGN。交互式 bash 不会自己恢复被忽略的信号，
// 于是 Ctrl-C 只剩一个 ^C 回显：sleep 之类的前台作业不中断，提示符也回不来。
// Go 的 exec 不重置信号处置，但被 Go 接管的信号在 exec 时会恢复默认，所以这里
// 用一个只进不读的通道把 SIGINT 从"忽略"改成"已接管"，让子 shell 看到默认行为。
var (
	terminalSignalSink    = make(chan os.Signal, 1)
	terminalSignalLogOnce sync.Once
)

func ensureInterruptibleShell() {
	if !signal.Ignored(os.Interrupt) {
		return
	}
	// 每次 open 都检查：进程可能在启动后被重新置为忽略（包装脚本、测试、重启路径）。
	signal.Notify(terminalSignalSink, os.Interrupt)
	terminalSignalLogOnce.Do(func() {
		log.Printf("🖥️ SIGINT 此前被忽略（继承自启动环境），已改由 Agent 接管，交互式终端恢复 Ctrl-C 中断")
	})
}

// terminalWorkdir 让终端默认落在 Agent 的工作目录（通常是 /workspace）。
func terminalWorkdir() string {
	if info, err := os.Stat("/workspace"); err == nil && info.IsDir() {
		return "/workspace"
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "/"
}

// terminalWorkdirForSession 优先落在当前会话已创建的 AI 工作区目录（如 /root/ws/<session>），
// 让人工接管交互终端时与 AI 会话处于同一目录；若该目录不存在或会话 ID 非法则回退默认目录。
func terminalWorkdirForSession(sessionID string) string {
	if dir, err := workspacePath(sessionID); err == nil {
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			return dir
		}
	}
	return terminalWorkdir()
}

func terminalClampInt(value, min, max, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func newTerminalID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("term_%d", time.Now().UnixNano())
	}
	return "term_" + hex.EncodeToString(raw)
}

// open 启动一个挂在 PTY 上的 login shell。
func (m *terminalManager) open(sessionID string, cols, rows int) (*terminalSession, error) {
	cols = terminalClampInt(cols, terminalMinCols, terminalMaxCols, 120)
	rows = terminalClampInt(rows, terminalMinRows, terminalMaxRows, 32)

	m.mu.Lock()
	if len(m.sessions) >= terminalMaxTotal {
		m.mu.Unlock()
		return nil, errTerminalCapacity
	}
	inSession := 0
	for _, existing := range m.sessions {
		if existing.sessionID == sessionID {
			inSession++
		}
	}
	if inSession >= terminalMaxPerSess {
		m.mu.Unlock()
		return nil, errTerminalLimit
	}
	m.mu.Unlock()

	ensureInterruptibleShell()

	termID := newTerminalID()
	shell := terminalShell()
	args := []string{"-l"}
	rcFile := ""
	shellIntegration := ""
	if filepath.Base(shell) == "sh" {
		args = nil
	} else if bashSupportsShellIntegration(shell) {
		if path, err := writeTerminalRCFile(termID); err == nil {
			rcFile = path
			shellIntegration = "osc133/v1"
			args = []string{"--rcfile", rcFile, "-i"}
		}
	}
	initialCwd := terminalWorkdirForSession(sessionID)
	cmd := exec.Command(shell, args...)
	cmd.Dir = initialCwd
	env := agentSanitizedChildEnv("TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=doops-console")
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=C.UTF-8")
	}
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		if rcFile != "" {
			_ = os.Remove(rcFile)
		}
		return nil, fmt.Errorf("start pty: %w", err)
	}

	session := &terminalSession{
		id:               termID,
		sessionID:        sessionID,
		rcFile:           rcFile,
		shellIntegration: shellIntegration,
		initialCwd:       initialCwd,
		cmd:              cmd,
		ptmx:             ptmx,
		notify:           make(chan struct{}, 1),
		lastUsed:         time.Now(),
		cols:             cols,
		rows:             rows,
	}

	go session.readLoop()
	go session.waitLoop()

	m.mu.Lock()
	m.sessions[session.id] = session
	m.mu.Unlock()

	log.Printf("🖥️ terminal %s opened for session %q (%dx%d, pid %d)", session.id, sessionID, cols, rows, cmd.Process.Pid)
	return session, nil
}

func (s *terminalSession) readLoop() {
	chunk := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			if !s.closed {
				s.appendLocked(chunk[:n])
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *terminalSession) signalLocked() {
	if s.notify == nil {
		return
	}
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *terminalSession) waitLoop() {
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	s.mu.Lock()
	s.exited = true
	s.exitCode = code
	s.signalLocked()
	s.mu.Unlock()
}

// appendLocked 追加输出并在超限时丢弃最旧的字节，同时前移 base。
func (s *terminalSession) appendLocked(chunk []byte) {
	s.buf = append(s.buf, chunk...)
	if len(s.buf) > terminalBufferLimit {
		over := len(s.buf) - terminalBufferLimit
		s.buf = append(s.buf[:0], s.buf[over:]...)
		s.base += int64(over)
	}
	s.signalLocked()
}

// readFrom 返回游标之后的新输出；游标早于缓冲起点时标记 truncated。
func (s *terminalSession) readFrom(cursor int64) (data []byte, next int64, truncated bool, alive bool, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()

	if cursor < s.base {
		cursor = s.base
		truncated = true
	}
	end := s.base + int64(len(s.buf))
	if cursor > end {
		cursor = end
	}
	offset := int(cursor - s.base)
	data = append([]byte(nil), s.buf[offset:]...)
	if len(data) > terminalReadChunk {
		data = data[:terminalReadChunk]
	}
	return data, cursor + int64(len(data)), truncated, !s.exited, s.exitCode
}

// readWait 在游标后暂无新数据时最多阻塞 wait；一旦有输出、截断、进程退出、
// 会话关闭或上下文取消即刻返回。
func (s *terminalSession) readWait(ctx context.Context, cursor int64, wait time.Duration) (data []byte, next int64, truncated bool, alive bool, code int) {
	data, next, truncated, alive, code = s.readFrom(cursor)
	if len(data) > 0 || truncated || !alive || wait <= 0 || s.notify == nil {
		return data, next, truncated, alive, code
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return s.readFrom(cursor)
		}
		select {
		case <-s.notify:
			data, next, truncated, alive, code = s.readFrom(cursor)
			if len(data) > 0 || truncated || !alive {
				return data, next, truncated, alive, code
			}
		case <-timer.C:
			return s.readFrom(cursor)
		case <-ctx.Done():
			return s.readFrom(cursor)
		}
	}
}

func (s *terminalSession) write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errTerminalClosed
	}
	if s.exited {
		return fmt.Errorf("terminal process has exited with code %d", s.exitCode)
	}
	if len(data) > terminalInputLimit {
		data = data[:terminalInputLimit]
	}
	s.lastUsed = time.Now()
	_, err := s.ptmx.Write(data)
	return err
}

func (s *terminalSession) resize(cols, rows int) {
	cols = terminalClampInt(cols, terminalMinCols, terminalMaxCols, s.cols)
	rows = terminalClampInt(rows, terminalMinRows, terminalMaxRows, s.rows)
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.lastUsed = time.Now()
	s.mu.Unlock()
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// shutdown 先挂断进程组，再在宽限期后强杀，确保 shell 的子进程一并退出。
func (s *terminalSession) shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.signalLocked()
	proc := s.cmd.Process
	rcFile := s.rcFile
	s.mu.Unlock()

	if rcFile != "" {
		_ = os.Remove(rcFile)
	}

	if proc == nil {
		_ = s.ptmx.Close()
		return
	}
	_ = syscall.Kill(-proc.Pid, syscall.SIGHUP)
	_ = s.ptmx.Close()

	deadline := time.Now().Add(terminalCloseGrace)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
}

func (m *terminalManager) lookup(sessionID, termID string) (*terminalSession, error) {
	m.mu.Lock()
	session, ok := m.sessions[termID]
	m.mu.Unlock()
	if !ok {
		return nil, errTerminalNotFound
	}
	if session.sessionID != sessionID {
		return nil, errTerminalForbidden
	}
	return session, nil
}

func (m *terminalManager) read(sessionID, termID string, cursor int64) ([]byte, int64, bool, bool, int, error) {
	session, err := m.lookup(sessionID, termID)
	if err != nil {
		return nil, 0, false, false, 0, err
	}
	data, next, truncated, alive, code := session.readFrom(cursor)
	return data, next, truncated, alive, code, nil
}

func (m *terminalManager) write(sessionID, termID string, data []byte) error {
	session, err := m.lookup(sessionID, termID)
	if err != nil {
		return err
	}
	return session.write(data)
}

func (m *terminalManager) resize(sessionID, termID string, cols, rows int) error {
	session, err := m.lookup(sessionID, termID)
	if err != nil {
		return err
	}
	session.resize(cols, rows)
	return nil
}

func (m *terminalManager) close(sessionID, termID string) error {
	session, err := m.lookup(sessionID, termID)
	if err != nil {
		return err
	}
	session.shutdown()
	m.mu.Lock()
	delete(m.sessions, termID)
	m.mu.Unlock()
	return nil
}

// reap 回收空闲终端和已经退出的终端，避免 Agent 里堆着没人用的 shell。
func (m *terminalManager) reap(now time.Time) int {
	m.mu.Lock()
	expired := make([]*terminalSession, 0, 4)
	for id, session := range m.sessions {
		session.mu.Lock()
		idle := now.Sub(session.lastUsed) > terminalIdleLimit
		gone := session.exited || session.closed
		session.mu.Unlock()
		if idle || gone {
			expired = append(expired, session)
			delete(m.sessions, id)
		}
	}
	remaining := len(m.sessions)
	m.mu.Unlock()
	for _, session := range expired {
		session.shutdown()
	}
	if len(expired) > 0 {
		log.Printf("🧹 terminal reaper closed %d session(s), %d remaining", len(expired), remaining)
	}
	return len(expired)
}

func (m *terminalManager) startReaper() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			m.reap(now)
		}
	}()
}

// closeAll 在 Agent 退出时清掉所有终端，避免留下孤儿 shell。
func (m *terminalManager) closeAll() {
	m.mu.Lock()
	all := make([]*terminalSession, 0, len(m.sessions))
	for id, session := range m.sessions {
		all = append(all, session)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, session := range all {
		session.shutdown()
	}
}

// terminalPTYDeviceLine 匹配 `tty` 打印的设备名。只接受两种真实形态：
// /dev/pts/<n> 与 /dev/tty*（/dev/tty、/dev/tty0…）。路径里的其它设备、
// 或者被当作普通文本回显的 "/dev/pts/9" 都不算，因为整行必须就是设备名。
var terminalPTYDeviceLine = regexp.MustCompile(`^/dev/(?:pts/[0-9]+|tty[A-Za-z0-9]*)$`)

// stripTerminalANSI 去掉 CSI（ESC [ … final）与 OSC（ESC ] … BEL|ESC \）序列。
//
// GitHub runner 的 bash 打开了 shell integration：提示符前后会插入
// OSC 3008（\x1b]3008;start=…;type=shell;…\x1b\）以及 \x1b[?2004h 之类的
// 私有模式序列，这些字节会和命令输出落在同一行上，逐行比较前必须先剥掉。
func stripTerminalANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return b.String()
		}
		switch s[i+1] {
		case '[': // CSI：参数字节 0x20-0x3f，最终字节 0x40-0x7e
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				return b.String()
			}
			i = j
		case ']': // OSC：BEL 或 ST(ESC \) 结束，内容可任意长
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					i = j
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					i = j + 1
					break
				}
				j++
			}
			if j >= len(s) {
				return b.String()
			}
		default: // 两字节转义序列
			i++
		}
	}
	return b.String()
}

// terminalPTYDeviceProbe 是测试探针命令用的前缀：测试让 shell 执行
// `printf 'PTY=%s\n' "$(tty)"`，只有真正跑过 tty 才会产生 "PTY=/dev/…"。
// 被 PTY 回显的命令行里只有字面量 "PTY=%s"，所以回显不可能满足这条断言。
const terminalPTYDeviceProbe = "PTY"

// 标签从 terminalPTYDeviceProbe 生成：改名时解析器与探针脚本不会各走一边。
var terminalPTYDeviceProbeLine = regexp.MustCompile(regexp.QuoteMeta(terminalPTYDeviceProbe) + `=(/dev/(?:pts/[0-9]+|tty[A-Za-z0-9]*))`)

// terminalPTYDevice 从终端回卷里取出探针打印的设备名，找不到返回 ""。
//
// 为什么按探针前缀找，而不是只认"整行就是设备名"（INT-99 的做法）：GitHub runner
// 上 `tty` 自己的输出后面不一定跟着换行——run 45416198758 的回卷是
// "/dev/pts/0TTY_READY_42\r\n"，设备名和紧随其后的 echo 粘在同一行，整行匹配
// 必然判空，于是同一个用例在 CI 上继续红。探针自己带前缀，断言就不再依赖邻居字节。
//
// 回卷里还有被 PTY 回显的命令本身、提示符和 runner 注入的 OSC/CSI 噪声，所以这里
// 不能退化成 strings.Contains(output, "/dev/")——回显的命令行里只要写了一个 /dev/
// 路径就会误判成 pty。作为兜底，整行恰好是设备名（反馈里没有探针时）也接受。
func terminalPTYDevice(output string) string {
	clean := stripTerminalANSI(output)
	if m := terminalPTYDeviceProbeLine.FindAllStringSubmatch(clean, -1); len(m) > 0 {
		return m[len(m)-1][1]
	}
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), "\r")
		if terminalPTYDeviceLine.MatchString(line) {
			return line
		}
	}
	return ""
}
