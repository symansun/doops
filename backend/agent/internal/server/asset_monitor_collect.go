package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/doops/agent/api"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// kubectl top returns rounded usage and no upstream timestamp. at_ms is the
// Gateway observation time. No API object, Pod spec or log is requested.
func (h *GatewayHub) collectMonitorPods(ctx context.Context, auth TokenAuth, req monitorRequest, session string) ([]monitorInput, error) {
	read, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 30*time.Second)
	defer cancel()
	out, err := h.RunInternalToolCall(read, req.Cluster, req.Instance, "doops_k8s", map[string]any{"operation": "top", "kind": "pods", "namespace": req.Namespace, "session_id": session})
	if err != nil {
		return nil, monitorErr(502, "monitor_k8s_top_failed")
	}
	return parseMonitorTop(out, req.Namespace, time.Now().UnixMilli())
}

func parseMonitorTop(out, ns string, at int64) ([]monitorInput, error) {
	bad := monitorErr(502, "invalid_monitor_top_evidence")
	text, ok := strings.CutSuffix(out, "\nKubernetes operation complete.")
	if !ok {
		return nil, bad
	}
	rows := []monitorInput{}
	seen := map[string]bool{}
	header := false
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(line, "Warning: ") {
			continue
		}
		f := strings.Fields(line)
		if !header {
			if strings.Join(f, " ") != "NAME CPU(cores) MEMORY(bytes)" {
				if strings.TrimSpace(text) == "No resources found in "+ns+" namespace." {
					return rows, nil
				}
				return nil, bad
			}
			header = true
			continue
		}
		if len(f) != 3 || !inventoryNodeName.MatchString(f[0]) || !monitorID(f[0]) || seen[f[0]] || len(seen) >= 2048 {
			return nil, bad
		}
		seen[f[0]] = true
		for i, key := range []string{"cpu", "memory"} {
			p := monitorInput{monitorAsset: monitorAsset{Type: "app", AssetID: f[0], Namespace: ns}, Metric: key, Unit: monitorUnits[key], monitorPoint: monitorPoint{At: at, Status: "missing"}}
			if raw := f[i+1]; raw != "<unknown>" {
				suffix, factor := "m", 0.001
				if key == "memory" {
					suffix, factor = "Mi", 1048576
				}
				number, ok := strings.CutSuffix(raw, suffix)
				if !ok {
					return nil, bad
				}
				n, err := strconv.ParseUint(number, 10, 53)
				if err != nil {
					return nil, bad
				}
				v := float64(n) * factor
				p.Value = &v
				p.Status = "ok"
			}
			rows = append(rows, p)
		}
	}
	if !header {
		return nil, bad
	}
	return rows, nil
}

type agentNetSample struct {
	DeviceID    string  `json:"device_id"`
	BytesPerSec float64 `json:"bytes_per_sec"`
}

type agentMountSample struct {
	Mount      string  `json:"mount"`
	TotalBytes float64 `json:"total_bytes"`
	UsedBytes  float64 `json:"used_bytes"`
	AvailBytes float64 `json:"avail_bytes"`
}

type agentDiskSample struct {
	DeviceID         string   `json:"device_id"`
	ReadIOPS         *float64 `json:"read_iops"`
	WriteIOPS        *float64 `json:"write_iops"`
	ReadBytesPerSec  *float64 `json:"read_bytes_per_sec"`
	WriteBytesPerSec *float64 `json:"write_bytes_per_sec"`
	ReadLatencyMs    *float64 `json:"read_latency_ms"`
	WriteLatencyMs   *float64 `json:"write_latency_ms"`
}

type agentVMSnapshot struct {
	AtMs            int64              `json:"at_ms"`
	CPUCores        *float64           `json:"cpu_cores,omitempty"`
	KernelCPUCores  *float64           `json:"kernel_cpu_cores,omitempty"`
	MemoryUsedBytes *float64           `json:"memory_used_bytes,omitempty"`
	KernelSlabBytes *float64           `json:"kernel_slab_bytes,omitempty"`
	Networks        []agentNetSample   `json:"networks,omitempty"`
	Mounts          []agentMountSample `json:"mounts,omitempty"`
	Disks           []agentDiskSample  `json:"disks,omitempty"`
}

type agentContainerRow struct {
	Name             string   `json:"name"`
	CPUCores         *float64 `json:"cpu_cores"`
	MemoryBytes      *float64 `json:"memory_bytes"`
	MemoryLimitBytes *float64 `json:"memory_limit_bytes"`
}

