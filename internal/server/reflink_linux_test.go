//go:build linux

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// reflinkFile is hard to test end to end because cloning needs btrfs/XFS and CI
// and this dev box run on ext4. This test therefore asserts the half that is
// ours either way:
//
//   - where cloning IS supported, the clone must reproduce the bytes and stay an
//     independent file (the property that makes it safe to use instead of a copy);
//   - where it is NOT, the kernel must still have RECOGNISED the request. ext4
//     answers EOPNOTSUPP ("this filesystem can't clone"), which only happens once
//     the ioctl has been dispatched. A wrong ioctl number surfaces as ENOTTY
//     ("inappropriate ioctl") or EINVAL instead, so failing those two is a real
//     regression guard on the FICLONE constant.
func TestReflinkIsWiredCorrectly(t *testing.T) {
	dir := t.TempDir()
	const body = "turn-one\nturn-two\n"
	srcPath := filepath.Join(dir, "src")
	if err := os.WriteFile(srcPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dstPath := filepath.Join(dir, "dst")
	out, err := os.Create(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	err = reflinkFile(in, out)
	if err == nil {
		t.Log("filesystem supports cloning: verifying the clone")
		got, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Fatalf("clone content = %q, want %q", got, body)
		}
		// A clone must not be a hard link: appending to it must leave src alone.
		f, err := os.OpenFile(dstPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("appended\n")
		f.Close()
		if s, _ := os.ReadFile(srcPath); string(s) != body {
			t.Fatalf("source changed after writing to the clone (hard link?): %q", s)
		}
		return
	}

	errno, ok := err.(syscall.Errno)
	if !ok {
		t.Fatalf("reflink error %v is not an errno; the ioctl wrapper is wrong", err)
	}
	if errno == syscall.ENOTTY || errno == syscall.EINVAL {
		t.Fatalf("kernel did not recognise the FICLONE ioctl (%v): the constant is wrong", errno)
	}
	t.Logf("filesystem cannot clone (%v); the request was dispatched, so the copy fallback is what runs here", errno)
}
