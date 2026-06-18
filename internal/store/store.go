// Package store builds the persistent session service backed by MySQL so that
// conversation history and state survive pod restarts on k8s.
package store

import (
	"github.com/o-ga09/adk-go-sample/internal/config"
	"google.golang.org/adk/session"
	"google.golang.org/adk/session/database"
	"gorm.io/driver/mysql"
)

// NewSessionService returns a MySQL-backed session.Service when a DSN is
// configured, otherwise it falls back to the in-memory service (handy for local
// development without a database).
func NewSessionService(c *config.Config) (session.Service, error) {
	if c.MySQLDSN == "" {
		return session.InMemoryService(), nil
	}
	return database.NewSessionService(mysql.Open(c.MySQLDSN))
}
