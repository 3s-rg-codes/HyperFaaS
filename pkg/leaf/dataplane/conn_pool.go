package dataplane

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ConnPool reuses gRPC ClientConns per target address.
type ConnPool struct {
	mu       sync.RWMutex
	conns    map[string]*pooledConn
	dialOpts []grpc.DialOption
	closed   bool
}

type pooledConn struct {
	conn     *grpc.ClientConn
	lastUsed time.Time
}

// NewConnPool creates a connection pool
func NewConnPool() *ConnPool {
	p := &ConnPool{
		conns:    make(map[string]*pooledConn),
		dialOpts: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
		closed:   false,
	}

	return p
}

// GetOrCreate returns a connection for the address if it exists, otherwise creates a new one.
func (p *ConnPool) GetOrCreate(ctx context.Context, address string) (*grpc.ClientConn, error) {
	// check while holding read lock

	p.mu.RLock()
	if conn, ok := p.conns[address]; ok {
		return conn.conn, nil
	}
	p.mu.RUnlock()

	// check again while holding write lock
	p.mu.Lock()
	if conn, ok := p.conns[address]; ok {
		p.mu.Unlock()
		return conn.conn, nil
	}

	// create new connection
	conn, dialErr := grpc.NewClient(address, p.dialOpts...)
	if dialErr != nil {
		return nil, dialErr
	}
	p.conns[address] = &pooledConn{
		conn:     conn,
		lastUsed: time.Now(),
	}
	p.mu.Unlock()

	return conn, nil
}

// Close evicts and closes the connection for address, if present.
func (p *ConnPool) Close(address string) {
	p.mu.Lock()
	entry, ok := p.conns[address]
	if ok {
		delete(p.conns, address)
	}
	p.mu.Unlock()

	if ok {
		err := entry.conn.Close()
		if err != nil {
			// TODO get rid of this panic
			panic(err)
		}
	}
}

// CloseAll closes every pooled connection.
func (p *ConnPool) CloseAll() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[string]*pooledConn)
	p.closed = true
	p.mu.Unlock()

	for _, entry := range conns {
		_ = entry.conn.Close()
	}
}
