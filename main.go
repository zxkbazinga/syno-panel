package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var webFiles embed.FS

var version = "dev"

const (
	hostMetricsInterval      = time.Second
	containerRefreshInterval = 5 * time.Second
)

type Snapshot struct {
	Timestamp  time.Time          `json:"timestamp"`
	Hostname   string             `json:"hostname"`
	CPU        Metric             `json:"cpu"`
	Memory     Memory             `json:"memory"`
	Disks      []Disk             `json:"disks"`
	Network    []NetworkInterface `json:"network"`
	Containers []Container        `json:"containers"`
	Warnings   []string           `json:"warnings"`
}

type Metric struct {
	UsedPercent float64 `json:"usedPercent"`
}

type Memory struct {
	UsedBytes   uint64  `json:"usedBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

type Disk struct {
	Name        string  `json:"name"`
	Mount       string  `json:"mount"`
	FileSystem  string  `json:"fileSystem"`
	UsedBytes   uint64  `json:"usedBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

type NetworkInterface struct {
	Name                  string  `json:"name"`
	ReceiveBytesPerSecond float64 `json:"receiveBytesPerSecond"`
	SendBytesPerSecond    float64 `json:"sendBytesPerSecond"`
}

type Container struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Image         string  `json:"image"`
	Status        string  `json:"status"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	MemoryLimit   uint64  `json:"memoryLimit"`
	MemoryPercent float64 `json:"memoryPercent"`
	NetworkBytes  uint64  `json:"networkBytes"`
	BlockBytes    uint64  `json:"blockBytes"`
}

type Collector struct {
	docker            *http.Client
	hostRoot          string
	hostProc          string
	previousCPU       uint64
	previousIdle      uint64
	hasCPU            bool
	previousNetwork   map[string]networkCounters
	previousNetworkAt time.Time
}

type hub struct {
	mu            sync.Mutex
	latest        Snapshot
	hostWarnings  []string
	dockerWarning string
	subscribers   map[chan Snapshot]struct{}
}

type mountInfo struct {
	device string
	mount  string
	fsType string
}

type networkCounters struct {
	received uint64
	sent     uint64
}

type dockerContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			Cache        uint64 `json:"cache"`
			InactiveFile uint64 `json:"inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServicedRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

func main() {
	socket := envOr("DOCKER_SOCKET", "/var/run/docker.sock")
	hostRoot := envOr("HOST_ROOT", "/host")
	hostProc := envOr("HOST_PROC", "/host/proc")
	port := envOr("PORT", "5666")

	dockerTransport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		},
	}
	collector := &Collector{
		docker:   &http.Client{Transport: dockerTransport},
		hostRoot: hostRoot,
		hostProc: hostProc,
	}
	stream := &hub{subscribers: make(map[chan Snapshot]struct{})}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go collector.run(ctx, stream)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", stream.serveSSE)
	mux.HandleFunc("/api/version", serveVersion)
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("Syno Panel listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func serveVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprint(w, version); err != nil {
		log.Printf("write app version: %v", err)
	}
}

func (c *Collector) run(ctx context.Context, stream *hub) {
	go c.runContainerMonitor(ctx, stream)
	ticker := time.NewTicker(hostMetricsInterval)
	defer ticker.Stop()
	for {
		snapshot := c.collect()
		stream.publish(snapshot)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Collector) collect() Snapshot {
	now := time.Now()
	hostnameData, _ := os.ReadFile(c.hostRoot + "/etc/hostname")
	host := strings.TrimSpace(string(hostnameData))
	if host == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "Synology NAS"
	}
	snapshot := Snapshot{
		Timestamp:  now,
		Hostname:   host,
		Disks:      []Disk{},
		Network:    []NetworkInterface{},
		Containers: []Container{},
		Warnings:   []string{},
	}
	if cpu, err := c.readCPU(); err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "无法读取 NAS CPU 信息: "+err.Error())
	} else {
		snapshot.CPU = cpu
	}
	if memory, err := readMemory(c.hostProc + "/meminfo"); err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "无法读取 NAS 内存信息: "+err.Error())
	} else {
		snapshot.Memory = memory
	}
	disks, diskWarnings := c.readDisks()
	snapshot.Disks = disks
	snapshot.Warnings = append(snapshot.Warnings, diskWarnings...)
	network, err := c.readNetwork()
	if err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "无法读取 NAS 网络信息: "+err.Error())
	} else {
		snapshot.Network = network
	}
	return snapshot
}

