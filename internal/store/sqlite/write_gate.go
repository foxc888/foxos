package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

var ErrWritesFrozen = errors.New("database writes are frozen for an upgrade checkpoint")

type upgradeWriteKey struct{}

type writeGate struct {
	mu      sync.Mutex
	frozen  bool
	active  int
	changed chan struct{}
}

func newWriteGate() *writeGate {
	return &writeGate{changed: make(chan struct{})}
}

func (g *writeGate) begin(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.frozen {
		if allowed, _ := ctx.Value(upgradeWriteKey{}).(bool); !allowed {
			g.mu.Unlock()
			return nil, ErrWritesFrozen
		}
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			g.notifyLocked()
			g.mu.Unlock()
		})
	}, nil
}

func (g *writeGate) freeze(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.frozen {
		g.frozen = true
		g.notifyLocked()
	}
	for g.active > 0 {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	g.mu.Unlock()
	return nil
}

func (g *writeGate) resume() {
	g.mu.Lock()
	if g.frozen {
		g.frozen = false
		g.notifyLocked()
	}
	g.mu.Unlock()
}

func (g *writeGate) isFrozen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.frozen
}

func (g *writeGate) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

type gatedDB struct {
	*sql.DB
	gate *writeGate
}

func (db *gatedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	done, err := db.gate.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return db.DB.ExecContext(ctx, query, args...)
}

func (db *gatedDB) BeginTx(ctx context.Context, options *sql.TxOptions) (*gatedTx, error) {
	done, err := db.gate.begin(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := db.DB.BeginTx(ctx, options)
	if err != nil {
		done()
		return nil, err
	}
	return &gatedTx{Tx: tx, done: done}, nil
}

func (db *gatedDB) Conn(ctx context.Context) (*gatedConn, error) {
	connection, err := db.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &gatedConn{Conn: connection, gate: db.gate}, nil
}

type gatedTx struct {
	*sql.Tx
	done func()
}

func (tx *gatedTx) Commit() error {
	err := tx.Tx.Commit()
	tx.done()
	return err
}

func (tx *gatedTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.done()
	return err
}

type gatedConn struct {
	*sql.Conn
	gate *writeGate
}

func (connection *gatedConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	done, err := connection.gate.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return connection.Conn.ExecContext(ctx, query, args...)
}

func (connection *gatedConn) BeginTx(ctx context.Context, options *sql.TxOptions) (*gatedTx, error) {
	done, err := connection.gate.begin(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := connection.Conn.BeginTx(ctx, options)
	if err != nil {
		done()
		return nil, err
	}
	return &gatedTx{Tx: tx, done: done}, nil
}

func allowUpgradeWrite(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, upgradeWriteKey{}, true)
}
