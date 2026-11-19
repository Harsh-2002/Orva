package sandbox

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// sdkProtoMarker is the literal an adapter carries in its first bytes to declare
// support for the SDK channel. SDK-enabled workers require this marker.
var sdkProtoMarker = []byte("orva-sdk-proto:1")

const sdkMarkerWindow = 4 << 10

// CheckManagedRuntime validates the endpoints required by a managed worker
// without executing or importing any runtime code.
func CheckManagedRuntime(root string, lang Language) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("managed runtime root must be absolute")
	}
	if _, _, err := resolveRuntime(ExecConfig{Language: lang, runtimeRootfs: root}); err != nil {
		return err
	}
	if !adapterSupportsSDK(root, lang) {
		return fmt.Errorf("installed %s runtime adapter does not support the managed SDK channel", lang)
	}
	return nil
}

// adapterPathFor returns the installed adapter for a language inside rootfs
// (the runtime directory, i.e. <RootfsDir>/<language>), mirroring resolveRuntime.
func adapterPathFor(rootfs string, lang Language) string {
	switch lang {
	case Python:
		return filepath.Join(rootfs, "opt", "orva", "adapter.py")
	case Node:
		return filepath.Join(rootfs, "opt", "orva", "adapter.js")
	}
	return ""
}

// adapterSupportsSDK reports whether the installed adapter declares the SDK
// channel. An absent or unreadable adapter is "no", which fails SDK spawns.
// Read the bounded header each time: permissions and read failures can change
// without changing the adapter's size or mtime.
func adapterSupportsSDK(rootfs string, lang Language) bool {
	path := adapterPathFor(rootfs, lang)
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o444 == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, sdkMarkerWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	return bytes.Contains(buf[:n], sdkProtoMarker)
}
