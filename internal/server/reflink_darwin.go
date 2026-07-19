//go:build darwin

package server

import "golang.org/x/sys/unix"

// cloneFile makes dst a copy-on-write clone of src on APFS, which is every
// modern Mac. dst must not already exist: clonefile(2) creates it.
//
// The syscall number comes from x/sys/unix rather than a hardcoded constant on
// purpose. The Linux FICLONE number can be checked from a Linux box by proving
// the kernel answers EOPNOTSUPP rather than ENOTTY; there is no equivalent check
// available when cross-compiling for macOS, so this leans on the maintained
// binding instead of a number we could not verify.
//
// As on Linux, a clone is not a hard link: writes to one side split the shared
// extents, so the two accounts' transcripts stay independent.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
