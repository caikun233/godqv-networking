//go:build windows

package tunnel

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed wintun/amd64/wintun.dll
var wintunDLL []byte

var extractOnce sync.Once
var extractErr error

// ensureWintunDLL extracts the embedded wintun.dll to the executable's
// directory so that the wintun Go package can load it via LoadLibraryEx.
// The extraction is performed only once; subsequent calls are no-ops.
func ensureWintunDLL() error {
	extractOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			extractErr = fmt.Errorf("get executable path: %w", err)
			return
		}
		dir := filepath.Dir(exe)
		dst := filepath.Join(dir, "wintun.dll")

		// If the DLL already exists and has the same size, skip extraction.
		if info, err := os.Stat(dst); err == nil && info.Size() == int64(len(wintunDLL)) {
			return
		}

		if err := os.WriteFile(dst, wintunDLL, 0600); err != nil {
			extractErr = fmt.Errorf("extract wintun.dll to %s: %w", dst, err)
			return
		}
	})
	return extractErr
}
