//go:build darwin

package directory

import "syscall"

func statFS(path string) (FSInfo, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return FSInfo{}, err
	}
	name := make([]byte, 0, len(s.Fstypename))
	for _, c := range s.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, byte(c)) //nolint:gosec // ASCII file-system names
	}
	return FSInfo{FreeBytes: s.Bavail * uint64(s.Bsize), FreeInodes: s.Ffree, Type: string(name)}, nil
}
