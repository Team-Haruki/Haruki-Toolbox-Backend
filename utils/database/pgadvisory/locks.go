package pgadvisory

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync"
	"time"
)

// Lock takes transaction-scoped advisory locks on a dedicated connection.
// Execute BEGIN explicitly so the acquisition deadline covers BEGIN without
// causing database/sql to roll back a successfully acquired reservation.
func Lock(ctx context.Context, db *sql.DB, statement string, keys ...string) (func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		discard(conn)
		return nil, err
	}
	for _, key := range keys {
		if _, err := conn.ExecContext(ctx, statement, key); err != nil {
			discard(conn)
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		discard(conn)
		return nil, err
	}
	return sync.OnceFunc(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
			discard(conn)
			return
		}
		_ = conn.Close()
	}), nil
}

func discard(conn *sql.Conn) {
	// A canceled BEGIN or lock statement may have reached PostgreSQL. Drop the
	// session instead of returning a potentially open transaction to the pool.
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
