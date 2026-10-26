//go:build linux

package pdf

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxWorkerRSSMB returns the largest resident set (MB) among this process's
// children whose command name matches the worker binary.
func maxWorkerRSSMB(workerBin string) int {
	if workerBin == "" {
		return 0
	}
	name := filepath.Base(workerBin)
	if len(name) > 15 { // /proc/<pid>/comm is truncated to 15 bytes
		name = name[:15]
	}
	self := strconv.Itoa(os.Getpid())
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		status, err := os.Open(filepath.Join("/proc", e.Name(), "status"))
		if err != nil {
			continue
		}
		var comm, ppid string
		var rssKB int
		sc := bufio.NewScanner(status)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch k {
			case "Name":
				comm = v
			case "PPid":
				ppid = v
			case "VmRSS":
				rssKB, _ = strconv.Atoi(strings.Fields(v)[0])
			}
		}
		status.Close()
		if ppid == self && comm == name && rssKB/1024 > best {
			best = rssKB / 1024
		}
	}
	return best
}
