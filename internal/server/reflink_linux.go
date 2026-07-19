//go:build linux

package server

import (
	"os"
	"syscall"
)

// FICLONE = _IOW(0x94, 9, int). Same encoding on every Linux arch we build for.
const ficlone = 0x40049409

// reflinkFile asks the kernel to make dst a copy-on-write clone of src.
//
// A reflink is NOT a hard link: the two files keep independent inodes and the
// shared extents are split on the first write to either side, so appending to
// one never changes the other. That is what makes it a safe substitute for a
// byte copy of a transcript, unlike a hard link, which would let a later turn
// written under one account silently appear in another account's folder.
//
// Only btrfs, XFS with reflink=1, and bcachefs support it, and only within one
// filesystem; everywhere else (notably ext4) this fails and the caller copies.
func reflinkFile(src, dst *os.File) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), ficlone, src.Fd()); errno != 0 {
		return errno
	}
	return nil
}
