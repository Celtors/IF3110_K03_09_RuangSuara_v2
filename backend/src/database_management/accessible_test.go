package databasemanagement

import (
	"reflect"
	"testing"

	fx "backend/access_control/effect"
)

func TestSummarizeAccessible(t *testing.T) {
	rows := []policyRow{
		{ID: "a", Name: "Alpha", Effect: fx.ViewOnly},
		{ID: "a", Name: "Alpha", Effect: fx.ViewEdit},    // ViewEdit wins over ViewOnly
		{ID: "b", Name: "Bravo", Effect: fx.ViewOnly},
		{ID: "c", Name: "Charlie", Effect: fx.ViewEdit},
		{ID: "c", Name: "Charlie", Effect: fx.Blocked},   // Blocked overrides everything
		{ID: "d", Name: "Delta", Effect: "Admin"},        // unknown grants nothing
	}
	got := summarizeAccessible(rows)
	want := []accessibleDatabase{
		{ID: "a", Name: "Alpha", Permission: fx.ViewEdit},
		{ID: "b", Name: "Bravo", Permission: fx.ViewOnly},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if len(summarizeAccessible(nil)) != 0 {
		t.Fatal("no policies must produce an empty list")
	}
}