package sandbox

import "testing"

func TestPrintable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text", in: "/Users/dev/Project/api", want: "/Users/dev/Project/api"},
		{name: "empty", in: "", want: ""},
		{name: "unicode letters", in: "/work/проект", want: "/work/проект"},
		{name: "control character", in: "a\tb", want: `"a\tb"`},
		{name: "line break", in: "a\nb", want: `"a\nb"`},
		{name: "escape sequence", in: "/work/\x1b[2K\rb", want: `"/work/\x1b[2K\rb"`},
		{name: "invalid UTF-8", in: "/work/\xff\xfe", want: `"/work/\xff\xfe"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Printable(test.in); got != test.want {
				t.Errorf("Printable(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
