package main

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func TestApexWireMessages(t *testing.T) {
	if got, want := apexHelloMessage("code", "work-dash"), []byte{
		0, 4, 'c', 'o', 'd', 'e', 9, 'w', 'o', 'r', 'k', '-', 'd', 'a', 's', 'h', 1, 0,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hello = %v, want %v", got, want)
	}
	if got, want := apexGotoMessage(1, "/repo/-codex"), []byte{
		20, 1, 24, 12, '/', 'r', 'e', 'p', 'o', '/', '-', 'c', 'o', 'd', 'e', 'x', 0,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("goto = %v, want %v", got, want)
	}

	var framed bytes.Buffer
	payload := []byte{1, 2, 3}
	if err := writeApexFrame(&framed, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readApexFrame(&framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("frame = %v, want %v", got, payload)
	}
}

func TestDecodeApexBuild(t *testing.T) {
	frame := appendPostcardUint(nil, apexServerBuild)
	frame = appendPostcardUint(frame, apexWireProtocol)
	frame = appendPostcardString(frame, "build-id")
	protocol, err := decodeApexBuild(frame)
	if err != nil || protocol != apexWireProtocol {
		t.Fatalf("decodeApexBuild = (%d, %v)", protocol, err)
	}
}

func TestDecodeApexPlumb(t *testing.T) {
	frame := appendPostcardUint(nil, apexServerPlumb)
	frame = appendPostcardUint(frame, 7)
	frame = appendPostcardUint(frame, apexExecContextWindow)
	frame = appendPostcardUint(frame, 300)
	frame = appendPostcardString(frame, "plumb")
	frame = appendPostcardString(frame, "codex")
	frame = appendPostcardString(frame, "/repo")
	frame = appendPostcardUint(frame, 1)
	frame = appendPostcardString(frame, "codex")
	frame = append(frame, 1)
	frame = appendPostcardUint(frame, 9)
	frame = appendPostcardUint(frame, 42)
	frame = appendPostcardUint(frame, 47)
	frame = append(frame, 0)

	got, ok := decodeApexPlumb(frame)
	want := apexPlumb{ID: 7, Window: "300", Verb: "plumb", Text: "codex", Point: 42, HasAt: true}
	if !ok || got != want {
		t.Fatalf("decodeApexPlumb = (%#v, %v), want (%#v, true)", got, ok, want)
	}
}

func TestDecodeApexApplied(t *testing.T) {
	success := appendPostcardUint(nil, apexServerApplied)
	success = appendPostcardUint(success, 7)
	success = append(success, 0, 1)
	got, ok := decodeApexApplied(success)
	if !ok || got.ID != 7 || got.Err != nil {
		t.Fatalf("success = (%#v, %v)", got, ok)
	}

	failure := appendPostcardUint(nil, apexServerApplied)
	failure = appendPostcardUint(failure, 8)
	failure = append(failure, 1)
	failure = appendPostcardString(failure, "no window")
	got, ok = decodeApexApplied(failure)
	if !ok || got.ID != 8 || got.Err == nil || got.Err.Error() != "no window" {
		t.Fatalf("failure = (%#v, %v)", got, ok)
	}
}

func TestApexWireIntegration(t *testing.T) {
	session := os.Getenv("WORK_APEX_TEST_SESSION")
	target := os.Getenv("WORK_APEX_TEST_WINDOW")
	if session == "" || target == "" {
		t.Skip("set WORK_APEX_TEST_SESSION and WORK_APEX_TEST_WINDOW")
	}
	client, err := connectApexTool(apexSocketPath(os.Getenv), session, "work-wire-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.gotoWindow(target); err != nil {
		t.Fatal(err)
	}
}

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
