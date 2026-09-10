package api

import (
	"fmt"
	"io"
	"os"

	"cerveau/internal/llm"
	"golang.org/x/sys/unix"
)

func readDevCheckImage(workspace, path string) ([]byte, error) {
	root, err := unix.Open(workspace, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(root)
	// Resolve and open atomically: a concurrent symlink swap cannot turn a
	// validated artifact path into an arbitrary workspace or host file read.
	fd, err := unix.Openat2(root, path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "devcheck-image")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > llm.MaxImageBytes {
		return nil, fmt.Errorf("invalid image artifact size or file type")
	}
	bytes, err := io.ReadAll(io.LimitReader(file, llm.MaxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(bytes) > llm.MaxImageBytes {
		return nil, fmt.Errorf("artifact grew beyond image limit")
	}
	return bytes, nil
}
