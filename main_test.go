package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseCPUStat(t *testing.T) {
	total, idle, err := parseCPUStat("cpu  100 20 30 400 50 6 7 8 9 10\ncpu0 50 10 15 200\n")
	if err != nil {
		t.Fatal(err)
	}
	if total != 640 || idle != 450 {
		t.Fatalf("parseCPUStat() = (%d, %d), want (640, 450)", total, idle)
	}
}

func TestReadMemory(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "meminfo")
	content := "MemTotal:       1000 kB\nMemFree:         100 kB\nMemAvailable:    400 kB\nBuffers:          20 kB\nCached:          100 kB\n"
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	memory, err := readMemory(filename)
	if err != nil {
		t.Fatal(err)
	}
	if memory.TotalBytes != 1024000 || memory.UsedBytes != 614400 || memory.UsedPercent != 60 {
		t.Fatalf("readMemory() = %+v, want 60%% used", memory)
	}
}

func TestParseMounts(t *testing.T) {
	got := parseMounts("/dev/md0 /volume1 btrfs rw 0 0\n/dev/md1 /volume2 ext4 rw 0 0\n/dev/sda1 /volumeUSB1/usbshare ext4 rw 0 0\n/dev/loop0 /volume2/@docker/btrfs/subvolumes/abc btrfs rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/md0 /volume1 btrfs rw 0 0\n")
	want := []mountInfo{
		{device: "/dev/md0", mount: "/volume1", fsType: "btrfs"},
		{device: "/dev/md1", mount: "/volume2", fsType: "ext4"},
		{device: "/dev/sda1", mount: "/volumeUSB1/usbshare", fsType: "ext4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMounts() = %#v, want %#v", got, want)
	}
}

func TestParseMountsWithContainerBindPaths(t *testing.T) {
	got := parseMountsForRoot("/dev/md0 /host/volume1 btrfs rw 0 0\n/dev/md1 /host/volume2 ext4 rw 0 0\n/dev/loop0 /host/volume2/@docker/btrfs/subvolumes/abc btrfs rw 0 0\n", "/host")
	want := []mountInfo{
		{device: "/dev/md0", mount: "/volume1", fsType: "btrfs"},
		{device: "/dev/md1", mount: "/volume2", fsType: "ext4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMounts() = %#v, want %#v", got, want)
	}
}

func TestIsNASVolumeMount(t *testing.T) {
	tests := []struct {
		mount string
		want  bool
	}{
		{"/volume1", true},
		{"/volume12", true},
		{"/volume2/@docker", false},
		{"/volume2/@appdata/ContainerManager", false},
		{"/volumeUSB1/usbshare", true},
		{"/volumeUSB1/usbshare2", true},
		{"/volumeUSB1", false},
		{"/volume", false},
		{"/", false},
	}
	for _, test := range tests {
		if got := isNASVolumeMount(test.mount); got != test.want {
			t.Errorf("isNASVolumeMount(%q) = %t, want %t", test.mount, got, test.want)
		}
	}
}

