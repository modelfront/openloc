package demodata

import (
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
)

// labelPick is one label value a project can carry.
type labelPick struct {
	defID   int64
	valueID int64
}

// systemLabel resolves a value of a built-in label (customer, domain),
// creating the value when the tenant has none of that
// name. The definitions come from the tenant trigger.
func (b *builder) systemLabel(systemKey, value string) (labelPick, error) {
	defs, err := b.q.ListLabelDefinitions(b.ctx, b.tenantID)
	if err != nil {
		return labelPick{}, fmt.Errorf("list labels: %w", err)
	}
	for _, d := range defs {
		if d.SystemKey != nil && *d.SystemKey == systemKey {
			return b.labelValue(d.ID, value)
		}
	}
	return labelPick{}, fmt.Errorf("tenant has no %q label", systemKey)
}

// demoLabel resolves the "Demo" project label and its one value, creating
// either when missing.
func (b *builder) demoLabel() (labelPick, error) {
	defs, err := b.q.ListLabelDefinitions(b.ctx, b.tenantID)
	if err != nil {
		return labelPick{}, fmt.Errorf("list labels: %w", err)
	}
	for _, d := range defs {
		if d.SystemKey == nil && strings.EqualFold(d.Name, DemoLabelName) {
			return b.labelValue(d.ID, DemoLabelValue)
		}
	}
	d, err := b.q.CreateLabelDefinition(b.ctx, db.CreateLabelDefinitionParams{TenantID: b.tenantID, Name: DemoLabelName})
	if err != nil {
		return labelPick{}, fmt.Errorf("create demo label: %w", err)
	}
	if err := b.record(ItemLabelDefinition, d.ID); err != nil {
		return labelPick{}, err
	}
	return b.labelValue(d.ID, DemoLabelValue)
}

// labelValue finds a live value of the definition by case-insensitive text,
// or creates it.
func (b *builder) labelValue(defID int64, value string) (labelPick, error) {
	values, err := b.q.ListLabelValues(b.ctx, b.tenantID)
	if err != nil {
		return labelPick{}, fmt.Errorf("list label values: %w", err)
	}
	for _, v := range values {
		if v.LabelDefinitionID == defID && strings.EqualFold(v.Value, value) {
			return labelPick{defID: defID, valueID: v.ID}, nil
		}
	}
	v, err := b.q.CreateLabelValue(b.ctx, db.CreateLabelValueParams{LabelDefinitionID: defID, Value: value})
	if err != nil {
		return labelPick{}, fmt.Errorf("create label value %q: %w", value, err)
	}
	if err := b.record(ItemLabelValue, v.ID); err != nil {
		return labelPick{}, err
	}
	return labelPick{defID: defID, valueID: v.ID}, nil
}
