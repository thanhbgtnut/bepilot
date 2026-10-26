//go:build !linux

package pdf

// maxWorkerRSSMB is only implemented on Linux; elsewhere RSS-based recycling
// is disabled and page-count recycling still applies.
func maxWorkerRSSMB(string) int { return 0 }
