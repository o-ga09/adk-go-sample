package store

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// The ADK's database session service (google.golang.org/adk/session/database)
// never runs AutoMigrate outside its own tests, so the tables must be created
// out-of-band before the pods start. The structs below mirror the ADK's
// unexported storage structs (storage_session.go) field-for-field; the
// gorm:"type:longtext" tags reproduce what the ADK's stateMap/dynamicJSON
// custom types emit for the MySQL dialect. Keep them in sync when upgrading
// the adk module.
//
// All time columns must be datetime(6), not GORM's default datetime(3):
// MySQL rounds sub-precision fractions, so a millisecond column can store a
// value *newer* than the in-memory nanosecond timestamp, which trips the
// ADK's stale-session check on the next AppendEvent. Microsecond columns
// combined with the timeTruncate=1µs DSN option (see store.go) guarantee the
// stored time is never ahead of the in-memory one.
//
// The precision:6 tag must accompany type:datetime(6): GORM's migrator
// compares the column's datetime_precision against field.Precision (not the
// type string), so without it AutoMigrate silently skips the ALTER on tables
// that already exist with datetime(3) columns.

// sessionSchema mirrors storageSession ('sessions' table).
type sessionSchema struct {
	AppName    string    `gorm:"primaryKey"`
	UserID     string    `gorm:"primaryKey"`
	ID         string    `gorm:"primaryKey"`
	State      string    `gorm:"type:longtext"`
	CreateTime time.Time `gorm:"type:datetime(6);precision:6"`
	UpdateTime time.Time `gorm:"type:datetime(6);precision:6"`
}

func (sessionSchema) TableName() string { return "sessions" }

// eventSchema mirrors storageEvent ('events' table). The Session association
// is intentionally omitted: GORM does not need a DB-level foreign key at
// runtime, and a RESTRICT constraint would break session deletion because the
// ADK deletes only the session row.
type eventSchema struct {
	ID        string `gorm:"primaryKey"`
	AppName   string `gorm:"primaryKey"`
	UserID    string `gorm:"primaryKey"`
	SessionID string `gorm:"primaryKey"`

	InvocationID           string
	Author                 string
	Actions                []byte
	LongRunningToolIDsJSON string `gorm:"type:longtext"`
	Branch                 *string
	Timestamp              time.Time `gorm:"type:datetime(6);precision:6"`

	Content           string `gorm:"type:longtext"`
	GroundingMetadata string `gorm:"type:longtext"`
	CustomMetadata    string `gorm:"type:longtext"`
	UsageMetadata     string `gorm:"type:longtext"`
	CitationMetadata  string `gorm:"type:longtext"`

	Partial      *bool
	TurnComplete *bool
	ErrorCode    *string
	ErrorMessage *string
	Interrupted  *bool
}

func (eventSchema) TableName() string { return "events" }

// appStateSchema mirrors storageAppState ('app_states' table).
type appStateSchema struct {
	AppName    string    `gorm:"primaryKey"`
	State      string    `gorm:"type:longtext"`
	UpdateTime time.Time `gorm:"type:datetime(6);precision:6"`
}

func (appStateSchema) TableName() string { return "app_states" }

// userStateSchema mirrors storageUserState ('user_states' table).
type userStateSchema struct {
	AppName    string    `gorm:"primaryKey"`
	UserID     string    `gorm:"primaryKey"`
	State      string    `gorm:"type:longtext"`
	UpdateTime time.Time `gorm:"type:datetime(6);precision:6"`
}

func (userStateSchema) TableName() string { return "user_states" }

// Migrate creates or updates the ADK session tables on the MySQL database
// pointed to by dsn. It is idempotent (GORM AutoMigrate) and is invoked by
// cmd/migrate from the ArgoCD PreSync Job before the pods roll out.
func Migrate(dsn string) error {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("open mysql: %w", err)
	}
	if err := db.AutoMigrate(
		&sessionSchema{},
		&eventSchema{},
		&appStateSchema{},
		&userStateSchema{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}
