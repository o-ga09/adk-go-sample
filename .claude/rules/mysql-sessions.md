# MySQL session persistence invariants

The ADK's database session service rejects `AppendEvent` on a "stale" session if the stored `update_time` is ahead of the in-memory timestamp. Two pieces cooperate to prevent that; **both must stay in sync**:

- `internal/store/store.go` forces `timeTruncate=1µs` onto the DSN so the MySQL driver truncates (never rounds up) `time.Time` values before sending them.
- `internal/store/migrate.go` declares all time columns as `datetime(6)`. GORM's default `datetime(3)` would let MySQL round a millisecond value *upward* past the in-memory nanosecond timestamp.

Do not remove the `timeTruncate` option or downgrade any time column below `datetime(6)`.

`migrate.go` hand-mirrors the ADK's **unexported** storage structs (`google.golang.org/adk/session/database/storage_session.go`) field-for-field, because the ADK never runs AutoMigrate outside its own tests. **When upgrading the `google.golang.org/adk` module, diff these schema structs against upstream and update them.** The `gorm:"type:longtext"` tags reproduce what the ADK's custom types emit for the MySQL dialect.

The `events` table intentionally has no foreign-key association to `sessions`: the ADK deletes only the session row, so a RESTRICT constraint would break session deletion.
