package identtoken

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUserById", []string{"get", "user", "by", "id"}},
		{"HTTPServer", []string{"http", "server"}},
		{"user_id", []string{"user", "id"}},
		{"parseHTML5", []string{"parse", "html", "5"}},
		{"", nil},
		{"lower", []string{"lower"}},
	}
	for _, c := range cases {
		got := Split(c.in)
		if len(got) != len(c.want) {
			t.Errorf("Split(%q)=%v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("Split(%q)=%v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}
