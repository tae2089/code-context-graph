package treesitter

import "testing"

func TestCollectJVMMembersFromTextReusesCompiledPatterns(t *testing.T) {
	const source = `class User {
    Address address;
    val profile: Profile
}`

	var members map[string]map[string]string
	allocs := testing.AllocsPerRun(100, func() {
		members = collectJVMMembersFromText(source, "example", nil, nil, false)
	})
	if got := members["example.User"]["address"]; got != "example.Address" {
		t.Fatalf("address type = %q, want %q", got, "example.Address")
	}
	if got := members["example.User"]["profile"]; got != "example.Profile" {
		t.Fatalf("profile type = %q, want %q", got, "example.Profile")
	}
	if allocs > 100 {
		t.Fatalf("collectJVMMembersFromText() allocations = %.0f, want <= 100", allocs)
	}
}
