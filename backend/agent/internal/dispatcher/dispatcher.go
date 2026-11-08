package dispatcher

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type PathType int

const (
	PathFast PathType = iota
	PathSlow
	PathBlocked
)

// BlockRule 是一条命令护栏规则。命中后 exec 被拒绝并写入审计。
type BlockRule struct {
	ID   string
	Desc string
	re   *regexp.Regexp
}

// Config 控制护栏行为，通常由 FromEnv() 构造。
type Config struct {
	Extra   []string // 追加的自定义正则（作用于规范化后的命令）
	Disable []string // 禁用的内置规则 ID
	Off     bool     // 完全关闭护栏（应急用）
}

type Dispatcher struct {
	rules         []BlockRule
	naturalLangRe *regexp.Regexp
	off           bool
}

// 内置护栏规则：按灾难类别分组，正则作用于规范化（压缩空白+小写）后的命令。
var builtinRules = []struct {
	id, desc, pattern string
}{
	// 磁盘破坏
	{"disk.rm_root", "递归强制删除根目录或系统目录", `\brm\s+(-[a-z]+\s+)*-[a-z]*[rf][a-z]*\s+(-[a-z]+\s+)*/(\*|etc|usr|var|boot|bin|sbin|lib|lib64|sys|proc|dev|opt)?(\s|;|&|\||$)`},
	{"disk.dd_device", "dd 直接写块设备", `\bdd\s+[^;&|]*\bof=/dev/(sd|vd|nvme|hd|xvd)`},
	{"disk.mkfs", "格式化文件系统", `\bmkfs(\.[a-z0-9]+)?\b`},
	{"disk.partition", "磁盘分区/擦除工具", `\b(fdisk|sfdisk|parted|wipefs)\b`},
	// 系统停机
	{"sys.shutdown", "关机或重启", `(^|[;&|]\s*)(sudo\s+)?(shutdown|poweroff|halt|reboot)\b|\binit\s+[06]\b|systemctl\s+(poweroff|reboot|halt)\b`},
	{"sys.fork_bomb", "fork 炸弹", `:\s*\(\s*\)\s*\{`},
	// 权限破坏
	{"perm.chmod_root", "递归修改根目录权限", `\bchmod\s+(-[a-z]+\s+)*-[a-z]*r[a-z]*\s+[0-7]{3,4}\s+/\s*($|[;&|])`},
	{"fs.overwrite_key_files", "覆写系统关键文件", `>\s*/etc/(passwd|shadow|sudoers|group|fstab)\b`},
	// 远程脚本注入面
	{"net.pipe_shell", "下载并直接执行远程脚本", `\b(curl|wget)\s+[^;&|]*\|\s*(sudo\s+)?(bash|sh|zsh|python[0-9.]*|perl)\b`},
	// 进程屠杀
	{"proc.kill_all", "杀死所有进程", `\bkill\s+-[a-z0-9]*9[a-z0-9]*\s+-1\b|\bkillall5\b`},
}

var whitespaceRe = regexp.MustCompile(`\s+`)

// guardrailBypassScopes are the only caller metadata that can bypass a
// command guardrail. The Gateway injects these scopes on its authenticated
// private Agent connection; a public RPC caller never controls this field.
var guardrailBypassScopes = map[string]struct{}{
	"admin":           {},
	"exec:privileged": {},
}

// ScopeBypassesGuardrail reports whether trusted caller metadata authorizes a
// guardrail bypass. Unknown and ordinary target scopes remain guarded.
func ScopeBypassesGuardrail(scopes []string) bool {
	for _, scope := range scopes {
		if _, ok := guardrailBypassScopes[strings.TrimSpace(scope)]; ok {
			return true
		}
	}
	return false
}

// normalize 压缩连续空白并转小写，防止 "rm  -rf  /"、"RM -RF /" 这类绕过。
func normalize(cmd string) string {
	return whitespaceRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(cmd)), " ")
}

func NewDispatcher() *Dispatcher {
	return NewDispatcherWithConfig(Config{})
}

func NewDispatcherWithConfig(cfg Config) *Dispatcher {
	d := &Dispatcher{
		naturalLangRe: regexp.MustCompile(`^[\p{L}\s]{10,}$`),
		off:           cfg.Off,
	}
	disabled := make(map[string]bool, len(cfg.Disable))
	for _, id := range cfg.Disable {
		disabled[strings.TrimSpace(id)] = true
	}
	for _, b := range builtinRules {
		if disabled[b.id] {
			continue
		}
		d.rules = append(d.rules, BlockRule{ID: b.id, Desc: b.desc, re: regexp.MustCompile(b.pattern)})
	}
	for _, pattern := range cfg.Extra {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		d.rules = append(d.rules, BlockRule{ID: "custom", Desc: "自定义拦截规则: " + pattern, re: regexp.MustCompile(pattern)})
	}
	return d
}

// NewDispatcherFromEnv 从环境变量装配护栏：
//
//	DOOPS_BLOCK_EXTRA    追加正则，分号分隔，例如 'rm\s+-rf\s+/data;\bhalt\b'
//	DOOPS_BLOCK_DISABLE  禁用规则 ID，逗号分隔，例如 'net.pipe_shell,disk.partition'
//	DOOPS_BLOCK_OFF      置为 1/true 时完全关闭护栏（仅应急）
func NewDispatcherFromEnv() *Dispatcher {
	cfg := Config{}
	if v := strings.TrimSpace(os.Getenv("DOOPS_BLOCK_EXTRA")); v != "" {
		cfg.Extra = strings.Split(v, ";")
	}
	if v := strings.TrimSpace(os.Getenv("DOOPS_BLOCK_DISABLE")); v != "" {
		cfg.Disable = strings.Split(v, ",")
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("DOOPS_BLOCK_OFF"))); v == "1" || v == "true" {
		cfg.Off = true
	}
	return NewDispatcherWithConfig(cfg)
}

// ClassifyCommand 返回路径分类；被拦截时同时返回命中的规则。
func (d *Dispatcher) ClassifyCommand(command string) (PathType, *BlockRule) {
	if d.off {
		return PathFast, nil
	}
	norm := normalize(command)
	for i := range d.rules {
		if d.rules[i].re.MatchString(norm) {
			return PathBlocked, &d.rules[i]
		}
	}
	if d.naturalLangRe.MatchString(command) && !strings.ContainsAny(command, "/.-_") {
		return PathSlow, nil
	}
	return PathFast, nil
}

// Classify 保持旧签名兼容。
func (d *Dispatcher) Classify(command string) PathType {
	path, _ := d.ClassifyCommand(command)
	return path
}

// BlockedError identifies the guardrail and the safe next action.
type BlockedError struct {
	Rule string
	Hint string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("blocked_command(%s): %s", e.Rule, e.Hint)
}
func (d *Dispatcher) Check(command string) error {
	if path, rule := d.ClassifyCommand(command); path == PathBlocked {
		return &BlockedError{Rule: rule.ID, Hint: rule.Desc + "; name the exact authorized command or path"}
	}
	return nil
}