func (c *Collector) readCPU() (Metric, error) {
	data, err := os.ReadFile(c.hostProc + "/stat")
	if err != nil {
		return Metric{}, err
	}
	total, idle, err := parseCPUStat(string(data))
	if err != nil {
		return Metric{}, err
	}
	if !c.hasCPU {
		c.previousCPU, c.previousIdle, c.hasCPU = total, idle, true
		return Metric{}, nil
	}
	totalDelta, idleDelta := total-c.previousCPU, idle-c.previousIdle
	c.previousCPU, c.previousIdle = total, idle
	if totalDelta == 0 {
		return Metric{}, nil
	}
	used := 100 * float64(totalDelta-idleDelta) / float64(totalDelta)
	return Metric{UsedPercent: clampPercent(used)}, nil
}

func parseCPUStat(data string) (total, idle uint64, err error) {
	for _, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return 0, 0, errors.New("invalid cpu stat line")
		}
		values := make([]uint64, 0, len(fields))
		for _, field := range fields {
			value, parseErr := strconv.ParseUint(field, 10, 64)
			if parseErr != nil {
				return 0, 0, parseErr
			}
			values = append(values, value)
			total += value
		}
		idle = values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		return total, idle, nil
	}
	return 0, 0, errors.New("cpu line not found")
}

func readMemory(filename string) (Memory, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Memory{}, err
	}
	values := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr == nil {
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	total := values["MemTotal"]
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if total == 0 {
		return Memory{}, errors.New("MemTotal not found")
	}
	if available > total {
		available = total
	}
	used := total - available
	return Memory{UsedBytes: used, TotalBytes: total, UsedPercent: clampPercent(100 * float64(used) / float64(total))}, nil
}

func (c *Collector) readDisks() ([]Disk, []string) {
	data, err := os.ReadFile(c.hostProc + "/mounts")
	if err != nil {
		return []Disk{}, []string{"无法读取 NAS 磁盘挂载信息: " + err.Error()}
	}
	mounts := parseMountsForRoot(string(data), c.hostRoot)
	disks := make([]Disk, 0, len(mounts))
	var warnings []string
	for _, mount := range mounts {
		path := strings.TrimRight(c.hostRoot, "/") + mount.mount
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			warnings = append(warnings, fmt.Sprintf("无法读取存储空间 %s: %v", mount.mount, err))
			continue
		}
		blockSize := uint64(stat.Bsize)
		total := stat.Blocks * blockSize
		free := stat.Bfree * blockSize
		if total == 0 {
			continue
		}
		used := total - free
		disks = append(disks, Disk{
			Name:        mount.mount,
			Mount:       mount.mount,
			FileSystem:  mount.fsType,
			UsedBytes:   used,
			TotalBytes:  total,
			UsedPercent: clampPercent(100 * float64(used) / float64(total)),
		})
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Mount < disks[j].Mount })
	return disks, warnings
}

func (c *Collector) readNetwork() ([]NetworkInterface, error) {
	data, err := os.ReadFile(c.hostProc + "/net/dev")
	if err != nil {
		return nil, err
	}
	counters, err := parseNetworkStats(string(data))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var elapsed time.Duration
	if !c.previousNetworkAt.IsZero() {
		elapsed = now.Sub(c.previousNetworkAt)
	}
	interfaces := calculateNetworkRates(counters, c.previousNetwork, elapsed)
	c.previousNetwork = counters
	c.previousNetworkAt = now
	return interfaces, nil
}

func parseNetworkStats(data string) (map[string]networkCounters, error) {
	counters := make(map[string]networkCounters)
	for _, line := range strings.Split(data, "\n") {
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if name == "" || isIgnoredNetworkInterface(name) {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			return nil, fmt.Errorf("网卡 %s 的统计数据格式无效", name)
		}
		received, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("解析网卡 %s 的接收字节数: %w", name, err)
		}
		sent, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("解析网卡 %s 的发送字节数: %w", name, err)
		}
		counters[name] = networkCounters{received: received, sent: sent}
	}
	return counters, nil
}

func calculateNetworkRates(current, previous map[string]networkCounters, elapsed time.Duration) []NetworkInterface {
	interfaces := make([]NetworkInterface, 0, len(current))
	for name, counters := range current {
		item := NetworkInterface{Name: name}
		if old, exists := previous[name]; elapsed > 0 && exists {
			item.ReceiveBytesPerSecond = float64(counterDelta(counters.received, old.received)) / elapsed.Seconds()
			item.SendBytesPerSecond = float64(counterDelta(counters.sent, old.sent)) / elapsed.Seconds()
		}
		interfaces = append(interfaces, item)
	}
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Name < interfaces[j].Name })
	return interfaces
}

func counterDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func isIgnoredNetworkInterface(name string) bool {
	for _, prefix := range []string{"docker", "veth", "br-", "virbr", "tun", "tap", "wg", "tailscale", "ifb"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return name == "lo"
}

func parseMounts(data string) []mountInfo {
	return parseMountsForRoot(data, "")
}

func parseMountsForRoot(data, hostRoot string) []mountInfo {
	allowed := map[string]bool{"ext4": true, "btrfs": true, "xfs": true, "vfat": true, "exfat": true, "ntfs": true, "fuseblk": true}
	seen := make(map[string]bool)
	var mounts []mountInfo
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !allowed[fields[2]] {
			continue
		}
		mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`).Replace(fields[1])
		root := strings.TrimRight(hostRoot, "/")
		if root != "" && strings.HasPrefix(mount, root+"/") {
			mount = strings.TrimPrefix(mount, root)
		}
		if !isNASVolumeMount(mount) {
			continue
		}
		if seen[mount] {
			continue
		}
		seen[mount] = true
		mounts = append(mounts, mountInfo{device: fields[0], mount: mount, fsType: fields[2]})
	}
	return mounts
}

func isNASVolumeMount(mount string) bool {
	if !strings.HasPrefix(mount, "/volume") {
		return false
	}
	parts := strings.Split(strings.Trim(mount, "/"), "/")
	if len(parts) == 1 {
		name := strings.TrimPrefix(parts[0], "volume")
		if name == "" {
			return false
		}
		for _, char := range name {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	}
	return len(parts) == 2 &&
		strings.HasPrefix(parts[0], "volumeUSB") &&
		strings.HasPrefix(parts[1], "usbshare")
}

func (c *Collector) runContainerMonitor(ctx context.Context, stream *hub) {
	active := make(map[string]context.CancelFunc)
	refresh := func() {
		listed, err := c.listContainers(ctx)
		if err != nil {
			stream.setDockerWarning("无法读取 Docker 容器: " + err.Error())
			log.Printf("list Docker containers: %v", err)
			return
		}
		stream.setDockerWarning("")

		running := make(map[string]struct{}, len(listed))
		containers := make([]Container, 0, len(listed))
		for _, item := range listed {
			if item.State != "running" {
				continue
			}
			running[item.ID] = struct{}{}
			containers = append(containers, containerFromList(item))
			if _, exists := active[item.ID]; !exists {
				containerCtx, cancel := context.WithCancel(ctx)
				active[item.ID] = cancel
				go c.streamContainerStats(containerCtx, item, stream)
			}
		}
		for id, cancel := range active {
			if _, exists := running[id]; !exists {
				cancel()
				delete(active, id)
			}
		}
		stream.setContainers(containers)
	}

	refresh()
	ticker := time.NewTicker(containerRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range active {
				cancel()
			}
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func (c *Collector) listContainers(ctx context.Context) ([]dockerContainer, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://docker/v1.41/containers/json", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.docker.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, fmt.Errorf("Docker API 返回 %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var containers []dockerContainer
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		return nil, err
	}
	return containers, nil
}

func containerFromList(item dockerContainer) Container {
	name := strings.TrimPrefix(first(item.Names), "/")
	if name == "" {
		name = item.ID[:min(12, len(item.ID))]
	}
	return Container{
		ID:     item.ID[:min(12, len(item.ID))],
		Name:   name,
		Image:  item.Image,
		Status: item.Status,
	}
}

func (c *Collector) streamContainerStats(ctx context.Context, item dockerContainer, stream *hub) {
	container := containerFromList(item)
	var previousCPU float64
	hasPreviousCPU := false
	for {
		if ctx.Err() != nil {
			return
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://docker/v1.41/containers/"+item.ID+"/stats?stream=true", nil)
		if err == nil {
			var response *http.Response
			response, err = c.docker.Do(request)
			if err == nil {
				if response.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
					err = fmt.Errorf("Docker stats API 返回 %s: %s", response.Status, strings.TrimSpace(string(body)))
					_ = response.Body.Close()
				} else {
					decoder := json.NewDecoder(response.Body)
					for {
						var stats dockerStats
						if decodeErr := decoder.Decode(&stats); decodeErr != nil {
							err = decodeErr
							break
						}
						updated := containerWithStats(container, item.Status, stats)
						if currentCPU, ok := dockerCPUPercent(stats); ok {
							if hasPreviousCPU {
								currentCPU = previousCPU*0.65 + currentCPU*0.35
							}
							updated.CPUPercent = currentCPU
							previousCPU, hasPreviousCPU = currentCPU, true
						}
						stream.updateContainer(updated)
					}
					_ = response.Body.Close()
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		failed := container
		failed.Status = "资源读取失败"
		stream.updateContainer(failed)
		log.Printf("stream Docker stats for %s: %v", container.Name, err)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func containerWithStats(container Container, status string, stats dockerStats) Container {
	container.Status = status
	if cpuPercent, ok := dockerCPUPercent(stats); ok {
		container.CPUPercent = cpuPercent
	}
	container.MemoryBytes = stats.MemoryStats.Usage
	if stats.MemoryStats.Stats.InactiveFile > 0 {
		container.MemoryBytes = subtractFloor(container.MemoryBytes, stats.MemoryStats.Stats.InactiveFile)
	} else if stats.MemoryStats.Stats.Cache > 0 {
		container.MemoryBytes = subtractFloor(container.MemoryBytes, stats.MemoryStats.Stats.Cache)
	}
	container.MemoryLimit = stats.MemoryStats.Limit
	if container.MemoryLimit > 0 {
		container.MemoryPercent = clampPercent(100 * float64(container.MemoryBytes) / float64(container.MemoryLimit))
	}
	container.NetworkBytes = 0
	for _, network := range stats.Networks {
		container.NetworkBytes += network.RxBytes + network.TxBytes
	}
	container.BlockBytes = 0
	for _, ioStat := range stats.BlkioStats.IOServicedRecursive {
		if strings.EqualFold(ioStat.Op, "read") || strings.EqualFold(ioStat.Op, "write") {
			container.BlockBytes += ioStat.Value
		}
	}
	return container
}

func dockerCPUPercent(stats dockerStats) (float64, bool) {
	cpuDelta := counterDelta(stats.CPUStats.CPUUsage.TotalUsage, stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := counterDelta(stats.CPUStats.SystemUsage, stats.PreCPUStats.SystemUsage)
	cpuCount := uint64(stats.CPUStats.OnlineCPUs)
	if cpuCount == 0 {
		cpuCount = uint64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if systemDelta == 0 || cpuCount == 0 {
		return 0, false
	}
	return 100 * float64(cpuDelta) / float64(systemDelta) * float64(cpuCount), true
}

func (h *hub) publish(snapshot Snapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot.Containers = h.latest.Containers
	h.hostWarnings = append([]string(nil), snapshot.Warnings...)
	h.latest = snapshot
	h.applyWarningsLocked()
	h.broadcastLocked()
}

func (h *hub) setDockerWarning(warning string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dockerWarning == warning {
		return
	}
	h.dockerWarning = warning
	h.latest.Timestamp = time.Now()
	h.applyWarningsLocked()
	h.broadcastLocked()
}

func (h *hub) setContainers(containers []Container) {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := make(map[string]Container, len(h.latest.Containers))
	for _, container := range h.latest.Containers {
		previous[container.ID] = container
	}
	for index, container := range containers {
		if old, exists := previous[container.ID]; exists {
			container.CPUPercent = old.CPUPercent
			container.MemoryBytes = old.MemoryBytes
			container.MemoryLimit = old.MemoryLimit
			container.MemoryPercent = old.MemoryPercent
			container.NetworkBytes = old.NetworkBytes
			container.BlockBytes = old.BlockBytes
		}
		containers[index] = container
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	h.latest.Containers = containers
	h.latest.Timestamp = time.Now()
	h.broadcastLocked()
}

func (h *hub) updateContainer(container Container) {
	h.mu.Lock()
	defer h.mu.Unlock()
	found := false
	for index := range h.latest.Containers {
		if h.latest.Containers[index].ID == container.ID {
			h.latest.Containers[index] = container
			found = true
			break
		}
	}
	if !found {
		h.latest.Containers = append(h.latest.Containers, container)
	}
	sort.Slice(h.latest.Containers, func(i, j int) bool {
		return h.latest.Containers[i].Name < h.latest.Containers[j].Name
	})
	h.latest.Timestamp = time.Now()
	h.broadcastLocked()
}

func (h *hub) applyWarningsLocked() {
	h.latest.Warnings = append([]string(nil), h.hostWarnings...)
	if h.dockerWarning != "" {
		h.latest.Warnings = append(h.latest.Warnings, h.dockerWarning)
	}
}

func (h *hub) broadcastLocked() {
	for subscriber := range h.subscribers {
		snapshot := cloneSnapshot(h.latest)
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			subscriber <- snapshot
		}
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Disks = append([]Disk(nil), snapshot.Disks...)
	snapshot.Network = append([]NetworkInterface(nil), snapshot.Network...)
	snapshot.Containers = append([]Container(nil), snapshot.Containers...)
	snapshot.Warnings = append([]string(nil), snapshot.Warnings...)
	return snapshot
}

func (h *hub) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan Snapshot, 1)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	latest := cloneSnapshot(h.latest)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
	}()
	if !latest.Timestamp.IsZero() {
		if !writeSSE(w, latest) {
			return
		}
		flusher.Flush()
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case snapshot := <-ch:
			if !writeSSE(w, snapshot) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, snapshot Snapshot) bool {
	data, err := json.Marshal(snapshot)
	if err != nil {
		log.Printf("encode snapshot: %v", err)
		return false
	}
	_, err = fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", data)
	return err == nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func subtractFloor(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}
