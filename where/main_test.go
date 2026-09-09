package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindMatchesInTraversalOrder(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "monument"))
	makeFile(t, filepath.Join(root, "mon"))
	makeFile(t, filepath.Join(root, "common"))
	makeFile(t, filepath.Join(root, "monitoring"))
	makeFile(t, filepath.Join(root, "unrelated"))

	matches, err := findMatches("mon", root)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got := rendered(matches)
	want := []string{
		filepath.Join(root, "common"),
		filepath.Join(root, "mon"),
		filepath.Join(root, "monitoring"),
		filepath.Join(root, "monument") + string(filepath.Separator),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}

}

func TestFindMatchesExpandsGlobsAndDeduplicatesOverlappingRoots(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "root-a")
	rootB := filepath.Join(base, "root-b")
	makeDir(t, filepath.Join(rootA, "nested"))
	makeDir(t, rootB)
	makeFile(t, filepath.Join(rootA, "nested", "Smartlog"))
	makeFile(t, filepath.Join(rootB, "SmartSync"))

	wherePath := strings.Join([]string{
		filepath.Join(base, "root-*"),
		filepath.Join(rootA, "nested"),
	}, string(os.PathListSeparator))
	matches, err := findMatches("Smart", wherePath)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got := rendered(matches)
	want := []string{
		filepath.Join(rootB, "SmartSync"),
		filepath.Join(rootA, "nested", "Smartlog"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}

}

func TestFindMatchesDoesNotSearchRecursively(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "monarch"))
	makeFile(t, filepath.Join(root, "monarch", "monitoring"))

	matches, err := findMatches("mon", root)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got := rendered(matches)
	want := []string{filepath.Join(root, "monarch") + string(filepath.Separator)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}
}

func TestFindMatchesTraversesForEachQueryComponent(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "monarch"))
	makeDir(t, filepath.Join(root, "monarch-kubernetes"))
	makeDir(t, filepath.Join(root, "unrelated"))
	makeFile(t, filepath.Join(root, "monarch", "BUCK"))
	makeFile(t, filepath.Join(root, "monarch", "BUILD_MODE"))
	makeFile(t, filepath.Join(root, "monarch-kubernetes", "BUCK"))
	makeFile(t, filepath.Join(root, "unrelated", "BUCK"))
	makeFile(t, filepath.Join(root, "monitoring"))

	matches, err := findMatches("mon/BUCK", root)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got := rendered(matches)
	want := []string{
		filepath.Join(root, "monarch", "BUCK"),
		filepath.Join(root, "monarch-kubernetes", "BUCK"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}

	matches, err = findMatches("monarch/BUILD", root)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got = rendered(matches)
	want = []string{filepath.Join(root, "monarch", "BUILD_MODE")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}
}

func TestFindMatchesSupportsMultipleTraversalLevels(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "monarch", "build"))
	makeFile(t, filepath.Join(root, "monarch", "build", "TARGETS"))

	matches, err := findMatches("mon/bui/TAR", root)
	if err != nil {
		t.Fatalf("findMatches: %v", err)
	}

	got := rendered(matches)
	want := []string{filepath.Join(root, "monarch", "build", "TARGETS")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}
}

func TestFindMatchesSupportsExactComponents(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "lib"))
	makeDir(t, filepath.Join(root, "library"))
	makeFile(t, filepath.Join(root, "lib", "rc"))
	makeFile(t, filepath.Join(root, "lib", "rcrc"))
	makeFile(t, filepath.Join(root, "library", "rc"))
	makeFile(t, filepath.Join(root, "library", "rcrc"))

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "quoted first component",
			query: `"lib"/rc`,
			want: []string{
				filepath.Join(root, "lib", "rc"),
				filepath.Join(root, "lib", "rcrc"),
			},
		},
		{
			name:  "equals first component",
			query: "=lib/rc",
			want: []string{
				filepath.Join(root, "lib", "rc"),
				filepath.Join(root, "lib", "rcrc"),
			},
		},
		{
			name:  "quoted second component",
			query: `lib/"rc"`,
			want: []string{
				filepath.Join(root, "lib", "rc"),
				filepath.Join(root, "library", "rc"),
			},
		},
		{
			name:  "equals second component",
			query: "lib/=rc",
			want: []string{
				filepath.Join(root, "lib", "rc"),
				filepath.Join(root, "library", "rc"),
			},
		},
		{
			name:  "quoted whole query",
			query: `"lib/rc"`,
			want:  []string{filepath.Join(root, "lib", "rc")},
		},
		{
			name:  "equals every component",
			query: "=lib/=rc",
			want:  []string{filepath.Join(root, "lib", "rc")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches, err := findMatches(test.query, root)
			if err != nil {
				t.Fatalf("findMatches: %v", err)
			}
			if got := rendered(matches); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("matches = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseQueryRejectsMalformedExactComponents(t *testing.T) {
	for _, query := range []string{`"lib/rc`, `lib/rc"`, `lib/"r"c`, `lib/=`, `""/rc`} {
		t.Run(query, func(t *testing.T) {
			if _, err := parseQuery(query); err == nil {
				t.Fatalf("parseQuery(%q) returned nil error", query)
			}
		})
	}
}

func TestFindMatchesRejectsEmptyQueryComponent(t *testing.T) {
	_, err := findMatches("mon//BUCK", t.TempDir())
	if err == nil {
		t.Fatal("findMatches returned nil error")
	}
}

func TestRunRequiresWherePath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"thing"}, func(string) string { return "" }, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if got := stderr.String(); !strings.Contains(got, "WHEREPATH is not set") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{arg}, func(string) string {
				t.Fatal("help should not read WHEREPATH")
				return ""
			}, &stdout, &stderr)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			for _, want := range []string{
				"where finds files and directories",
				"export WHEREPATH=",
				"where mon/BUCK",
				`where "lib"/rc`,
				"where =lib/=rc",
			} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help output does not contain %q", want)
				}
			}
		})
	}
}

func TestExpandWherePathRejectsMalformedGlob(t *testing.T) {
	_, err := expandWherePath("[")
	if err == nil {
		t.Fatal("expandWherePath returned nil error")
	}
}

func rendered(matches []match) []string {
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		path := match.path
		if match.isDir {
			path += string(filepath.Separator)
		}
		result = append(result, path)
	}
	return result
}

func makeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func makeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
