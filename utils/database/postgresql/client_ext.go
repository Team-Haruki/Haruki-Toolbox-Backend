package postgresql

import (
	stdsql "database/sql"
	"entgo.io/ent/dialect"
)

// SQLDB returns the underlying *sql.DB instance when available.
func (c *Client) SQLDB() *stdsql.DB {
	if c == nil || c.driver == nil {
		return nil
	}
	type sqlDBProvider interface {
		DB() *stdsql.DB
	}
	provider, ok := c.driver.(sqlDBProvider)
	if !ok {
		return nil
	}
	return provider.DB()
}

// PostgresSQLDB returns a connection for SQL that uses PostgreSQL syntax.
func (c *Client) PostgresSQLDB() *stdsql.DB {
	if c == nil || c.driver == nil || c.driver.Dialect() != dialect.Postgres {
		return nil
	}
	return c.SQLDB()
}
