package main

import "testing"

func TestApexHookLocation(t *testing.T) {
	environment := map[string]string{
		"APEX_SOCKET": "/tmp/apex-user/main.sock",
		"apexsession": "code",
		"winid":       "42",
	}
	getenv := func(key string) string { return environment[key] }
	socket, session, window := apexHookLocation(getenv)
	if socket != environment["APEX_SOCKET"] || session != "code" || window != "42" {
		t.Fatalf("apexHookLocation = (%q, %q, %q)", socket, session, window)
	}
	environment["winid"] = ""
	if socket, session, window := apexHookLocation(getenv); socket != "" || session != "" || window != "" {
		t.Fatalf("non-Apex location = (%q, %q, %q)", socket, session, window)
	}
}
