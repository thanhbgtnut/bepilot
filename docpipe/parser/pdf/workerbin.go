package pdf

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findWorkerBin resolves the pdfium-worker binary to an absolute path. A bare
// name ("pdfium-worker") is looked up in PATH, then next to the running
// executable, then in ./bin and the working directory, so a local build works
// on Windows and Linux without touching PATH. exec.LookPath adds ".exe" (and
// the other PATHEXT extensions) on Windows.
func findWorkerBin(name string) (string, error) {
	if name == "" {
		name = "pdfium-worker"
	}
	candidates := []string{name}
	if !strings.ContainsAny(name, `/\`) {
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
		}
		candidates = append(candidates, filepath.Join("bin", name), "."+string(filepath.Separator)+name)
	}
	var tried []string
	for _, c := range candidates {
		bin, err := exec.LookPath(c)
		if err != nil {
			tried = append(tried, c)
			continue
		}
		return filepath.Abs(bin)
	}
	return "", fmt.Errorf("pdf: pdfium worker not found (tried %s); build it with `CGO_ENABLED=1 go build -tags pdfium_cgo -o bin/pdfium-worker[.exe] ./cmd/pdfium-worker` or set BEPAYLOT_PDFIUM_WORKER to its path", strings.Join(tried, ", "))
}
