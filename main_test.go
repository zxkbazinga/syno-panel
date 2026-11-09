package main

import (
	"os"
	"path/filepath"
	"reflect"
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