type agentContainerSnapshot struct {
	Runtime    string              `json:"runtime"`
	AtMs       int64               `json:"at_ms"`
	Containers []agentContainerRow `json:"containers"`
}

func (h *GatewayHub) resolveHostAssetID(ctx context.Context, req monitorRequest) string {
	if req.AssetID != "" {
		return req.AssetID
	}
	var assetID string
	_ = h.store.db.QueryRowContext(ctx,
		`SELECT asset_id FROM inventory_machines WHERE cluster=? AND instance=? ORDER BY kind='vm' DESC, asset_id LIMIT 1`,
		req.Cluster, req.Instance).Scan(&assetID)
	if monitorID(assetID) {
		return assetID
	}
	return req.Instance
}

func (h *GatewayHub) collectMonitorHost(ctx context.Context, auth TokenAuth, req monitorRequest, session string) ([]monitorInput, error) {
	assetID := h.resolveHostAssetID(ctx, req)
	read, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 30*time.Second)
	defer cancel()
	out, err := h.RunInternalToolCall(read, req.Cluster, req.Instance, "doops_node_info", map[string]any{
		"session_id": session,
		"section":    "vm_metrics",
	})
	if err != nil {
		return nil, monitorErr(502, "monitor_host_collect_failed")
	}
	var snap agentVMSnapshot
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &snap) != nil {
		return nil, monitorErr(502, "invalid_monitor_host_evidence")
	}
	at := time.Now().UnixMilli()
	if snap.AtMs > 0 && snap.AtMs <= at {
		at = snap.AtMs
	}
	machineAsset := monitorAsset{Type: "machine", AssetID: assetID}
	kernelAsset := monitorAsset{Type: "kernel", AssetID: assetID}
	rows := make([]monitorInput, 0, 16)
	add := func(asset monitorAsset, metric, device string, val *float64) {
		unit := monitorUnits[metric]
		if unit == "" || !validMonitorDevice(metric, device) {
			return
		}
		pt := monitorInput{DeviceID: device, monitorAsset: asset, Metric: metric, Unit: unit, monitorPoint: monitorPoint{At: at, Status: "missing"}}
		if val != nil && monitorValidValue(metric, *val) {
			v := *val
			pt.Value = &v
			pt.Status = "ok"
		}
		rows = append(rows, pt)
	}
	add(machineAsset, "cpu", "", snap.CPUCores)
	add(machineAsset, "memory", "", snap.MemoryUsedBytes)
	if snap.KernelCPUCores != nil || snap.KernelSlabBytes != nil {
		add(kernelAsset, "cpu", "", snap.KernelCPUCores)
		add(kernelAsset, "memory", "", snap.KernelSlabBytes)
	}
	for _, netRow := range snap.Networks {
		v := netRow.BytesPerSec
		add(machineAsset, "network", netRow.DeviceID, &v)
	}
	for _, m := range snap.Mounts {
		tot, used, avail := m.TotalBytes, m.UsedBytes, m.AvailBytes
		add(machineAsset, "storage_total", m.Mount, &tot)
		add(machineAsset, "storage", m.Mount, &used)
		add(machineAsset, "storage_available", m.Mount, &avail)
	}
	for _, d := range snap.Disks {
		add(machineAsset, "disk_read_iops", d.DeviceID, d.ReadIOPS)
		add(machineAsset, "disk_write_iops", d.DeviceID, d.WriteIOPS)
		add(machineAsset, "disk_read_bytes_per_second", d.DeviceID, d.ReadBytesPerSec)
		add(machineAsset, "disk_write_bytes_per_second", d.DeviceID, d.WriteBytesPerSec)
		add(machineAsset, "disk_read_latency_ms", d.DeviceID, d.ReadLatencyMs)
		add(machineAsset, "disk_write_latency_ms", d.DeviceID, d.WriteLatencyMs)
	}
	if len(rows) == 0 {
		return nil, monitorErr(502, "invalid_monitor_host_evidence")
	}
	return rows, nil
}

