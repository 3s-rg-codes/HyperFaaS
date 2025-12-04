package proxy

import (
	"sync"

	"google.golang.org/grpc"
)

// WrapClientConn wraps a grpc.ClientConnInterface so that the provided release
// callback is executed once the proxy handler completes the call.
func WrapClientConn(conn grpc.ClientConnInterface, release func(error)) grpc.ClientConnInterface {
	if release == nil {
		return conn
	}
	return &leasedClientConn{
		ClientConnInterface: conn,
		release:             release,
	}
}

type leasedClientConn struct {
	grpc.ClientConnInterface
	once    sync.Once
	release func(error)
}

func (l *leasedClientConn) Release(err error) {
	l.once.Do(func() {
		if l.release != nil {
			l.release(err)
		}
	})
}
