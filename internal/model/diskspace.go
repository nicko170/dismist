package model

// DiskSpace describes the filesystem holding a path.
type DiskSpace struct {
	Total uint64 // bytes
	Free  uint64 // bytes available to an unprivileged writer
}

// DiskUsage reports the size and free space of the filesystem holding dir
// (statfs). internal/migrate and the backup schedule use it for their
// free-space checks.
func DiskUsage(dir string) (DiskSpace, error) { return diskUsage(dir) }
