// Package sqlite owns the single production SQLite driver registration.
//
// Opening and validating the persistent state store is intentionally added
// only after the Stage 2 dependency-intake gate is complete.
package sqlite

import _ "modernc.org/sqlite"

const driverName = "sqlite"
