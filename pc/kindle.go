package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// A Kindle in USB mass-storage mode mounts as a volume with "documents" and
// "system" folders at its root. Recent models that connect over MTP instead
// show up as a phone-like device with no drive letter or mount point, and
// cannot be written to this way.

func isKindleRoot(root string) bool {
	for _, d := range []string{"documents", "system"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

func candidateRoots() []string {
	switch runtime.GOOS {
	case "windows":
		var roots []string
		// A and B are floppy letters and C is the system drive.
		for l := 'D'; l <= 'Z'; l++ {
			roots = append(roots, string(l)+`:\`)
		}
		return roots
	case "darwin":
		return subdirs("/Volumes")
	default:
		user := os.Getenv("USER")
		return append(subdirs("/media/"+user), subdirs("/run/media/"+user)...)
	}
}

func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// findKindle returns the root of a connected Kindle, or "" if none.
func findKindle() string {
	for _, r := range candidateRoots() {
		if isKindleRoot(r) {
			return r
		}
	}
	return ""
}

// copyToKindle copies src into the Kindle's documents folder and returns the
// destination path. A book already there with the same name and size is left
// alone.
func copyToKindle(root, src string) (string, bool, error) {
	dst := filepath.Join(root, "documents", filepath.Base(src))
	si, err := os.Stat(src)
	if err != nil {
		return "", false, err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() {
		return dst, false, nil
	}

	in, err := os.Open(src)
	if err != nil {
		return "", false, err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return "", false, err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return "", false, err
	}
	return dst, true, os.Rename(part, dst)
}
