package main

import (
	"bufio"
	"io"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	fdMonitorInterval     = 2 * time.Minute
	fdAlertThresholdRatio = 0.8 // alert once open fds reach 80% of the limit
)

var fdAlertNotified atomic.Bool

// startFDMonitor launches a background goroutine that watches the process's
// open file-descriptor count against RLIMIT_NOFILE.
//
// This exists because a leak of idle/stuck connections once exhausted the fd
// limit and took the API fully down with "accept: too many open files". A slow
// climb like that is invisible until it hits the ceiling, so this fires a
// one-time notification at 80% (with a socket-state breakdown) while there is
// still headroom to react.
func startFDMonitor() {
	limit := getFDLimit()
	if limit == 0 {
		log.Warnf("%s FD monitor disabled: could not read RLIMIT_NOFILE", logcolors.LogMemory)
		return
	}

	go func() {
		for {
			open := getOpenFDCount()
			if open < 0 {
				// /proc/self/fd unavailable (non-Linux dev host): nothing to watch.
				time.Sleep(fdMonitorInterval)
				continue
			}

			pct := float64(open) / float64(limit) * 100

			if fdThresholdExceeded(open, limit) {
				states := getSocketStates()
				log.Warnf("%s FD usage high: %d/%d (%.0f%%) | sockets: %v | goroutines: %d",
					logcolors.LogMemoryAlert, open, limit, pct, states, runtime.NumGoroutine())

				if fdAlertNotified.CompareAndSwap(false, true) {
					notifier.PublishFDThresholdExceeded(open, limit, map[string]interface{}{
						"percent":       int(pct),
						"socket_states": states,
						"goroutines":    runtime.NumGoroutine(),
					})
				}
			} else {
				fdAlertNotified.Store(false)
				log.Infof("%s FDs: %d/%d (%.0f%%)", logcolors.LogMemory, open, limit, pct)
			}

			time.Sleep(fdMonitorInterval)
		}
	}()

	log.Infof("%s FD monitor started (limit: %d, alert: %.0f%%, interval: %v)",
		logcolors.LogMemory, limit, fdAlertThresholdRatio*100, fdMonitorInterval)
}

// getOpenFDCount returns the number of open file descriptors for this process,
// or -1 when /proc is unavailable (e.g. macOS development).
func getOpenFDCount() int {
	return countDirEntries("/proc/self/fd")
}

// countDirEntries returns the number of entries in dir, or -1 on error.
func countDirEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	return len(entries)
}

// getFDLimit returns the soft RLIMIT_NOFILE for this process, or 0 on error.
func getFDLimit() uint64 {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return 0
	}
	return rl.Cur
}

// fdThresholdExceeded reports whether open fds have reached the alert ratio of limit.
func fdThresholdExceeded(open int, limit uint64) bool {
	if open < 0 || limit == 0 {
		return false
	}
	return float64(open) >= float64(limit)*fdAlertThresholdRatio
}

// getSocketStates returns a count of TCP sockets by connection state, merged
// across IPv4 and IPv6. Empty when /proc is unavailable.
//
// Note: /proc/self/net/tcp reflects the whole network namespace, not only fds
// this process holds. In production (one process per container/namespace) that
// is effectively per-process; on a shared-netns host the counts include peers.
func getSocketStates() map[string]int {
	merged := make(map[string]int)
	for _, path := range []string{"/proc/self/net/tcp", "/proc/self/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		for state, n := range parseSocketStates(f) {
			merged[state] += n
		}
		f.Close()
	}
	return merged
}

// parseSocketStates parses the /proc/net/tcp format and counts rows by TCP state.
func parseSocketStates(r io.Reader) map[string]int {
	states := make(map[string]int)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "sl" {
			continue // too short, or the header row
		}
		states[tcpStateName(fields[3])]++
	}
	return states
}

// tcpStateName maps the hex state from /proc/net/tcp to its human name.
func tcpStateName(hex string) string {
	switch strings.ToUpper(hex) {
	case "01":
		return "ESTABLISHED"
	case "02":
		return "SYN_SENT"
	case "03":
		return "SYN_RECV"
	case "04":
		return "FIN_WAIT1"
	case "05":
		return "FIN_WAIT2"
	case "06":
		return "TIME_WAIT"
	case "07":
		return "CLOSE"
	case "08":
		return "CLOSE_WAIT"
	case "09":
		return "LAST_ACK"
	case "0A":
		return "LISTEN"
	case "0B":
		return "CLOSING"
	default:
		return "STATE_" + hex
	}
}
