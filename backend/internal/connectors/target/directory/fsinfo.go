package directory

// FSInfo is what statfs says about the file system holding a folder.
type FSInfo struct {
	FreeBytes  uint64
	FreeInodes uint64
	// Type is the file system's name (apfs, ext4, xfs, nfs…), "" when unknown.
	Type string
}

// networkFS are file systems where fsync of a directory (durability file+dir) is slow.
var networkFS = map[string]bool{"nfs": true, "nfs4": true, "cifs": true, "smb": true, "smb2": true, "smbfs": true, "afpfs": true, "webdav": true, "fuse": true, "9p": true}
