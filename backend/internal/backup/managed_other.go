//go:build !linux

package backup

import "io"

func stageIfPortable(r io.Reader, _ string) (io.Reader, bool, error) { return r, false, nil }
