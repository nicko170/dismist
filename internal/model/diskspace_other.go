//go:build !(linux || darwin)

package model

import "errors"

func diskUsage(string) (DiskSpace, error) {
	return DiskSpace{}, errors.New("free-space check not supported on this platform")
}
