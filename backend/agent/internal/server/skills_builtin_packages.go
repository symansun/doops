package server

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Builtin (system) inspection capabilities are shipped as real, editable Skill
// directory packages under skills_builtin/<slug>/. The package is the single
// source of truth: SKILL.md and scripts/ live on disk, while Go keeps only the
// metadata index below. Nothing in this file inlines SKILL.md bodies or script
// content, so editing a package file is enough to change the next execution.
//
// The directory layout and frontmatter contract follow
// backend/docs/design/SKILL_FIRST_INSPECTION_SPEC.md section 2.
//
//go:embed all:skills_builtin
var builtinSkillPackagesFS embed.FS

const builtinSkillPackagesRoot = "skills_builtin"

// defaultSystemSkillSpec is the metadata index of one builtin Skill package.
// Entrypoint names the deterministic script entry declared by the package; an
// empty Entrypoint means "auto-detect", matching the SKILL.md frontmatter
// contract (frontmatter entrypoint wins, otherwise scripts/inspect.sh).
//
// There is deliberately no driver mode / strategy id here: execution is
// dispatched purely by the Skill package (entrypoint + exec_mode), per
// SKILL_FIRST_INSPECTION_SPEC.md 2.3 (defect D9).
type defaultSystemSkillSpec struct {
	Slug        string
	Name        string
	Description string
	Category    string
	Entrypoint  string
}

// builtinSystemSkills is the single definition of the system Skill set.
// Keep this in sync with the skills_builtin/ directory tree; the loader below
// fails closed when a spec has no matching package.
var builtinSystemSkills = []defaultSystemSkillSpec{
	{
		Slug:        "inspect-vm-host",
		Name:        "虚机与物理机系统巡检 Skill",
		Description: "通过 Agent 本机只读探测 CPU、内存、内核、磁盘容量与 I/O、网卡流量、NVIDIA GPU 及 M1–M5 主机安全基线。",
		Category:    "inspection",
		Entrypoint:  "scripts/inspect.sh",
	},
	{
		Slug:        "inspect-ssh-host",
		Name:        "无 Agent 远程主机 SSH 巡检 Skill",
		Description: "由在线 Agent 通过结构化只读 BatchMode SSH 远程采集无 Agent 主机的资源水位与系统安全基线。",
		Category:    "inspection",
	},
	{
		Slug:        "inspect-k8s-cluster",
		Name:        "Kubernetes 容器集群巡检 Skill",
		Description: "通过 Agent 内置只读 kubectl 巡检 K8s 节点健康压力、异常/高重启 Pod、控制器副本、PVC 绑定、Warning 事件及资源水位。",
		Category:    "inspection",
		Entrypoint:  "scripts/inspect.sh",
	},
	{
		Slug:        "inspect-container-runtime",
		Name:        "容器运行时巡检 Skill",
		Description: "自动识别 Docker / nerdctl / Podman，采集运行中容器 CPU、内存与内存上限（memory_limit）占比。",
		Category:    "inspection",
		Entrypoint:  "scripts/inspect.sh",
	},
	{
		Slug:        "inspect-app-web",
		Name:        "应用服务与 Web 安全巡检 Skill",
		Description: "探测已登记站点 HTTP/HTTPS 可用性、响应延迟、错误率、TLS 证书剩余天数、安全响应头及本机 Nginx 并发请求速率。",
		Category:    "inspection",
		Entrypoint:  "scripts/inspect.sh",
	},
	{
		Slug:        "inspect-database",
		Name:        "数据库运行与连通性巡检 Skill",
		Description: "探测已登记数据库实例（PostgreSQL / MySQL / Redis 等）的 TCP 连通性与建连延迟，识别实例不可达或高延迟风险。",
		Category:    "inspection",
	},
	{
		Slug:        "inspect-proxy-evasion",
		Name:        "主机违规代理与内网穿透巡检 Skill",
		Description: "由系统驱动 Agent Skill 检查主机是否存在未备案代理/翻墙/穿透进程（clash、v2ray、xray、frpc、trojan 等）及异常监听端口。",
		Category:    "security",
		Entrypoint:  "scripts/inspect.sh",
	},
	{
		Slug:        "inspect-package-vuln",
		Name:        "主机软件包与补丁基线巡检 Skill",
		Description: "由系统驱动 Agent Skill 只读盘点本机已安装关键软件包版本（OpenSSL、OpenSSH、Nginx、内核等）与待更新安全补丁概况。",
		Category:    "security",
		Entrypoint:  "scripts/inspect.sh",
	},
}

// loadBuiltinSkillPackage reads one embedded Skill package: SKILL.md plus every
// attachment (sorted by relative path), and resolves the entrypoint. It fails
// closed when the package or its declared entrypoint is missing so a broken
// embed is caught at startup instead of silently degrading to a prompt run.
func loadBuiltinSkillPackage(spec defaultSystemSkillSpec) (string, []platformSkillFile, error) {
	dir := path.Join(builtinSkillPackagesRoot, spec.Slug)
	mdBytes, err := fs.ReadFile(builtinSkillPackagesFS, path.Join(dir, "SKILL.md"))
	if err != nil {
		return "", nil, fmt.Errorf("builtin skill %s: SKILL.md missing: %w", spec.Slug, err)
	}
	skillMD := string(mdBytes)

	files := []platformSkillFile{}
	walkErr := fs.WalkDir(builtinSkillPackagesFS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		if strings.EqualFold(rel, "SKILL.md") {
			return nil
		}
		data, readErr := fs.ReadFile(builtinSkillPackagesFS, p)
		if readErr != nil {
			return readErr
		}
		files = append(files, platformSkillFile{Path: rel, Content: string(data)})
		return nil
	})
	if walkErr != nil {
		return "", nil, fmt.Errorf("builtin skill %s: walk package: %w", spec.Slug, walkErr)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	entrypoint := strings.TrimSpace(spec.Entrypoint)
	if fromFrontmatter := skillFrontmatterValue(skillMD, "entrypoint"); fromFrontmatter != "" {
		entrypoint = fromFrontmatter
	}
	if entrypoint == "" {
		for _, f := range files {
			if f.Path == "scripts/inspect.sh" {
				entrypoint = f.Path
				break
			}
		}
	}
	if entrypoint != "" {
		found := false
		for _, f := range files {
			if f.Path == entrypoint {
				found = true
				break
			}
		}
		if !found {
			return "", nil, fmt.Errorf("builtin skill %s: entrypoint %q not present in package", spec.Slug, entrypoint)
		}
	}
	return skillMD, files, nil
}

// skillFrontmatterValue returns the trimmed value of key from a SKILL.md YAML
// frontmatter block, or "" when absent.
func skillFrontmatterValue(md, key string) string {
	trimmed := strings.TrimSpace(md)
	if !strings.HasPrefix(trimmed, "---") {
		return ""
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:idx], "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