func (h *GatewayHub) collectMonitorContainers(ctx context.Context, auth TokenAuth, req monitorRequest, session string) ([]monitorInput, string, error) {
	read, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 30*time.Second)
	defer cancel()
	out, err := h.RunInternalToolCall(read, req.Cluster, req.Instance, "doops_node_info", map[string]any{
		"session_id": session,
		"section":    "container_metrics",
	})
	if err != nil {
		if strings.Contains(err.Error(), "container_runtime_unavailable") {
			return nil, "", monitorErr(503, "container_runtime_unavailable")
		}
		return nil, "", monitorErr(502, "monitor_container_collect_failed")
	}
	var snap agentContainerSnapshot
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &snap) != nil || snap.Runtime == "" {
		return nil, "", monitorErr(502, "invalid_monitor_container_evidence")
	}
	at := time.Now().UnixMilli()
	if snap.AtMs > 0 && snap.AtMs <= at {
		at = snap.AtMs
	}
	rows := make([]monitorInput, 0, len(snap.Containers)*3)
	seen := make(map[string]bool, len(snap.Containers))
	for _, c := range snap.Containers {
		if !monitorID(c.Name) || seen[c.Name] || len(seen) >= 1024 {
			continue
		}
		seen[c.Name] = true
		asset := monitorAsset{Type: "container", AssetID: c.Name}
		for _, item := range []struct {
			metric string
			val    *float64
		}{
			{"cpu", c.CPUCores},
			{"memory", c.MemoryBytes},
			{"memory_limit", c.MemoryLimitBytes},
		} {
			pt := monitorInput{
				monitorAsset: asset,
				Metric:       item.metric,
				Unit:         monitorUnits[item.metric],
				monitorPoint: monitorPoint{At: at, Status: "missing"},
			}
			if item.val != nil && monitorValidValue(item.metric, *item.val) {
				v := *item.val
				pt.Value = &v
				pt.Status = "ok"
			}
			rows = append(rows, pt)
		}
	}
	return rows, snap.Runtime, nil
}

// ---------------- Agent-side VM & Container Read-Only Probes ----------------

type linuxCPUTicks struct {
	total, idle, kernel uint64
}

func readLinuxCPUTicks() (linuxCPUTicks, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return linuxCPUTicks{}, false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return linuxCPUTicks{}, false
	}
	vals := make([]uint64, len(fields)-1)
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return linuxCPUTicks{}, false
		}
		vals[i] = v
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3] + vals[4]
	kernel := vals[2] + vals[5] + vals[6]
	return linuxCPUTicks{total: total, idle: idle, kernel: kernel}, true
}

func readLinuxMeminfo() (usedBytes *float64, slabBytes *float64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, nil
	}
	kv := make(map[string]uint64)
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			key := strings.TrimSuffix(f[0], ":")
			if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				kv[key] = v * 1024
			}
		}
	}
	if total, ok := kv["MemTotal"]; ok {
		if avail, okA := kv["MemAvailable"]; okA && total >= avail {
			u := float64(total - avail)
			usedBytes = &u
		}
	}
	if slab, ok := kv["Slab"]; ok {
		s := float64(slab)
		slabBytes = &s
	}
	return usedBytes, slabBytes
}

func isIgnoredNIC(name string) bool {
	if name == "lo" || !monitorInterface.MatchString(name) {
		return true
	}
	for _, prefix := range []string{"veth", "docker", "cni", "flannel", "cali", "br-", "virbr", "kube-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readLinuxNetBytes() map[string]uint64 {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	out := make(map[string]uint64)
	for _, e := range entries {
		name := e.Name()
		if isIgnoredNIC(name) || len(out) >= 16 {
			continue
		}
		rxRaw, errRx := os.ReadFile(filepath.Join("/sys/class/net", name, "statistics/rx_bytes"))
		txRaw, errTx := os.ReadFile(filepath.Join("/sys/class/net", name, "statistics/tx_bytes"))
		if errRx != nil || errTx != nil {
			continue
		}
		rx, err1 := strconv.ParseUint(strings.TrimSpace(string(rxRaw)), 10, 64)
		tx, err2 := strconv.ParseUint(strings.TrimSpace(string(txRaw)), 10, 64)
		if err1 == nil && err2 == nil {
			out[name] = rx + tx
		}
	}
	return out
}

type linuxDiskCounter struct {
	majorMinor string
	reads      uint64
	writes     uint64
	readSec    uint64
	writeSec   uint64
	readMs     uint64
	writeMs    uint64
}

func isWholePhysicalDisk(name string) bool {
	if !monitorDisk.MatchString(name) {
		return false
	}
	for _, prefix := range []string{"loop", "ram", "sr", "fd", "dm-", "md", "zram"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	_, err := os.Stat(filepath.Join("/sys/block", name))
	return err == nil
}

func readLinuxDiskCounters() map[string]linuxDiskCounter {
	raw, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil
	}
	out := make(map[string]linuxDiskCounter)
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 11 {
			continue
		}
		name := f[2]
		if !isWholePhysicalDisk(name) || len(out) >= 16 {
			continue
		}
		nums := make([]uint64, 6)
		indices := []int{3, 7, 5, 9, 6, 10}
		valid := true
		for i, idx := range indices {
			v, err := strconv.ParseUint(f[idx], 10, 64)
			if err != nil {
				valid = false
				break
			}
			nums[i] = v
		}
		if valid {
			out[name] = linuxDiskCounter{
				majorMinor: f[0] + ":" + f[1],
				reads:      nums[0],
				writes:     nums[1],
				readSec:    nums[2],
				writeSec:   nums[3],
				readMs:     nums[4],
				writeMs:    nums[5],
			}
		}
	}
	return out
}

