package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListFilterProfiles  = "openloc_list_filter_profiles"
	toolGetFilterProfile    = "openloc_get_filter_profile"
	toolCreateFilterProfile = "openloc_create_filter_profile"
	toolUpdateFilterProfile = "openloc_update_filter_profile"
	toolDeleteFilterProfile = "openloc_delete_filter_profile"
	toolSetFilterDefault    = "openloc_set_filter_profile_default"
)

// registerFilterProfiles registers the filter profile tools. Gates mirror
// /api/filter-profiles: every member reads; owners, admins and project
// managers create, edit and delete.
func registerFilterProfiles(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListFilterProfiles,
		title: "List filter profiles",
		description: "List the filter profiles: named import options for one file format (html, xlsx, json_i18n, po, " +
			"txt, xml), the read-only built-ins and the organization's own. Each project and template names one per " +
			"format in its settings (filter_profile_ids); a format it leaves out uses its Default. org_default marks " +
			"the organization's default for the format.",
	}, b.ListFilterProfiles)

	addTool(s, srv, managerTier, toolSpec{
		name:        toolGetFilterProfile,
		title:       "Get filter profile",
		description: "Read one filter profile and its options.",
	}, b.GetFilterProfile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateFilterProfile,
		title: "Create filter profile",
		description: "Create a filter profile for a file format, from options or as a copy of another profile (copy " +
			"a built-in to change it). A project or template uses it once it is chosen in its settings.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateFilterProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateFilterProfile,
		title: "Update filter profile",
		description: "Change a filter profile's name or options; its format never changes. Files already uploaded " +
			"keep the options they were read with." + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateFilterProfile, b.UpdateFilterProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteFilterProfile,
		title: "Delete filter profile",
		description: "Delete one of the organization's filter profiles. Files already uploaded keep the options they " +
			"were read with; projects that use it go back to the format's Default. A profile a template uses, or " +
			"the organization's default, cannot be deleted." + guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteFilterProfile, b.DeleteFilterProfile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetFilterDefault,
		title: "Set default filter profile",
		description: "Set the organization's default filter profile for one format, or null for the built-in Default. A " +
			"project or template created without a template starts with it for every format the call leaves out; " +
			"existing projects and templates keep their picks.",
		requireManager: true,
		requireWrite:   true,
	}, b.SetFilterProfileDefault)
}
