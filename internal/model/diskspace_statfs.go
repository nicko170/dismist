//go:build linux || darwin

package model

import "syscall"

func diskUsage(dir string) (DiskSpace, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return DiskSpace{}, err
	}
	blockSize := uint64(stat.Bsize)
	return DiskSpace{Total: uint64(stat.Blocks) * blockSize, Free: uint64(stat.Bavail) * blockSize}, nil
}
