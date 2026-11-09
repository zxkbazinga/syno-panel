package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
	got := parseMounts("/dev/md0 /volume1 btrfs rw 0 0\n/dev/sda1 /volume\\040USB1/usbshare ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/md0 /volume1 btrfs rw 0 0\n")
	want := []mountInfo{
		{device: "/dev/md0", mount: "/volume1", fsType: "btrfs"},
		{device: "/dev/sda1", mount: "/volume USB1/usbshare", fsType: "ext4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMounts() = %#v, want %#v", got, want)
	}
}
