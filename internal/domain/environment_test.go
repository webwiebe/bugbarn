package domain

import "testing"

func TestEnvironmentSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, base, env string
		ok            bool
	}{
		{"bananasketch-staging", "bananasketch", "staging", true},
		{"videoslop-production", "videoslop", "production", true},
		{"maatwerksoftware-testing", "maatwerksoftware", "testing", true},
		{"brandtrace-e2e", "brandtrace", "e2e", true},
		{"app_dev", "app", "dev", true},
		{"Shop-PROD", "Shop", "production", true},
		{"bugbarn-service", "", "", false},
		{"staging", "", "", false},
		{"-test", "", "", false},
		{"Read Only Test", "", "", false},
		{"contest", "", "", false},
	}
	for _, c := range cases {
		base, env, ok := EnvironmentSuffix(c.in)
		if base != c.base || env != c.env || ok != c.ok {
			t.Errorf("EnvironmentSuffix(%q) = %q, %q, %v; want %q, %q, %v", c.in, base, env, ok, c.base, c.env, c.ok)
		}
	}
}

func TestNormalizeEnvironment(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"Production": "production", " e2e ": "e2e", "qa-2": "qa-2"} {
		if got, ok := NormalizeEnvironment(in); !ok || got != want {
			t.Errorf("NormalizeEnvironment(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", " ", "has space", "-lead", "UPPER!", "waytoolongenvironmentnamethatexceeds32chars"} {
		if _, ok := NormalizeEnvironment(bad); ok {
			t.Errorf("NormalizeEnvironment(%q) accepted", bad)
		}
	}
}
