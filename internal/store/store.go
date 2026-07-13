// Package store builds the persistent session service backed by MySQL so that
// conversation history and state survive pod restarts on k8s.
package store

import (
	"fmt"
	"time"

	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/driver/mysql"
)

// NewSessionService returns a MySQL-backed session.Service when a DSN is
// configured, otherwise it falls back to the in-memory service (handy for local
// development without a database).
func NewSessionService(c *config.Config) (session.Service, error) {
	if c.MySQLDSN == "" {
		return session.InMemoryService(), nil
	}
	dsn, err := microsecondDSN(c.MySQLDSN)
	if err != nil {
		return nil, err
	}
	return database.NewSessionService(mysql.Open(dsn))
}

// microsecondDSN forces timeTruncate=1µs on the DSN so the driver truncates
// time.Time values to microseconds before sending them. Without it, MySQL
// rounds the nanosecond fraction to the column precision — possibly upward —
// leaving the stored update_time ahead of the in-memory one, and the ADK's
// session service then rejects the next AppendEvent with a stale session
// error. Truncation plus datetime(6) columns (see migrate.go) guarantees
// stored time <= in-memory time.
func microsecondDSN(dsn string) (string, error) {
	cfg, err := sqlmysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse mysql dsn: %w", err)
	}
	if err := cfg.Apply(sqlmysql.TimeTruncate(time.Microsecond)); err != nil {
		return "", fmt.Errorf("apply timeTruncate: %w", err)
	}
	return cfg.FormatDSN(), nil
}
