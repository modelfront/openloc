package mcp_test

import (
	"reflect"
	"strings"
	"testing"

	"openloc/internal/mcp"
	"openloc/internal/qa"
)

// The check_type a QA rule tool accepts is described to the model by a fixed
// list: it must name every built-in check type a profile can switch on, and
// regex.
func TestQARuleIn_CheckTypeSchemaListsEveryCheckType(t *testing.T) {
	field, _ := reflect.TypeOf(mcp.QARuleIn{}).FieldByName("CheckType")
	list := strings.TrimPrefix(field.Tag.Get("jsonschema"), "one of: ")
	got := map[string]bool{}
	for _, c := range strings.Split(list, ", ") {
		got[c] = true
	}
	for _, c := range qa.CheckTypeList() {
		if !got[c] {
			t.Errorf("check_type schema does not list %q", c)
		}
	}
	if !got[qa.RegexCheckType] {
		t.Errorf("check_type schema does not list %q", qa.RegexCheckType)
	}
	if len(got) != len(qa.CheckTypes)+1 {
		t.Errorf("check_type schema lists %d types, want qa.CheckTypes (%d) and regex", len(got), len(qa.CheckTypes))
	}
}