var ignoredMountTypes = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true,
	"nsfs": true, "autofs": true, "mqueue": true, "hugetlbfs": true,
	"debugfs": true, "tracefs": true, "fusectl": true, "configfs": true,
	"securityfs": true, "pstore": true, "bpf": true, "rpc_pipefs": true,
	"devpts": true, "binfmt_misc": true,
}

func readHostMounts() []agentMountSample {
	candidates := []string{"/"}
	if raw, err := os.ReadFile("/proc/self/mounts"); err == nil {
		seen := map[string]bool{"/": true}
		seenDev := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || ignoredMountTypes[f[2]] {
				continue
			}
			dev := f[0]
			mp := strings.ReplaceAll(f[1], `\040`, " ")
			clean := path.Clean(mp)
			if clean == "/" {
				if dev != "" {
					seenDev[dev] = true
				}
				continue
			}
			if !path.IsAbs(clean) || clean != mp || seen[clean] || len(candidates) >= 16 {
				continue
			}
			if strings.HasPrefix(clean, "/proc/") || strings.HasPrefix(clean, "/sys/") || strings.HasPrefix(clean, "/run/") || strings.HasPrefix(clean, "/dev/") {
				continue
			}
			// Skip single-file container bind mounts (e.g. /etc/hosts, /root/.kube/config).
			if fi, statErr := os.Stat(clean); statErr != nil || !fi.IsDir() {
				continue
			}
			// Avoid double-counting the same backing block/network device mounted at multiple paths.
			if strings.HasPrefix(dev, "/") && seenDev[dev] {
				continue
			}
			if strings.HasPrefix(dev, "/") {
				seenDev[dev] = true
			}
			seen[clean] = true
			candidates = append(candidates, clean)
		}
	}
	sort.Strings(candidates)
	out := make([]agentMountSample, 0, len(candidates))
	for _, mp := range candidates {
		var st syscall.Statfs_t
		if err := syscall.Statfs(mp, &st); err != nil || st.Blocks == 0 || st.Bsize <= 0 {
			continue
		}
		bsize := statfsBlockSize(st)
		if bsize <= 0 {
			continue
		}
		total := float64(st.Blocks) * bsize
		used := float64(st.Blocks-st.Bfree) * bsize
		avail := float64(st.Bavail) * bsize
		out = append(out, agentMountSample{
			Mount:      mp,
			TotalBytes: total,
			UsedBytes:  used,
			AvailBytes: avail,
		})
	}
	return out
}

