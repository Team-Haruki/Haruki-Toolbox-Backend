package postgresql

import stdsql "database/sql"

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
