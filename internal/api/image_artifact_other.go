//go:build !linux

package api

import "fmt"

// Fail closed until this platform has an equivalent atomic beneath/no-symlink
// open. Ordinary image upload remains portable; artifact reads are optional.
func readDevCheckImage(workspace, path string) ([]byte, error) {
	return nil, fmt.Errorf("atomic no-symlink DevCheck artifact reads require Linux")
}