func collectAgentVMMetricsJSON(ctx context.Context) (string, error) {
	snap := agentVMSnapshot{Mounts: readHostMounts()}

	if runtime.GOOS == "linux" {
		cpu0, okCPU0 := readLinuxCPUTicks()
		net0 := readLinuxNetBytes()
		disk0 := readLinuxDiskCounters()
		t0 := time.Now()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}

		elapsed := time.Since(t0).Seconds()
		if elapsed > 0 {
			if cpu1, okCPU1 := readLinuxCPUTicks(); okCPU0 && okCPU1 && cpu1.total > cpu0.total && cpu1.idle >= cpu0.idle {
				dtTotal := float64(cpu1.total - cpu0.total)
				dtBusy := dtTotal - float64(cpu1.idle-cpu0.idle)
				cores := (dtBusy / dtTotal) * float64(runtime.NumCPU())
				if cores >= 0 {
					snap.CPUCores = &cores
				}
				if cpu1.kernel >= cpu0.kernel {
					kCores := (float64(cpu1.kernel-cpu0.kernel) / dtTotal) * float64(runtime.NumCPU())
					snap.KernelCPUCores = &kCores
				}
			}
			net1 := readLinuxNetBytes()
			var netNames []string
			for name := range net1 {
				if _, ok := net0[name]; ok {
					netNames = append(netNames, name)
				}
			}
			sort.Strings(netNames)
			for _, name := range netNames {
				if net1[name] >= net0[name] {
					snap.Networks = append(snap.Networks, agentNetSample{
						DeviceID:    name,
						BytesPerSec: float64(net1[name]-net0[name]) / elapsed,
					})
				}
			}
			disk1 := readLinuxDiskCounters()
			var diskNames []string
			for name := range disk1 {
				diskNames = append(diskNames, name)
			}
			sort.Strings(diskNames)
			for _, name := range diskNames {
				b, ok := disk0[name]
				a := disk1[name]
				row := agentDiskSample{DeviceID: name}
				if ok && b.majorMinor == a.majorMinor && a.reads >= b.reads && a.writes >= b.writes &&
					a.readSec >= b.readSec && a.writeSec >= b.writeSec && a.readMs >= b.readMs && a.writeMs >= b.writeMs {
					dR := float64(a.reads - b.reads)
					dW := float64(a.writes - b.writes)
					rIOPS := dR / elapsed
					wIOPS := dW / elapsed
					rBPS := float64(a.readSec-b.readSec) * 512 / elapsed
					wBPS := float64(a.writeSec-b.writeSec) * 512 / elapsed
					row.ReadIOPS = &rIOPS
					row.WriteIOPS = &wIOPS
					row.ReadBytesPerSec = &rBPS
					row.WriteBytesPerSec = &wBPS
					if dR > 0 {
						rLat := float64(a.readMs-b.readMs) / dR
						row.ReadLatencyMs = &rLat
					}
					if dW > 0 {
						wLat := float64(a.writeMs-b.writeMs) / dW
						row.WriteLatencyMs = &wLat
					}
				}
				snap.Disks = append(snap.Disks, row)
			}
		}
		snap.MemoryUsedBytes, snap.KernelSlabBytes = readLinuxMeminfo()
	} else {
		// Portable fallback for non-Linux development hosts (e.g. macOS).
		if out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "%cpu=").Output(); err == nil {
			var sumPct float64
			for _, f := range strings.Fields(string(out)) {
				if v, err := strconv.ParseFloat(strings.ReplaceAll(f, ",", "."), 64); err == nil && v >= 0 {
					sumPct += v
				}
			}
			cores := sumPct / 100.0
			snap.CPUCores = &cores
		}
		if out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "rss=").Output(); err == nil {
			var sumKB uint64
			for _, f := range strings.Fields(string(out)) {
				if v, err := strconv.ParseUint(f, 10, 64); err == nil {
					sumKB += v
				}
			}
			if sumKB > 0 {
				mem := float64(sumKB * 1024)
				snap.MemoryUsedBytes = &mem
			}
		}
	}

	snap.AtMs = time.Now().UnixMilli()
	encoded, err := json.Marshal(snap)
	return string(encoded), err
}

