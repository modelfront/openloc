package settings

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/stepdef"
)

// A Pre-translation step is the whole workflow or not part of it: it completes
// itself, so a step after it would start with nobody having looked at the MT,
// and one before it would be overwritten by it. Pre-translation is the step's
// only work, so the profile always fills in MT on upload, and a live project
// on it needs an MT router to translate with. A draft or a template may lack
// one while it is being filled in.

// errPretranslateNeedsMT is the client error for switching MT off on a
// Pre-translation profile.
var errPretranslateNeedsMT = httputil.NewClientError("the Pre-translation workflow needs machine translation on upload")

// ErrPretranslateNeedsRouter refuses a live Pre-translation project with
// no MT router.
var ErrPretranslateNeedsRouter = httputil.NewClientError("Choose an MT router: the Pre-translation workflow fills files in with machine translation.")

// IsPretranslateOnly reports whether the steps are the single built-in
// Pre-translation step.
func IsPretranslateOnly(steps []db.WorkflowStep) bool {
	return len(steps) == 1 && steps[0].StepType == stepdef.TypePretranslate
}

// checkPretranslateAlone refuses a step list that mixes a Pre-translation step
// with any other step.
func checkPretranslateAlone(steps []resolvedStep) error {
	if len(steps) < 2 {
		return nil
	}
	for _, s := range steps {
		if s.stepType == stepdef.TypePretranslate {
			return httputil.NewClientError("Pre-translation cannot be combined with other steps")
		}
	}
	return nil
}

// profileIsPretranslateOnly loads the profile's steps and reports whether they
// are Pre-translation.
func profileIsPretranslateOnly(ctx context.Context, q db.Querier, profileID int64) (bool, error) {
	steps, err := q.ListSettingsWorkflowSteps(ctx, profileID)
	if err != nil {
		return false, fmt.Errorf("list workflow steps: %w", err)
	}
	return IsPretranslateOnly(steps), nil
}

// CheckPretranslateRouter refuses routerID == nil (the router the profile has
// or is about to have) on a Pre-translation profile.
func CheckPretranslateRouter(ctx context.Context, q db.Querier, profileID int64, routerID *int64) error {
	if routerID != nil {
		return nil
	}
	only, err := profileIsPretranslateOnly(ctx, q, profileID)
	if err != nil {
		return err
	}
	if only {
		return ErrPretranslateNeedsRouter
	}
	return nil
}

// CheckProfilePretranslateRouter is CheckPretranslateRouter with the router
// the profile has now.
func CheckProfilePretranslateRouter(ctx context.Context, q db.Querier, profileID int64) error {
	profile, err := q.GetSettingsProfile(ctx, profileID)
	if err != nil {
		return fmt.Errorf("get settings profile: %w", err)
	}
	return CheckPretranslateRouter(ctx, q, profileID, profile.MtRouterID)
}

// StepInputsArePretranslateOnly reports whether a step list resolves to the
// single built-in Pre-translation step. The inputs are resolved as a write
// would, so an invalid list comes back as that write's client error.
func StepInputsArePretranslateOnly(ctx context.Context, q db.Querier, tenantID int64, steps []StepInput) (bool, error) {
	resolved, err := resolveSteps(ctx, q, tenantID, nil, steps, nil)
	if err != nil {
		return false, err
	}
	return len(resolved) == 1 && resolved[0].stepType == stepdef.TypePretranslate, nil
}

// CheckPretranslateMT refuses mtOnUpload=false on a Pre-translation
// profile. For settings writes that leave the workflow alone.
func CheckPretranslateMT(ctx context.Context, q db.Querier, profileID int64, mtOnUpload bool) error {
	if mtOnUpload {
		return nil
	}
	only, err := profileIsPretranslateOnly(ctx, q, profileID)
	if err != nil {
		return err
	}
	if only {
		return errPretranslateNeedsMT
	}
	return nil
}

// ensurePretranslateMT switches MT on upload on when the profile is
// Pre-translation.
func ensurePretranslateMT(ctx context.Context, q db.Querier, profileID int64) error {
	only, err := profileIsPretranslateOnly(ctx, q, profileID)
	if err != nil || !only {
		return err
	}
	if err := q.SetSettingsProfileMtOnUpload(ctx, db.SetSettingsProfileMtOnUploadParams{ID: profileID, MtOnUpload: true}); err != nil {
		return fmt.Errorf("switch on MT on upload: %w", err)
	}
	return nil
}
