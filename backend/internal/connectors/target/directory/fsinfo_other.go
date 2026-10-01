//go:build !darwin && !linux

package directory

import "errors"

func statFS(string) (FSInfo, error) {
	return FSInfo{}, errors.New("free space isn't available on this platform")
}