func parseContainerByteQuantity(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "--" || s == "N/A" {
		return 0, false
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"TiB", 1099511627776}, {"TB", 1e12},
		{"GiB", 1073741824}, {"GB", 1e9},
		{"MiB", 1048576}, {"MB", 1e6},
		{"KiB", 1024}, {"kB", 1e3}, {"KB", 1e3},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			v, err := strconv.ParseFloat(numStr, 64)
			if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, false
			}
			return v * u.factor, true
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func parseContainerStatsOutput(runtimeName, raw string) []agentContainerRow {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []agentContainerRow{}
	}
	var records []map[string]interface{}
	if strings.HasPrefix(trimmed, "[") {
		_ = json.Unmarshal([]byte(trimmed), &records)
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var item map[string]interface{}
			if json.Unmarshal([]byte(line), &item) == nil {
				records = append(records, item)
			}
		}
	}
	rows := make([]agentContainerRow, 0, len(records))
	for _, rec := range records {
		name := strings.TrimPrefix(strings.TrimSpace(firstMapString(rec, "Name", "name", "Names")), "/")
		if !monitorID(name) {
			id := strings.TrimSpace(firstMapString(rec, "ID", "id", "Container"))
			if len(id) > 12 {
				id = id[:12]
			}
			if !monitorID(id) {
				continue
			}
			name = id
		}
		row := agentContainerRow{Name: name}
		cpuRaw := strings.TrimSuffix(strings.TrimSpace(firstMapString(rec, "CPUPerc", "CPU", "cpu_percent")), "%")
		if v, err := strconv.ParseFloat(cpuRaw, 64); err == nil && v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			cores := v / 100.0
			row.CPUCores = &cores
		}
		memRaw := firstMapString(rec, "MemUsage", "mem_usage")
		if usedPart, limitPart, ok := strings.Cut(memRaw, "/"); ok {
			if used, okU := parseContainerByteQuantity(usedPart); okU {
				row.MemoryBytes = &used
			}
			if limit, okL := parseContainerByteQuantity(limitPart); okL && limit > 0 {
				row.MemoryLimitBytes = &limit
			}
		} else if used, okU := parseContainerByteQuantity(memRaw); okU {
			row.MemoryBytes = &used
		}
		rows = append(rows, row)
	}
	_ = runtimeName
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func firstMapString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch typed := v.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return typed
				}
			default:
				s := fmt.Sprintf("%v", typed)
				if strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
	}
	return ""
}

func collectAgentContainerMetricsJSON(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, candidate := range []string{"docker", "nerdctl", "podman"} {
		bin, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		out, runErr := exec.CommandContext(ctx, bin, "stats", "--no-stream", "--no-trunc", "--format", "{{json .}}").Output()
		if runErr != nil {
			continue
		}
		snap := agentContainerSnapshot{
			Runtime:    candidate,
			AtMs:       time.Now().UnixMilli(),
			Containers: parseContainerStatsOutput(candidate, string(out)),
		}
		encoded, err := json.Marshal(snap)
		return string(encoded), err
	}
	return "", errors.New("container_runtime_unavailable")
}

type agentAppProbeRow struct {
	AssetID           string   `json:"asset_id"`
	ResponseTimeMs    *float64 `json:"response_time_ms,omitempty"`
	ErrorRate         *float64 `json:"error_rate,omitempty"`
	ActiveConnections *float64 `json:"active_connections,omitempty"`
	RequestsPerSec    *float64 `json:"requests_per_sec,omitempty"`
}

type agentAppSnapshot struct {
	AtMs int64              `json:"at_ms"`
	Apps []agentAppProbeRow `json:"apps"`
}

var nginxStubStatusPattern = regexp.MustCompile(`^\s*Active connections:\s*(\d+)\s+server accepts handled requests\s+\d+\s+\d+\s+(\d+)\s+Reading:\s*\d+\s+Writing:\s*\d+\s+Waiting:\s*\d+\s*$`)

