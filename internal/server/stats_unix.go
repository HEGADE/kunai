//go:build darwin || linux

package server

import "syscall"

// diskInfo reports the total and available bytes of the filesystem holding dir.
func diskInfo(dir string) (total, free uint64) {
	if dir == "" {
		dir = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	return st.Blocks * bs, st.Bavail * bs
}
