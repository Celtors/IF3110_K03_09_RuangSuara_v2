package effect

import "testing"

func TestIsValid(t *testing.T) {
	for _, ok := range []string{"ViewOnly", "ViewEdit", "Blocked"} {
		if !IsValid(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "ALLOW", "DENY", "Allow", "viewonly", "VIEW_ONLY", "ViewOnly ", "Blocked\n"} {
		if IsValid(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestGrants(t *testing.T) {
	cases := []struct {
		name    string
		effects []string
		edit    bool
		want    bool
	}{
		{"no policies means no access", nil, false, false},
		{"view only can view", []string{ViewOnly}, false, true},
		{"view only cannot edit", []string{ViewOnly}, true, false},
		{"view edit can view", []string{ViewEdit}, false, true},
		{"view edit can edit", []string{ViewEdit}, true, true},
		{"highest of several groups wins", []string{ViewOnly, ViewEdit}, true, true},
		{"order does not matter", []string{ViewEdit, ViewOnly}, true, true},
		{"blocked denies view", []string{Blocked}, false, false},
		{"blocked overrides view edit", []string{ViewEdit, Blocked}, false, false},
		{"blocked overrides regardless of order", []string{Blocked, ViewEdit}, true, false},
		{"unknown values grant nothing", []string{"ALLOW", "DENY"}, false, false},
		{"unknown values do not hide a real grant", []string{"ALLOW", ViewOnly}, false, true},
	}
	for _, c := range cases {
		if got := Grants(c.effects, c.edit); got != c.want {
			t.Errorf("%s: Grants(%v, edit=%v) = %v, want %v", c.name, c.effects, c.edit, got, c.want)
		}
	}
}
