//go:build linux

package server

import (
	"os"
	"syscall"
)

// FICLONE = _IOW(0x94, 9, int). Same encoding on every Linux arch we build for.
const ficlone = 0x40049409

// cloneFile makes dst a copy-on-write clone of src. dst must not already exist,
// matching macOS clonefile(2), so the two platforms present one API.
//
// A clone is NOT a hard link: the files keep independent inodes and the shared
// extents split on the first write to either side, so appending to one never
// changes the other. That is what makes it a safe substitute for copying a
// transcript; a hard link would let a turn written under one account appear in
// another account's folder.
//
// Only btrfs, XFS with reflink=1, and bcachefs support this, and only within one
// filesystem. Everywhere else it fails and the caller falls back to copying.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficlone, in.Fd()); errno != 0 {
		out.Close()
		os.Remove(dst) // leave no stub behind for the copy fallback to trip on
		return errno
	}
	return out.Close()
}
