//go:build !linux

package main

import "fmt"

func hostDiskUsage(string) (int64, int64, error) {
	return 0, 0, fmt.Errorf("host disk metrics require Linux")
}
