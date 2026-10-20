package database

// MigrateFS exposes migrate to the external test package, so a test can run it
// on a hand-made migrations folder.
var MigrateFS = migrate
