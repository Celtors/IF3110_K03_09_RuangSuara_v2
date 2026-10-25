package effect

import "testing"

// Covers every pair of group effects, including precedence independent of order.
func TestGroupEffectPrecedence(t *testing.T) {
	effects := []string{ViewOnly, ViewEdit, Blocked, "unknown"}
	for _, left := range effects {
		for _, right := range effects {
			for _, edit := range []bool{false, true} {
				list := []string{left, right}
				blocked := left == Blocked || right == Blocked
				allowed := !blocked && (left == ViewEdit || right == ViewEdit || (!edit && (left == ViewOnly || right == ViewOnly)))
				if got := Grants(list, edit); got != allowed {
					t.Errorf("Grants(%q,%q, edit=%t)=%t want %t", left, right, edit, got, allowed)
				}
				if Grants([]string{right, left}, edit) != allowed {
					t.Errorf("effect order changes result for %q and %q", left, right)
				}
			}
		}
	}
}
