package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 集成测试策略：构建真实 CLI 二进制，用 DOOPS_CONFIG 隔离配置，
// 跑真实子命令并断言 stdout 上的 Error + Hint 双层输出。

var (
	buildOnce     sync.Once
	builtCLIPath  string
	builtCLIError error
)

func cliBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtCLIPath = filepath.Join(os.TempDir(), "doops-errhint-itest")
		cmd := exec.Command("go", "build", "-o", builtCLIPath, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			builtCLIError = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if builtCLIError != nil {
		t.Fatalf("build CLI: %v", builtCLIError)
	}
	return builtCLIPath
}

func runCLI(t *testing.T, configPath string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary(t), args...)
	cmd.Env = append(os.Environ(), "DOOPS_CONFIG="+configPath)
	out, err := cmd.CombinedOutput() // 非零退出码是预期路径，不视为测试失败
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("CLI hung for 60s (args=%v), output so far:\n%s", args, out)
	}
	_ = err
	return string(out)
}

func writeConfig(t *testing.T, servers ...Server) string {
	t.Helper()
	data, err := json.Marshal(RootConfig{Servers: servers})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// target 未配置：Error 指出问题，Hint 给出查看与新增路径。
func TestIntegrationServerNotFoundHint(t *testing.T) {
	cfg := writeConfig(t) // 空 servers
	out := runCLI(t, cfg, "-session", "s1", "exec", "-target", "ghost", "-cmd", "echo hi")

	if !strings.Contains(out, "Error: Server 'ghost' not found.") {
		t.Fatalf("missing not-found error, output:\n%s", out)
	}
	if !strings.Contains(out, "doops login --target ghost") {
		t.Fatalf("missing actionable hint, output:\n%s", out)
	}
	if !strings.Contains(out, "doops targets") {
		t.Fatalf("hint should mention doops targets, output:\n%s", out)
	}
}

// gateway 对 WS 握手返回 401：Hint 必须指向重新登录（而不是网络排查）。
func TestIntegration401Hint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	cfg := writeConfig(t, Server{
		Name: "demo", IP: "10.0.0.1", Port: "42222", Use: "ops",
		Token: "expired-token", Gateway: ts.URL, Cluster: "c", Instance: "i",
	})
	out := runCLI(t, cfg, "-session", "s1", "exec", "-target", "demo", "-cmd", "echo hi")

	// 关键：先断言错误本体来自 HTTP 401 路径，防止 target 缺失等其它路径
	// （其提示语同样含 "doops login"）造成假阳性。
	if !strings.Contains(out, "401") {
		t.Fatalf("expected HTTP 401 in error line, output:\n%s", out)
	}
	if strings.Contains(out, "not found") {
		t.Fatalf("must not fall into target-not-found path, output:\n%s", out)
	}
	if !strings.Contains(out, "doops login") {
		t.Fatalf("401 should hint re-login, output:\n%s", out)
	}
	if strings.Contains(out, "网络可达性") {
		t.Fatalf("401 must not be misclassified as network issue, output:\n%s", out)
	}
}

// 回归：gateway target 的 exec 必须穿过校验走到连接层（不被 gateway-only 拦截），
// 而 bash 子命令仍应拒绝 gateway target（该拦截本属于 bash）。
func TestIntegrationGatewayOnlyGuardPlacement(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	cfg := writeConfig(t, Server{
		Name: "demo", IP: "10.0.0.1", Port: "42222", Use: "ops",
		Token: "t", Gateway: ts.URL, Cluster: "c", Instance: "i",
	})

	execOut := runCLI(t, cfg, "-session", "s1", "exec", "-target", "demo", "-cmd", "echo hi")
	if strings.Contains(execOut, "interactive SSH bash is not supported") {
		t.Fatalf("exec must not hit bash-only guard, output:\n%s", execOut)
	}

	bashOut := runCLI(t, cfg, "bash", "-target", "demo")
	if !strings.Contains(bashOut, "interactive SSH bash is not supported") {
		t.Fatalf("bash on gateway target should be rejected, output:\n%s", bashOut)
	}
}

// gateway 地址不可达：Hint 给出三步排查清单。
func TestIntegrationConnRefusedHint(t *testing.T) {
	cfg := writeConfig(t, Server{
		Name: "demo", IP: "10.0.0.1", Port: "42222", Use: "ops",
		Token: "t", Gateway: "http://127.0.0.1:1", Cluster: "c", Instance: "i",
	})
	out := runCLI(t, cfg, "-session", "s1", "exec", "-target", "demo", "-cmd", "echo hi")

	// 先断言错误本体是连接层失败（而非 target 缺失等其它路径）。
	if !strings.Contains(out, "refused") {
		t.Fatalf("expected connection refused in error line, output:\n%s", out)
	}
	if !strings.Contains(out, "gateway 地址") || !strings.Contains(out, "doops targets") {
		t.Fatalf("connection failure should hint three-step check, output:\n%s", out)
	}
}
