package pdf

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindWorkerBin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PATH", "")
	if _, err := findWorkerBin(""); err == nil {
		t.Fatal("want not-found error")
	}
	file := "pdfium-worker"
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.MkdirAll("bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("bin", file), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findWorkerBin("")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != file {
		t.Fatalf("got %q", got)
	}
}