func (h *GatewayHub) collectMonitorApps(ctx context.Context, auth TokenAuth, req monitorRequest, session string) ([]monitorInput, error) {
	targets := []api.AppProbeTarget{}
	rows, err := h.store.db.QueryContext(ctx,
		`SELECT name, details FROM inventory_resource_catalog WHERE kind='website' AND cluster=? AND instance=? ORDER BY id LIMIT 32`,
		req.Cluster, req.Instance)
	if err == nil {
		for rows.Next() {
			var name string
			var rawDetails []byte
			if rows.Scan(&name, &rawDetails) != nil {
				continue
			}
			var d map[string]string
			if json.Unmarshal(rawDetails, &d) != nil {
				continue
			}
			scheme, domain, p := d["scheme"], d["domain"], d["path"]
			if (scheme != "http" && scheme != "https") || !catalogHost(domain) || !strings.HasPrefix(p, "/") {
				continue
			}
			assetID := domain
			if !monitorID(assetID) {
				if monitorID(name) {
					assetID = name
				} else {
					continue
				}
			}
			targets = append(targets, api.AppProbeTarget{AssetID: assetID, URL: scheme + "://" + domain + p})
		}
		rows.Close()
	}

	read, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 30*time.Second)
	defer cancel()
	out, callErr := h.RunInternalToolCall(read, req.Cluster, req.Instance, "doops_node_info", map[string]any{
		"session_id": session,
		"section":    "app_metrics",
		"targets":    targets,
	})
	if callErr != nil {
		return nil, monitorErr(502, "monitor_app_collect_failed")
	}
	var snap agentAppSnapshot
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &snap) != nil {
		return nil, monitorErr(502, "invalid_monitor_app_evidence")
	}
	at := time.Now().UnixMilli()
	if snap.AtMs > 0 && snap.AtMs <= at {
		at = snap.AtMs
	}
	inputs := make([]monitorInput, 0, len(snap.Apps)*3)
	seen := make(map[string]bool)
	for _, app := range snap.Apps {
		if !monitorID(app.AssetID) || seen[app.AssetID] || len(seen) >= 64 {
			continue
		}
		seen[app.AssetID] = true
		asset := monitorAsset{Type: "app", AssetID: app.AssetID}
		add := func(metric string, val *float64) {
			pt := monitorInput{
				monitorAsset: asset,
				Metric:       metric,
				Unit:         monitorUnits[metric],
				monitorPoint: monitorPoint{At: at, Status: "missing"},
			}
			if val != nil && monitorValidValue(metric, *val) {
				v := *val
				pt.Value = &v
				pt.Status = "ok"
			}
			inputs = append(inputs, pt)
		}
		if app.ResponseTimeMs != nil || app.ErrorRate != nil {
			add("response_time_ms", app.ResponseTimeMs)
			add("error_rate", app.ErrorRate)
		}
		if app.ActiveConnections != nil {
			add("active_connections", app.ActiveConnections)
		}
		if app.RequestsPerSec != nil {
			add("requests", app.RequestsPerSec)
		}
	}
	return inputs, nil
}

func isDisallowedProbeIP(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func readLocalNginxStubStatus(ctx context.Context, client *http.Client) (active float64, totalReqs uint64, ok bool) {
	for _, stubURL := range []string{"http://127.0.0.1/nginx_status", "http://127.0.0.1:8080/nginx_status", "http://127.0.0.1/stub_status"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, stubURL, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		m := nginxStubStatusPattern.FindSubmatch(body)
		if m == nil {
			continue
		}
		act, err1 := strconv.ParseFloat(string(m[1]), 64)
		reqs, err2 := strconv.ParseUint(string(m[2]), 10, 64)
		if err1 == nil && err2 == nil {
			return act, reqs, true
		}
	}
	return 0, 0, false
}

func collectAgentAppMetricsJSON(ctx context.Context, targets []api.AppProbeTarget) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	dialer := &net.Dialer{Timeout: 4 * time.Second}
	safeClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext: func(dCtx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(dCtx, host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("lookup failed")
				}
				for _, ipAddr := range ips {
					if isDisallowedProbeIP(ipAddr.IP) {
						return nil, fmt.Errorf("disallowed probe address")
					}
				}
				return dialer.DialContext(dCtx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
	}

	snap := agentAppSnapshot{Apps: []agentAppProbeRow{}}

	// Check local loopback nginx stub_status if available.
	loopClient := &http.Client{Timeout: 1500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if act0, req0, ok0 := readLocalNginxStubStatus(ctx, loopClient); ok0 {
		t0 := time.Now()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		dt := time.Since(t0).Seconds()
		act1, req1, ok1 := readLocalNginxStubStatus(ctx, loopClient)
		row := agentAppProbeRow{AssetID: "nginx"}
		if ok1 {
			row.ActiveConnections = &act1
			if dt > 0 && req1 >= req0 {
				rps := float64(req1-req0) / dt
				row.RequestsPerSec = &rps
			}
		} else {
			row.ActiveConnections = &act0
		}
		snap.Apps = append(snap.Apps, row)
	}

	for i, target := range targets {
		if i >= 32 || !monitorID(target.AssetID) {
			continue
		}
		parsed, err := url.Parse(target.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		row := agentAppProbeRow{AssetID: target.AssetID}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			continue
		}
		t0 := time.Now()
		resp, doErr := safeClient.Do(req)
		elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0
		if doErr != nil {
			errPct := 100.0
			row.ErrorRate = &errPct
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			row.ResponseTimeMs = &elapsedMs
			errPct := 0.0
			if resp.StatusCode >= 400 {
				errPct = 100.0
			}
			row.ErrorRate = &errPct
		}
		snap.Apps = append(snap.Apps, row)
	}

	snap.AtMs = time.Now().UnixMilli()
	encoded, err := json.Marshal(snap)
	return string(encoded), err
}
