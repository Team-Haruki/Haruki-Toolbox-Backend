package bootstrap

import (
	"database/sql"
	"time"
)

// openTunedSQLDB opens a database/sql pool with explicit sizing. Ent's generated
// Open leaves the database/sql defaults in place (MaxOpenConns=0 → unlimited, which
// can exhaust the server's max_connections; MaxIdleConns=2 → connection churn under
// load), so we open the pool ourselves and wrap it in the Ent driver at the call
// site. ConnMaxLifetime recycles connections so a PG restart or a proxy in front of
// PG does not leave stale connections in the pool.
func openTunedSQLDB(driverName, dataSourceName string, maxOpen, maxIdle int) (*sql.DB, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}
