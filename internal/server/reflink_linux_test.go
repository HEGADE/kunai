//go:build linux

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The Linux clone is a raw ioctl with a hardcoded FICLONE number, so it needs a
// guard the cross-platform test cannot give: on a filesystem that cannot clone,
// the kernel must still have RECOGNISED the request. ext4 answers EOPNOTSUPP,
// which is only reached after the ioctl has been dispatched. A wrong number
// surfaces as ENOTTY ("inappropriate ioctl") or EINVAL instead, so failing on
// those catches a broken constant without needing a btrfs volume.
//
// (macOS takes its syscall number from x/sys/unix rather than a literal, which
// is why there is no darwin equivalent of this test.)
func TestLinuxCloneIoctlIsRecognised(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cloneFile(src, filepath.Join(dir, "dst"))
	if err == nil {
		t.Log("filesystem supports cloning; the ioctl is plainly wired up")
		return
	}
	errno, ok := err.(syscall.Errno)
	if !ok {
		t.Fatalf("clone error %v is not an errno: the ioctl wrapper is wrong", err)
	}
	if errno == syscall.ENOTTY || errno == syscall.EINVAL {
		t.Fatalf("kernel did not recognise the FICLONE ioctl (%v): the constant is wrong", errno)
	}
	t.Logf("filesystem cannot clone (%v), but the ioctl was dispatched", errno)
}
