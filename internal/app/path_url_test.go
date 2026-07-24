package app

import "testing"

func TestEscapeNotebookPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"plain.ipynb", "plain.ipynb"},
		{"My Notebook.ipynb", "My%20Notebook.ipynb"},
		{"sub/My Notebook.ipynb", "sub/My%20Notebook.ipynb"},
		{"a#b.ipynb", "a%23b.ipynb"},
		{"caf\u00e9.ipynb", "caf%C3%A9.ipynb"},
		{"weird?.ipynb", "weird%3F.ipynb"},
	}
	for _, tc := range cases {
		if got := EscapeNotebookPath(tc.in); got != tc.want {
			t.Errorf("EscapeNotebookPath(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
