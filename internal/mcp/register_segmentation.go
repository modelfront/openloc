package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

const (
	toolListSegmentationProfiles  = "openloc_list_segmentation_profiles"
	toolGetSegmentationProfile    = "openloc_get_segmentation_profile"
	toolPreviewSegmentation       = "openloc_preview_segmentation"
	toolExportSegmentationSRX     = "openloc_export_segmentation_srx"
	toolReadSegmentationSRX       = "openloc_read_segmentation_srx"
	toolCreateSegmentationProfile = "openloc_create_segmentation_profile"
	toolUpdateSegmentationProfile = "openloc_update_segmentation_profile"
	toolDeleteSegmentationProfile = "openloc_delete_segmentation_profile"
)

// registerSegmentation registers the segmentation profile tools. Gates mirror
// /api/segmentation-profiles: every member reads, previews and exports;
// owners, admins and project managers create, edit, delete and read SRX.
func registerSegmentation(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolListSegmentationProfiles,
		title: "List segmentation profiles",
		description: "List the segmentation profiles: the read-only built-ins and the organization's own, each for a " +
			"source language or for any language. Each project and template names the profile its uploads are split " +
			"with; with source_language, the list keeps the profiles that suit it and marks the one a new project starts with.",
	}, b.ListSegmentationProfiles)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetSegmentationProfile,
		title: "Get segmentation profile",
		description: "Read one segmentation profile: its mode (sentence or paragraph), its ordered rules (the first " +
			"rule whose before and after patterns match a position breaks there or not) and its abbreviation lists.",
	}, b.GetSegmentationProfile)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolPreviewSegmentation,
		title: "Preview segmentation",
		description: "Split sample text with a stored profile or with unsaved rules, and say which rule or word list " +
			"decided each position. Nothing is stored.",
	}, b.PreviewSegmentation)

	addTool(s, srv, managerTier, toolSpec{
		name:        toolExportSegmentationSRX,
		title:       "Export segmentation profile as SRX",
		description: "Return a segmentation profile as an SRX 2.0 file, for other translation tools.",
	}, b.ExportSegmentationSRX)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolReadSegmentationSRX,
		title: "Read SRX file",
		description: "Read an SRX file into unsaved segmentation profiles, with every rule that could not be used and " +
			"why. Nothing is stored: save the ones to keep with " + toolCreateSegmentationProfile + ".",
		requireManager: true,
	}, b.ReadSegmentationSRX)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolCreateSegmentationProfile,
		title: "Create segmentation profile",
		description: "Create a segmentation profile for a source language, from rules or as a copy of another profile " +
			"(copy a built-in to change it). A project or template uses it once it is chosen in its settings.",
		requireManager: true,
		requireWrite:   true,
	}, b.CreateSegmentationProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolUpdateSegmentationProfile,
		title: "Update segmentation profile",
		description: "Change a segmentation profile's name, language or rules; omitted fields keep their value. " +
			"Files already uploaded keep their segments." + guardedCallsNote,
		requireManager: true,
	}, b.PreviewUpdateSegmentationProfile, b.UpdateSegmentationProfile)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteSegmentationProfile,
		title: "Delete segmentation profile",
		description: "Delete one of the organization's segmentation profiles. Files already uploaded keep their " +
			"segments; projects that use it lose it, and their uploads are refused until they name another " +
			"profile. A profile a template uses cannot be deleted." + guardedCallsNote,
		requireManager: true,
		destructive:    true,
	}, b.PreviewDeleteSegmentationProfile, b.DeleteSegmentationProfile)
}
