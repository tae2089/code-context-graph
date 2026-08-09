package identtoken

import (
	"reflect"
	"testing"
)

func TestFields_KeepsCaseAndUnderscores(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUser byID", []string{"getUser", "byID"}},
		{"user_id, HTTPServer!", []string{"user_id", "HTTPServer"}},
		{"why do we verify?", []string{"why", "do", "we", "verify"}},
		{"한글 조사가", []string{"한글", "조사가"}},
		{"...", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := Fields(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Fields(%q)=%v, want %v", c.in, got, c.want)
		}
	}
}

func TestFieldsLower_LowercasesEveryField(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUser byID", []string{"getuser", "byid"}},
		{"user_id HTTPServer", []string{"user_id", "httpserver"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := FieldsLower(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("FieldsLower(%q)=%v, want %v", c.in, got, c.want)
		}
	}
}

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
