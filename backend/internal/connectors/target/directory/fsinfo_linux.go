//go:build linux

package directory

import "syscall"

// linuxFS names the magic numbers of common file systems (statfs(2)).
var linuxFS = map[int64]string{
	0xEF53: "ext4", 0x58465342: "xfs", 0x9123683E: "btrfs", 0x01021994: "tmpfs", 0x794c7630: "overlay",
	0x6969: "nfs", 0xFF534D42: "cifs", 0xFE534D42: "smb2", 0x517B: "smb", 0x65735546: "fuse", 0x01021997: "9p",
	0x2FC12FC1: "zfs", 0xF2F52010: "f2fs",
}

func statFS(path string) (FSInfo, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return FSInfo{}, err
	}
	return FSInfo{FreeBytes: s.Bavail * uint64(s.Bsize), FreeInodes: s.Ffree, Type: linuxFS[int64(s.Type)]}, nil //nolint:gosec,unconvert // Bsize and Type are signed per arch
}
