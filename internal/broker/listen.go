package broker

import (
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

var errNotAUnixSocket = errors.New("provided net.Conn is not a unix socket")

// peerCredListener only lets through connections whose peer process runs as
// allowedUID, as reported by the kernel (SO_PEERCRED)
type peerCredListener struct {
	net.Listener
	allowedUID int
	log        *slog.Logger
}

// Accept returns the next connection from allowedUID. Other connections are
// closed and logged; they never make Accept fail, since an error would stop
// http.Server.Serve for everyone
func (l *peerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// listener is closed or broken, let the server stop.
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && uid == l.allowedUID {
			return conn, nil
		}
		l.log.Warn("rejected connection", "peer_uid", uid, "err", err)
		conn.Close()
	}
}

// peerUID returns the UID of the process at the other end of c, or -1 on
// error (0 would be root).
func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errNotAUnixSocket
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}

	// Control lends the socket's fd to the function, and keeps it from being
	// closed meanwhile.
	var cred *syscall.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}

	return int(cred.Uid), nil
}

// Listen creates a unix socket at path and listens on it. The socket
// directory is created if needed and set to 0750, the socket to 0660
func Listen(path string, allowedUID int, log *slog.Logger) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	// MkdirAll is filtered by the umask and leaves an existing directory as is
	if err := os.Chmod(dir, 0o750); err != nil {
		return nil, err
	}
	// a crashed broker leaves its socket behind, and net.Listen would fail on it
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return nil, err
	}
	return &peerCredListener{Listener: l, allowedUID: allowedUID, log: log}, nil
}
