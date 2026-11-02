package main

import "testing"

func TestParseReference(t *testing.T) {
	for argument, want := range map[string]reference{
		"D123456": {revset: "D123456", name: "D123456"},
		"d123456": {revset: "D123456", name: "D123456"},
		"https://www.internalfb.com/diff/D123456":                    {revset: "D123456", name: "D123456"},
		"https://www.internalfb.com/diff/D123456?dst_version_fbid=1": {revset: "D123456", name: "D123456"},
		"https://phabricator.intern.facebook.com/D123456/":           {revset: "D123456", name: "D123456"},
		"PR42": {revset: "PR42", name: "pr42"},
		"#42":  {revset: "PR42", name: "pr42"},
		"https://github.com/facebook/sapling/pull/42/files": {revset: "PR42", name: "pr42"},
		"0123456789abcdef0123456789abcdef01234567":          {revset: "0123456789abcdef0123456789abcdef01234567", name: "0123456789ab"},
		"remote/master": {revset: "remote/master", name: "remote-master"},
	} {
		got, err := parseReference(argument)
		if err != nil || got != want {
			t.Errorf("parseReference(%q) = (%#v, %v), want %#v", argument, got, err, want)
		}
	}
	for _, argument := range []string{"", "  ", "//"} {
		if got, err := parseReference(argument); err == nil {
			t.Errorf("parseReference(%q) = %#v, want error", argument, got)
		}
	}
}
