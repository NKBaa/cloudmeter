//go:build linux

package main

import "syscall"

func hostDiskUsage(path string) (int64, int64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0, 0, e
	}
	return int64(s.Blocks) * int64(s.Bsize), int64(s.Bavail) * int64(s.Bsize), nil
}
