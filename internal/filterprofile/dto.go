package filterprofile

import "time"

// The wire types of /api/filter-profiles, exported to the frontend by tygo
// (web/src/types/filterprofile.ts).

// Profile is a filter profile as the API returns it: a built-in or one of the
// org's.
type Profile struct {
	ID int64 `json:"id"`
	// BuiltinKey names a built-in and its version ("builtin:xlsx-default@1");
	// empty on an org profile.
	BuiltinKey string `json:"builtin_key,omitempty"`
	Name       string `json:"name"`
	// Format is a filter format (Formats).
	Format  string `json:"format"`
	Builtin bool   `json:"builtin"`
	// OrgDefault is set on the org's default for the format: the pick a new
	// project or template starts with when no template gives one.
	OrgDefault  bool   `json:"org_default"`
	Description string `json:"description"`
	// BasedOn is the id of the profile this one was copied from.
	BasedOn   *int64    `json:"based_on"`
	Options   Options   `json:"options"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is what a save sends back to detect a concurrent edit.
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is the writable shape of a profile. Format is set on create and
// never changes. On create, From copies the options of another profile of
// the same format when Options is nil.
type Input struct {
	Name    string   `json:"name"`
	Format  string   `json:"format,omitempty"`
	Options *Options `json:"options"`
	From    *int64   `json:"from,omitempty"`
	// UpdatedAt must equal the stored value on update.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Choice is the filter profile a project or template uses for one format.
type Choice struct {
	Format string `json:"format"`
	ID     int64  `json:"id"`
	Name   string `json:"name"`
}

// Snapshot is the filter profile a file was imported with, stored on the
// file (files.filter_profile) so every later parse reads it the same way.
type Snapshot struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Format  string  `json:"format"`
	Builtin bool    `json:"builtin"`
	Options Options `json:"options"`
}

// DefaultInput sets the org default of one format; a null id goes back to
// the built-in Default.
type DefaultInput struct {
	Format          string `json:"format"`
	FilterProfileID *int64 `json:"filter_profile_id"`
}
