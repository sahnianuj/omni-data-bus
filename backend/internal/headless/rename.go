package headless

import (
	"os"
	"runtime"
	"time"
)

// rename moves a finished temporary file over path. On Windows a file that antivirus or the
// search indexer has open for a moment can't be replaced, so it retries for about two seconds
// before giving up (OC-186); elsewhere a rename is atomic and done once.
func rename(tmp, path string) error {
	err := os.Rename(tmp, path)
	if runtime.GOOS != "windows" {
		return err
	}
	for wait := 10 * time.Millisecond; err != nil && wait <= 640*time.Millisecond; wait *= 2 {
		time.Sleep(wait)
		err = os.Rename(tmp, path)
	}
	return err
}