func TestReadDisksUsesMountPathAsDisplayName(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(t.TempDir(), "proc")
	if err := os.MkdirAll(filepath.Join(root, "volume1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proc, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "mounts"), []byte("/dev/cachedev_0 /volume1 btrfs rw 0 0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	disks, warnings := (&Collector{hostRoot: root, hostProc: proc}).readDisks()
	if len(warnings) != 0 {
		t.Fatalf("readDisks() warnings = %v", warnings)
	}
	if len(disks) != 1 || disks[0].Name != "/volume1" {
		t.Fatalf("readDisks() = %+v, want display name /volume1", disks)
	}
}

func TestParseNetworkStats(t *testing.T) {
	got, err := parseNetworkStats("Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n  lo: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n eth0: 1000 2 0 0 0 0 0 0 2000 3 0 0 0 0 0 0\n docker0: 3000 0 0 0 0 0 0 0 4000 0 0 0 0 0 0 0\n bond0: 5000 4 0 0 0 0 0 0 6000 5 0 0 0 0 0 0\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]networkCounters{
		"eth0":  {received: 1000, sent: 2000},
		"bond0": {received: 5000, sent: 6000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNetworkStats() = %#v, want %#v", got, want)
	}
}

func TestCalculateNetworkRates(t *testing.T) {
	current := map[string]networkCounters{
		"eth0":  {received: 3000, sent: 5000},
		"bond0": {received: 250, sent: 500},
		"new0":  {received: 10000, sent: 20000},
	}
	previous := map[string]networkCounters{
		"eth0":  {received: 1000, sent: 1000},
		"bond0": {received: 500, sent: 1000},
	}
	got := calculateNetworkRates(current, previous, 2*time.Second)
	want := []NetworkInterface{
		{Name: "bond0", ReceiveBytesPerSecond: 125, SendBytesPerSecond: 250},
		{Name: "eth0", ReceiveBytesPerSecond: 1000, SendBytesPerSecond: 2000},
		{Name: "new0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calculateNetworkRates() = %#v, want %#v", got, want)
	}
}

func TestParseNetworkStatsRejectsInvalidCounters(t *testing.T) {
	if _, err := parseNetworkStats("eth0: invalid\n"); err == nil {
		t.Fatal("parseNetworkStats() expected an error for invalid counters")
	}
}

func TestContainerWithStats(t *testing.T) {
	var stats dockerStats
	data := `{
		"cpu_stats":{"cpu_usage":{"total_usage":300},"system_cpu_usage":2000,"online_cpus":4},
		"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},
		"memory_stats":{"usage":1500,"limit":4000,"stats":{"inactive_file":500}},
		"networks":{"eth0":{"rx_bytes":100,"tx_bytes":200}},
		"blkio_stats":{"io_service_bytes_recursive":[{"op":"read","value":30},{"op":"write","value":20}]}
	}`
	if err := json.Unmarshal([]byte(data), &stats); err != nil {
		t.Fatal(err)
	}
	got := containerWithStats(Container{ID: "abc", Name: "web"}, "Up 5 seconds", stats)
	if got.CPUPercent != 80 || got.MemoryBytes != 1000 || got.MemoryPercent != 25 ||
		got.NetworkBytes != 300 || got.BlockBytes != 50 || got.Status != "Up 5 seconds" {
		t.Fatalf("containerWithStats() = %+v, want live CPU/memory/network/block stats", got)
	}
}

func TestDockerCPUPercentAllowsUsageAcrossMultipleCores(t *testing.T) {
	var stats dockerStats
	stats.CPUStats.CPUUsage.TotalUsage = 2500
	stats.CPUStats.SystemUsage = 3000
	stats.CPUStats.OnlineCPUs = 4
	stats.PreCPUStats.CPUUsage.TotalUsage = 1000
	stats.PreCPUStats.SystemUsage = 2000

	got, ok := dockerCPUPercent(stats)
	if !ok {
		t.Fatal("dockerCPUPercent() did not calculate valid multi-core usage")
	}
	if got != 600 {
		t.Fatalf("dockerCPUPercent() = %v, want 600%%", got)
	}
}

func TestHubPublishesContainerUpdateImmediately(t *testing.T) {
	stream := &hub{subscribers: make(map[chan Snapshot]struct{})}
	subscriber := make(chan Snapshot, 1)
	stream.mu.Lock()
	stream.subscribers[subscriber] = struct{}{}
	stream.mu.Unlock()
	defer func() {
		stream.mu.Lock()
		delete(stream.subscribers, subscriber)
		stream.mu.Unlock()
	}()

	stream.publish(Snapshot{Timestamp: time.Now(), Hostname: "nas"})
	<-subscriber
	stream.updateContainer(Container{ID: "abc", Name: "web", CPUPercent: 12.5})

	select {
	case snapshot := <-subscriber:
		if len(snapshot.Containers) != 1 || snapshot.Containers[0].CPUPercent != 12.5 {
			t.Fatalf("received snapshot = %+v, want immediate container update", snapshot)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("container update was not published immediately")
	}
}

func TestHubBroadcastClonesSnapshotSlices(t *testing.T) {
	stream := &hub{subscribers: make(map[chan Snapshot]struct{})}
	subscriber := make(chan Snapshot, 2)
	stream.mu.Lock()
	stream.subscribers[subscriber] = struct{}{}
	stream.mu.Unlock()
	defer func() {
		stream.mu.Lock()
		delete(stream.subscribers, subscriber)
		stream.mu.Unlock()
	}()

	stream.setContainers([]Container{{ID: "abc", CPUPercent: 1}})
	first := <-subscriber
	stream.updateContainer(Container{ID: "abc", CPUPercent: 2})
	if first.Containers[0].CPUPercent != 1 {
		t.Fatalf("earlier snapshot mutated to %+v", first.Containers[0])
	}
}

func TestServeVersion(t *testing.T) {
	previous := version
	version = "v1.2.3"
	defer func() { version = previous }()

	recorder := httptest.NewRecorder()
	serveVersion(recorder, httptest.NewRequest("GET", "/api/version", nil))
	if recorder.Body.String() != "v1.2.3" {
		t.Fatalf("version response = %q, want %q", recorder.Body.String(), "v1.2.3")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

type captureTransport struct {
	requestURL string
	body       string
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requestURL = req.URL.String()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     make(http.Header),
	}, nil
}

func TestListContainersRequestsAllStates(t *testing.T) {
	transport := &captureTransport{body: `[]`}
	collector := &Collector{docker: &http.Client{Transport: transport}}
	if _, err := collector.listContainers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transport.requestURL, "all=1") {
		t.Fatalf("listContainers URL = %q, want all=1 query to include stopped containers", transport.requestURL)
	}
}

func TestContainerFromListRunningState(t *testing.T) {
	item := dockerContainer{
		ID:     "abcdef1234567890",
		Names:  []string{"/web"},
		Image:  "nginx:latest",
		State:  "exited",
		Status: "Exited (0) 2 hours ago",
	}
	stopped := containerFromList(item)
	if stopped.Running {
		t.Fatal("exited container should not be marked running")
	}
	if stopped.Status != "Exited (0) 2 hours ago" || stopped.Name != "web" {
		t.Fatalf("stopped container = %+v, want preserved status and name", stopped)
	}

	item.State = "running"
	item.Status = "Up 5 seconds"
	running := containerFromList(item)
	if !running.Running {
		t.Fatal("running container should be marked running")
	}
}

func TestSetContainersClearsMetricsForStoppedContainers(t *testing.T) {
	stream := &hub{subscribers: make(map[chan Snapshot]struct{})}
	stream.setContainers([]Container{{ID: "abc", Name: "web", Running: true, CPUPercent: 42, MemoryBytes: 1000, MemoryLimit: 4000}})
	// 容器停止后重新上报，指标字段为零值，不应继承运行时的旧数据
	stream.setContainers([]Container{{ID: "abc", Name: "web", Running: false, Status: "Exited (0)"}})
	got := stream.latest.Containers[0]
	if got.CPUPercent != 0 || got.MemoryBytes != 0 || got.MemoryLimit != 0 {
		t.Fatalf("stopped container retained stale metrics: %+v", got)
	}
}

func TestSetContainersPreservesMetricsForRunningContainers(t *testing.T) {
	stream := &hub{subscribers: make(map[chan Snapshot]struct{})}
	stream.setContainers([]Container{{ID: "abc", Name: "web", Running: true, CPUPercent: 42, MemoryBytes: 1000}})
	// 运行中的容器在刷新间隔内仍继承上一次的实时指标，避免数据抖动归零
	stream.setContainers([]Container{{ID: "abc", Name: "web", Running: true, Status: "Up 6 seconds"}})
	got := stream.latest.Containers[0]
	if got.CPUPercent != 42 || got.MemoryBytes != 1000 {
		t.Fatalf("running container lost live metrics: %+v", got)
	}
}
