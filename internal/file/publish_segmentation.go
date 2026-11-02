package file

import (
	"context"
	"encoding/json"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
)

// publishSegmentation returns the segmentation snapshot a draft file is
// published with: the one stored at upload, or, when the draft's source
// language or segmentation profile changed since, the one for the draft as it
// is now.
func (h *Handler) publishSegmentation(ctx context.Context, project db.Project, f db.File) ([]byte, error) {
	lang, profileID, ok, err := segmentation.SnapshotOrigin(f.Segmentation)
	if err != nil {
		return nil, err
	}
	if !ok {
		return f.Segmentation, nil
	}
	settings, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return nil, err
	}
	sameProfile := settings.SegmentationProfileID == nil || *settings.SegmentationProfileID == profileID
	if lang == langcode.Normalize(project.SourceLanguage) && sameProfile {
		return f.Segmentation, nil
	}
	snapshot, err := segmentationapi.Resolve(ctx, h.queries, project.TenantID, settings.SegmentationProfileID, project.SourceLanguage)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}
