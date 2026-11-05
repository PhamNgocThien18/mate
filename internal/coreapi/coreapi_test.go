package coreapi

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// A request crosses the core's socket as JSON (CON-212), every field under
// its name in snake_case - a field added later too.
func TestActionRequestFieldsAreSnakeCaseInJSON(t *testing.T) {
	typ := reflect.TypeOf(ActionRequest{})
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if want := snakeCase(f.Name); name != want {
			t.Errorf("ActionRequest.%s is %q in JSON, want %q", f.Name, name, want)
		}
	}
}

// snakeCase is a Go field name in snake_case: TargetKind is target_kind.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
