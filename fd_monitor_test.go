package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountDirEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got := countDirEntries(dir); got != 3 {
		t.Errorf("countDirEntries = %d, want 3", got)
	}
	if got := countDirEntries(filepath.Join(dir, "does-not-exist")); got != -1 {
		t.Errorf("countDirEntries(missing) = %d, want -1", got)
	}
}

func TestFDThresholdExceeded(t *testing.T) {
	// fdAlertThresholdRatio is 0.8; 65536 * 0.8 = 52428.8
	tests := []struct {
		name  string
		open  int
		limit uint64
		want  bool
	}{
		{"at threshold", 52429, 65536, true},
		{"just below threshold", 52428, 65536, false},
		{"well under", 100, 65536, false},
		{"maxed out", 65536, 65536, true},
		{"unknown limit", 1000, 0, false},
		{"unknown open count", -1, 65536, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fdThresholdExceeded(tc.open, tc.limit); got != tc.want {
				t.Errorf("fdThresholdExceeded(%d, %d) = %v, want %v", tc.open, tc.limit, got, tc.want)
			}
		})
	}
}

func TestParseSocketStates(t *testing.T) {
	// Minimal /proc/net/tcp sample: header + LISTEN, ESTABLISHED, ESTABLISHED, CLOSE_WAIT
	sample := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000 100 0 0 10 0
   1: 0100007F:1F90 0100007F:C1B2 01 00000000:00000000 00:00000000 00000000  1000        0 12346 1 0000 20 4 30 10 -1
   2: 0100007F:1F90 0100007F:C1B3 01 00000000:00000000 00:00000000 00000000  1000        0 12347 1 0000 20 4 30 10 -1
   3: 0100007F:1F90 0100007F:C1B4 08 00000000:00000000 00:00000000 00000000  1000        0 12348 1 0000 20 4 30 10 -1
`
	got := parseSocketStates(strings.NewReader(sample))
	if got["ESTABLISHED"] != 2 {
		t.Errorf("ESTABLISHED = %d, want 2", got["ESTABLISHED"])
	}
	if got["LISTEN"] != 1 {
		t.Errorf("LISTEN = %d, want 1", got["LISTEN"])
	}
	if got["CLOSE_WAIT"] != 1 {
		t.Errorf("CLOSE_WAIT = %d, want 1", got["CLOSE_WAIT"])
	}
}

func TestGetFDLimit(t *testing.T) {
	if got := getFDLimit(); got == 0 {
		t.Error("getFDLimit() returned 0, expected the process RLIMIT_NOFILE soft limit")
	}
}
