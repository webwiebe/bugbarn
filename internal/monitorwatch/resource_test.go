package monitorwatch

import "testing"

func TestResourceForReadsEnvironmentFromSlug(t *testing.T) {
	t.Parallel()
	for slug, want := range map[string]string{
		"appointment-reminders-testing": "testing",
		"sweep-production":              "production",
		"nightly-e2e":                   "e2e",
		"sweep":                         "",
		"production-sweep":              "",
		"contestingly":                  "",
	} {
		res := resourceFor(slug)
		got, _ := res["deployment.environment"].(string)
		if got != want {
			t.Errorf("%s: environment %q, want %q", slug, got, want)
		}
		if res["service.name"] != "bugbarn-monitors" {
			t.Errorf("%s: service.name lost", slug)
		}
	}
}
