package sandbox

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sdkProtoMarker is the literal an adapter carries in its first bytes to declare
// support for the SDK channel. Adapters that do not have it use the HTTP SDK.
var sdkProtoMarker = []byte("orva-sdk-proto:1")

const sdkMarkerWindow = 4 << 10

type adapterCapKey struct {
	path  string
	mtime time.Time
	size  int64
}

var adapterCaps sync.Map // adapterCapKey -> bool

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
// channel. An absent or unreadable adapter is "no": the channel stays off and
// the SDK falls back to HTTP. Results are cached by path, mtime and size, so a
// runtime upgrade is picked up without re-reading the file per spawn.
func adapterSupportsSDK(rootfs string, lang Language) bool {
	path := adapterPathFor(rootfs, lang)
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	key := adapterCapKey{path: path, mtime: fi.ModTime(), size: fi.Size()}
	if v, ok := adapterCaps.Load(key); ok {
		return v.(bool)
	}
	ok := false
	if f, err := os.Open(path); err == nil {
		buf := make([]byte, sdkMarkerWindow)
		n, _ := io.ReadFull(f, buf)
		_ = f.Close()
		ok = bytes.Contains(buf[:n], sdkProtoMarker)
	}
	adapterCaps.Store(key, ok)
	return ok
}
