package sandbox

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

// sdkChannelFD is the descriptor number the sandboxed process sees. Pipes occupy
// 0-2 and cmd.ExtraFiles[0] becomes 3; nsjail closes everything else unless told
// otherwise (--pass_fd), so the number is part of the contract with the adapters.
const sdkChannelFD = 3

// maxSDKFrame bounds one frame read from untrusted sandbox code.
const maxSDKFrame = 1 << 20

// newSDKPair returns the host end of a per-worker AF_UNIX socketpair and the
// file the child inherits. Both are CLOEXEC on the host so a concurrent spawn
// cannot leak another worker's end into its own sandbox; os/exec clears CLOEXEC
// on ExtraFiles for the one child that is meant to have it.
func newSDKPair() (host net.Conn, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("sdk socketpair: %w", err)
	}
	hostFile := os.NewFile(uintptr(fds[0]), "sdk-host")
	child = os.NewFile(uintptr(fds[1]), "sdk-child")
	host, err = net.FileConn(hostFile) // dups the descriptor
	_ = hostFile.Close()
	if err != nil {
		_ = child.Close()
		return nil, nil, fmt.Errorf("sdk conn: %w", err)
	}
	return host, child, nil
}

func readSDKFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxSDKFrame {
		return nil, fmt.Errorf("sdk frame length %d out of range", n)
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeSDKFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	_, err := w.Write(append(hdr[:], payload...))
	return err
}

// serveSDKSpike is the SPIKE host handler: it echoes {"id","op":"echo","data"}.
// It exists only to prove the transport (fd passing under the production
// seccomp policy, EOF on worker death, latency); the real dispatcher replaces it.
func serveSDKSpike(c net.Conn) {
	defer c.Close()
	for {
		frame, err := readSDKFrame(c)
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal(frame, &req) != nil {
			return
		}
		req["ok"] = true
		out, _ := json.Marshal(req)
		if writeSDKFrame(c, out) != nil {
			return
		}
	}
}
