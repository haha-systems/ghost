package serve

import (
	"errors"
	"net"
	"os"
)

// sameUser rejects a Unix socket peer whose uid is not this process's uid.
func sameUser(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var credErr error
	var uid uint32
	if err := raw.Control(func(fd uintptr) {
		uid, credErr = peerUID(fd)
	}); err != nil {
		return err
	}
	if credErr != nil {
		return credErr
	}
	if uid != uint32(os.Getuid()) {
		return errors.New("peer uid mismatch")
	}
	return nil
}
