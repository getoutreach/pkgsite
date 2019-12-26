// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fetch

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/discovery/internal"
	"golang.org/x/discovery/internal/derrors"
	"golang.org/x/discovery/internal/license"
	"golang.org/x/discovery/internal/postgres"
	"golang.org/x/discovery/internal/proxy"
	"golang.org/x/discovery/internal/source"
	"golang.org/x/discovery/internal/stdlib"
	"golang.org/x/discovery/internal/testing/sample"
	"golang.org/x/discovery/internal/testing/testhelper"
	"golang.org/x/discovery/internal/version"
)

const testTimeout = 30 * time.Second

var testDB *postgres.DB

func TestMain(m *testing.M) {
	httpClient = &http.Client{Transport: fakeTransport{}}
	postgres.RunDBTests("discovery_etl_test", m, &testDB)
}

type fakeTransport struct{}

func (fakeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("bad")
}

func TestSkipIncompletePackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	defer postgres.ResetTestDB(testDB, t)
	badModule := map[string]string{
		"foo/foo.go": "// Package foo\npackage foo\n\nconst Foo = 42",
		"README.md":  "This is a readme",
		"LICENSE":    testhelper.MITLicense,
	}
	var bigFile strings.Builder
	bigFile.WriteString("package bar\n")
	bigFile.WriteString("const Bar = 123\n")
	for bigFile.Len() <= maxFileSize {
		bigFile.WriteString("// All work and no play makes Jack a dull boy.\n")
	}
	badModule["bar/bar.go"] = bigFile.String()
	var (
		modulePath = "github.com/my/module"
		version    = "v1.0.0"
	)
	client, teardownProxy := proxy.SetupTestProxy(t, []*proxy.TestVersion{
		proxy.NewTestVersion(t, modulePath, version, badModule),
	})
	defer teardownProxy()

	HasIncompletePackages, err := FetchAndInsertVersion(ctx, modulePath, version, client, testDB)
	if err != nil {
		t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v): %v", modulePath, version, client, testDB, err)
	}
	if !HasIncompletePackages {
		t.Errorf("FetchAndInsertVersion(%q, %q, %v, %v): HasIncompletePackages=false, want true",
			modulePath, version, client, testDB)
	}

	pkgFoo := modulePath + "/foo"
	if _, err := testDB.GetPackage(ctx, pkgFoo, internal.UnknownModulePath, version); err != nil {
		t.Errorf("got %v, want nil", err)
	}
	pkgBar := modulePath + "/bar"
	if _, err := testDB.GetPackage(ctx, pkgBar, internal.UnknownModulePath, version); !errors.Is(err, derrors.NotFound) {
		t.Errorf("got %v, want NotFound", err)
	}
}

// Test that large string literals and slices are trimmed when
// rendering documentation, rather than being included verbatim.
//
// This makes it viable for us to show documentation for packages that
// would otherwise exceed HTML size limit and not get shown at all.
func TestTrimLargeCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	defer postgres.ResetTestDB(testDB, t)
	trimmedModule := map[string]string{
		"foo/foo.go": "// Package foo\npackage foo\n\nconst Foo = 42",
		"LICENSE":    testhelper.MITLicense,
	}
	// Create a package with a large string literal. It should not be included verbatim.
	{
		var b strings.Builder
		b.WriteString("package bar\n\n")
		b.WriteString("const Bar = `\n")
		for b.Len() <= maxDocumentationHTML {
			b.WriteString("All work and no play makes Jack a dull boy.\n")
		}
		b.WriteString("`\n")
		trimmedModule["bar/bar.go"] = b.String()
	}
	// Create a package with a large slice. It should not be included verbatim.
	{
		var b strings.Builder
		b.WriteString("package baz\n\n")
		b.WriteString("var Baz = []string{\n")
		for b.Len() <= maxDocumentationHTML {
			b.WriteString("`All work and no play makes Jack a dull boy.`,\n")
		}
		b.WriteString("}\n")
		trimmedModule["baz/baz.go"] = b.String()
	}
	var (
		modulePath = "github.com/my/module"
		version    = "v1.0.0"
	)
	client, teardownProxy := proxy.SetupTestProxy(t, []*proxy.TestVersion{
		proxy.NewTestVersion(t, modulePath, version, trimmedModule),
	})
	defer teardownProxy()

	HasIncompletePackages, err := FetchAndInsertVersion(ctx, modulePath, version, client, testDB)
	if err != nil {
		t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v): %v", modulePath, version, client, testDB, err)
	}
	if HasIncompletePackages {
		t.Errorf("FetchAndInsertVersion(%q, %q, %v, %v): HasIncompletePackages=true, want false",
			modulePath, version, client, testDB)
	}

	pkgFoo := modulePath + "/foo"
	if _, err := testDB.GetPackage(ctx, pkgFoo, internal.UnknownModulePath, version); err != nil {
		t.Errorf("got %v, want nil", err)
	}
	pkgBar := modulePath + "/bar"
	if _, err := testDB.GetPackage(ctx, pkgBar, internal.UnknownModulePath, version); err != nil {
		t.Errorf("got %v, want nil", err)
	}
	pkgBaz := modulePath + "/baz"
	if _, err := testDB.GetPackage(ctx, pkgBaz, internal.UnknownModulePath, version); err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestFetch_V1Path(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	defer postgres.ResetTestDB(testDB, t)
	client, tearDown := proxy.SetupTestProxy(t, []*proxy.TestVersion{
		proxy.NewTestVersion(t, "my.mod/foo", "v1.0.0", map[string]string{
			"foo.go":  "package foo\nconst Foo = 41",
			"LICENSE": testhelper.MITLicense,
		}),
	})
	defer tearDown()
	if _, err := FetchAndInsertVersion(ctx, "my.mod/foo", "v1.0.0", client, testDB); err != nil {
		t.Fatalf("FetchAndInsertVersion: %v", err)
	}
	pkg, err := testDB.GetPackage(ctx, "my.mod/foo", internal.UnknownModulePath, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pkg.V1Path, "my.mod/foo"; got != want {
		t.Errorf("V1Path = %q, want %q", got, want)
	}
}

func TestReFetch(t *testing.T) {
	// This test checks that re-fetching a version will cause its data to be
	// overwritten.  This is achieved by fetching against two different versions
	// of the (fake) proxy, though in reality the most likely cause of changes to
	// a version is updates to our data model or fetch logic.
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	defer postgres.ResetTestDB(testDB, t)

	var (
		modulePath = "github.com/my/module"
		version    = "v1.0.0"
		pkgFoo     = "github.com/my/module/foo"
		pkgBar     = "github.com/my/module/bar"
		foo        = map[string]string{
			"foo/foo.go": "// Package foo\npackage foo\n\nconst Foo = 42",
			"README.md":  "This is a readme",
			"LICENSE":    testhelper.MITLicense,
		}
		bar = map[string]string{
			"bar/bar.go": "// Package bar\npackage bar\n\nconst Bar = 21",
			"README.md":  "This is another readme",
			"COPYING":    testhelper.MITLicense,
		}
	)

	// First fetch and insert a version containing package foo, and verify that
	// foo can be retrieved.
	client, teardownProxy := proxy.SetupTestProxy(t, []*proxy.TestVersion{
		proxy.NewTestVersion(t, modulePath, version, foo),
	})
	defer teardownProxy()
	if _, err := FetchAndInsertVersion(ctx, modulePath, version, client, testDB); err != nil {
		t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v): %v", modulePath, version, client, testDB, err)
	}

	if _, err := testDB.GetPackage(ctx, pkgFoo, internal.UnknownModulePath, version); err != nil {
		t.Error(err)
	}

	// Now re-fetch and verify that contents were overwritten.
	client, teardownProxy = proxy.SetupTestProxy(t, []*proxy.TestVersion{
		proxy.NewTestVersion(t, modulePath, version, bar),
	})
	defer teardownProxy()

	if _, err := FetchAndInsertVersion(ctx, modulePath, version, client, testDB); err != nil {
		t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v): %v", modulePath, version, client, testDB, err)
	}
	want := &internal.VersionedPackage{
		VersionInfo: internal.VersionInfo{
			ModulePath:     modulePath,
			Version:        version,
			CommitTime:     time.Date(2019, 1, 30, 0, 0, 0, 0, time.UTC),
			ReadmeFilePath: "README.md",
			ReadmeContents: "This is another readme",
			VersionType:    "release",
			SourceInfo:     source.NewGitHubInfo("https://github.com/my/module", "", "v1.0.0"),
		},
		Package: internal.Package{
			Path:              "github.com/my/module/bar",
			Name:              "bar",
			Synopsis:          "Package bar",
			DocumentationHTML: "Bar returns the string &#34;bar&#34;.",
			V1Path:            "github.com/my/module/bar",
			Licenses: []*license.Metadata{
				{Types: []string{"MIT"}, FilePath: "COPYING"},
			},
			GOOS:   "linux",
			GOARCH: "amd64",
		},
	}
	got, err := testDB.GetPackage(ctx, pkgBar, internal.UnknownModulePath, version)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(internal.Package{}, "DocumentationHTML"), cmp.AllowUnexported(source.Info{})); diff != "" {
		t.Errorf("testDB.GetPackage(ctx, %q, %q) mismatch (-want +got):\n%s", pkgBar, version, diff)
	}

	// For good measure, verify that package foo is now NotFound.
	if _, err := testDB.GetPackage(ctx, pkgFoo, internal.UnknownModulePath, version); !errors.Is(err, derrors.NotFound) {
		t.Errorf("got %v, want NotFound", err)
	}
}

var testProxyCommitTime = time.Date(2019, 1, 30, 0, 0, 0, 0, time.UTC)

func TestMatchingFiles(t *testing.T) {
	plainGoBody := `
		package plain
		type Value int`
	jsGoBody := `
		// +build js,wasm

		// Package js only works with wasm.
		package js
		type Value int`

	plainContents := map[string]string{
		"README.md":      "THIS IS A README",
		"LICENSE.md":     testhelper.MITLicense,
		"plain/plain.go": plainGoBody,
	}

	jsContents := map[string]string{
		"README.md":  "THIS IS A README",
		"LICENSE.md": testhelper.MITLicense,
		"js/js.go":   jsGoBody,
	}
	for _, test := range []struct {
		name         string
		goos, goarch string
		contents     map[string]string
		want         map[string][]byte
	}{
		{
			name:     "plain-linux",
			goos:     "linux",
			goarch:   "amd64",
			contents: plainContents,
			want: map[string][]byte{
				"plain.go": []byte(plainGoBody),
			},
		},
		{
			name:     "plain-js",
			goos:     "js",
			goarch:   "wasm",
			contents: plainContents,
			want: map[string][]byte{
				"plain.go": []byte(plainGoBody),
			},
		},
		{
			name:     "wasm-linux",
			goos:     "linux",
			goarch:   "amd64",
			contents: jsContents,
			want:     map[string][]byte{},
		},
		{
			name:     "wasm-js",
			goos:     "js",
			goarch:   "wasm",
			contents: jsContents,
			want: map[string][]byte{
				"js.go": []byte(jsGoBody),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := testhelper.ZipContents(test.contents)
			if err != nil {
				t.Fatal(err)
			}
			r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			got, err := matchingFiles(test.goos, test.goarch, r.File)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	modulePath := "github.com/my/module"
	vers := "v1.0.0"
	wantVersionInfo := internal.VersionInfo{
		ModulePath:     "github.com/my/module",
		Version:        "v1.0.0",
		CommitTime:     testProxyCommitTime,
		ReadmeFilePath: "README.md",
		ReadmeContents: "THIS IS A README",
		VersionType:    version.TypeRelease,
		SourceInfo:     source.NewGitHubInfo("https://github.com/my/module", "", "v1.0.0"),
	}
	wantCoverage := sample.LicenseMetadata[0].Coverage
	wantLicenses := []*license.License{
		{
			Metadata: &license.Metadata{
				Types:    []string{"MIT"},
				FilePath: "LICENSE.md",
				Coverage: wantCoverage,
			},
			Contents: testhelper.MITLicense,
		},
	}
	for _, test := range []struct {
		name     string
		contents map[string]string
		want     *internal.Version
	}{
		{
			name: "basic",
			contents: map[string]string{
				"README.md":  "THIS IS A README",
				"foo/foo.go": "// package foo exports a helpful constant.\npackage foo\nimport \"net/http\"\nconst OK = http.StatusOK",
				"LICENSE.md": testhelper.MITLicense,
			},
			want: &internal.Version{
				VersionInfo: wantVersionInfo,
				Packages: []*internal.Package{
					{
						Path:     "github.com/my/module/foo",
						V1Path:   "github.com/my/module/foo",
						Name:     "foo",
						Synopsis: "package foo exports a helpful constant.",
						Licenses: []*license.Metadata{
							{Types: []string{"MIT"}, FilePath: "LICENSE.md", Coverage: wantCoverage},
						},
						Imports: []string{"net/http"},
						GOOS:    "linux",
						GOARCH:  "amd64",
					},
				},
				Licenses: wantLicenses,
			},
		},
		{
			name: "wasm",
			contents: map[string]string{
				"README.md":  "THIS IS A README",
				"LICENSE.md": testhelper.MITLicense,
				"js/js.go": `
					// +build js,wasm

					// Package js only works with wasm.
					package js
					type Value int`,
			},
			want: &internal.Version{
				VersionInfo: wantVersionInfo,
				Packages: []*internal.Package{
					{
						Path:     "github.com/my/module/js",
						V1Path:   "github.com/my/module/js",
						Name:     "js",
						Synopsis: "Package js only works with wasm.",
						Licenses: []*license.Metadata{
							{Types: []string{"MIT"}, FilePath: "LICENSE.md", Coverage: wantCoverage},
						},
						Imports: []string{},
						GOOS:    "js",
						GOARCH:  "wasm",
					},
				},
				Licenses: wantLicenses,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, teardownProxy := proxy.SetupTestProxy(t, []*proxy.TestVersion{
				proxy.NewTestVersion(t, modulePath, vers, test.contents),
			})
			defer teardownProxy()

			got, _, err := FetchVersion(ctx, modulePath, vers, client)
			if err != nil {
				t.Fatal(err)
			}
			opts := []cmp.Option{
				cmpopts.IgnoreFields(internal.Package{}, "DocumentationHTML"),
				cmp.AllowUnexported(source.Info{}),
			}
			opts = append(opts, sample.LicenseCmpOpts...)
			if diff := cmp.Diff(test.want, got, opts...); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFetchAndInsertVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	const goRepositoryURLPrefix = "https://github.com/golang"

	stdlib.UseTestData = true
	defer func() { stdlib.UseTestData = false }()

	myModuleV100 := &internal.VersionedPackage{
		VersionInfo: internal.VersionInfo{
			ModulePath:     "github.com/my/module",
			Version:        "v1.0.0",
			CommitTime:     testProxyCommitTime,
			ReadmeFilePath: "README.md",
			ReadmeContents: "README FILE FOR TESTING.",
			SourceInfo:     source.NewGitHubInfo("https://github.com/my/module", "", "v1.0.0"),
			VersionType:    "release",
		},
		Package: internal.Package{
			Path:              "github.com/my/module/bar",
			Name:              "bar",
			Synopsis:          "package bar",
			DocumentationHTML: "Bar returns the string &#34;bar&#34;.",
			V1Path:            "github.com/my/module/bar",
			Licenses: []*license.Metadata{
				{Types: []string{"BSD-3-Clause"}, FilePath: "LICENSE"},
				{Types: []string{"MIT"}, FilePath: "bar/LICENSE"},
			},
			GOOS:   "linux",
			GOARCH: "amd64",
		},
	}

	testCases := []struct {
		modulePath  string
		version     string
		pkg         string
		want        *internal.VersionedPackage
		dontWantDoc []string // Substrings we expect not to see in DocumentationHTML.
	}{
		{
			modulePath: "github.com/my/module",
			version:    "v1.0.0",
			pkg:        "github.com/my/module/bar",
			want:       myModuleV100,
		},
		{
			modulePath: "github.com/my/module",
			version:    internal.LatestVersion,
			pkg:        "github.com/my/module/bar",
			want:       myModuleV100,
		},
		{
			modulePath: "nonredistributable.mod/module",
			version:    "v1.0.0",
			pkg:        "nonredistributable.mod/module/bar/baz",
			want: &internal.VersionedPackage{
				VersionInfo: internal.VersionInfo{
					ModulePath:     "nonredistributable.mod/module",
					Version:        "v1.0.0",
					CommitTime:     testProxyCommitTime,
					ReadmeFilePath: "README.md",
					ReadmeContents: "README FILE FOR TESTING.",
					VersionType:    "release",
					SourceInfo:     nil,
				},
				Package: internal.Package{
					Path:              "nonredistributable.mod/module/bar/baz",
					Name:              "baz",
					Synopsis:          "package baz",
					DocumentationHTML: "Baz returns the string &#34;baz&#34;.",
					V1Path:            "nonredistributable.mod/module/bar/baz",
					Licenses: []*license.Metadata{
						{Types: []string{"BSD-3-Clause"}, FilePath: "LICENSE"},
						{Types: []string{"MIT"}, FilePath: "bar/LICENSE"},
						{Types: []string{"MIT"}, FilePath: "bar/baz/COPYING"},
					},
					GOOS:   "linux",
					GOARCH: "amd64",
				},
			},
		}, {
			modulePath: "nonredistributable.mod/module",
			version:    "v1.0.0",
			pkg:        "nonredistributable.mod/module/foo",
			want: &internal.VersionedPackage{
				VersionInfo: internal.VersionInfo{
					ModulePath:     "nonredistributable.mod/module",
					Version:        "v1.0.0",
					CommitTime:     testProxyCommitTime,
					ReadmeFilePath: "README.md",
					ReadmeContents: "README FILE FOR TESTING.",
					VersionType:    "release",
					SourceInfo:     nil,
				},
				Package: internal.Package{
					Path:     "nonredistributable.mod/module/foo",
					Name:     "foo",
					Synopsis: "",
					V1Path:   "nonredistributable.mod/module/foo",
					Licenses: []*license.Metadata{
						{Types: []string{"BSD-3-Clause"}, FilePath: "LICENSE"},
						{Types: []string{"BSD-0-Clause"}, FilePath: "foo/LICENSE.md"},
					},
					GOOS:   "linux",
					GOARCH: "amd64",
				},
			},
		}, {
			modulePath: "std",
			version:    "v1.12.5",
			pkg:        "context",
			want: &internal.VersionedPackage{
				VersionInfo: internal.VersionInfo{
					ModulePath:     "std",
					Version:        "v1.12.5",
					CommitTime:     stdlib.TestCommitTime,
					VersionType:    "release",
					ReadmeFilePath: "README.md",
					ReadmeContents: "# The Go Programming Language\n",
					SourceInfo:     source.NewGitHubInfo(goRepositoryURLPrefix+"/go", "src", "go1.12.5"),
				},
				Package: internal.Package{
					Path:              "context",
					Name:              "context",
					Synopsis:          "Package context defines the Context type, which carries deadlines, cancelation signals, and other request-scoped values across API boundaries and between processes.",
					DocumentationHTML: "This example demonstrates the use of a cancelable context to prevent a\ngoroutine leak.",
					V1Path:            "context",
					Licenses: []*license.Metadata{
						{
							Types:    []string{"BSD-3-Clause"},
							FilePath: "LICENSE",
						},
					},
					GOOS:   "linux",
					GOARCH: "amd64",
				},
			},
		}, {
			modulePath: "std",
			version:    "v1.12.5",
			pkg:        "builtin",
			want: &internal.VersionedPackage{
				VersionInfo: internal.VersionInfo{
					ModulePath:     "std",
					Version:        "v1.12.5",
					CommitTime:     stdlib.TestCommitTime,
					VersionType:    "release",
					ReadmeFilePath: "README.md",
					ReadmeContents: "# The Go Programming Language\n",
					SourceInfo:     source.NewGitHubInfo(goRepositoryURLPrefix+"/go", "src", "go1.12.5"),
				},
				Package: internal.Package{
					Path:              "builtin",
					Name:              "builtin",
					Synopsis:          "Package builtin provides documentation for Go's predeclared identifiers.",
					DocumentationHTML: "int64 is the set of all signed 64-bit integers.",
					V1Path:            "builtin",
					Licenses: []*license.Metadata{
						{
							Types:    []string{"BSD-3-Clause"},
							FilePath: "LICENSE",
						},
					},
					GOOS:   "linux",
					GOARCH: "amd64",
				},
			},
		}, {
			modulePath: "build.constraints/module",
			version:    "v1.0.0",
			pkg:        "build.constraints/module/cpu",
			want: &internal.VersionedPackage{
				VersionInfo: internal.VersionInfo{
					ModulePath:  "build.constraints/module",
					Version:     "v1.0.0",
					CommitTime:  testProxyCommitTime,
					VersionType: "release",
					SourceInfo:  nil,
				},
				Package: internal.Package{
					Path:              "build.constraints/module/cpu",
					Name:              "cpu",
					Synopsis:          "Package cpu implements processor feature detection used by the Go standard library.",
					DocumentationHTML: "const CacheLinePadSize = 3",
					V1Path:            "build.constraints/module/cpu",
					Licenses: []*license.Metadata{
						{Types: []string{"BSD-3-Clause"}, FilePath: "LICENSE"},
					},
					GOOS:   "linux",
					GOARCH: "amd64",
				},
			},
			dontWantDoc: []string{
				"const CacheLinePadSize = 1",
				"const CacheLinePadSize = 2",
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.pkg, func(t *testing.T) {
			defer postgres.ResetTestDB(testDB, t)

			client, teardownProxy := proxy.SetupTestProxy(t, nil)
			defer teardownProxy()

			if _, err := FetchAndInsertVersion(ctx, test.modulePath, test.version, client, testDB); err != nil {
				t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v): %v", test.modulePath, test.version, client, testDB, err)
			}

			gotVersionInfo, err := testDB.GetVersionInfo(ctx, test.modulePath, test.version)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.want.VersionInfo, *gotVersionInfo, cmp.AllowUnexported(source.Info{})); diff != "" {
				t.Fatalf("testDB.GetVersionInfo(ctx, %q, %q) mismatch (-want +got):\n%s", test.modulePath, test.version, diff)
			}

			gotPkg, err := testDB.GetPackage(ctx, test.pkg, internal.UnknownModulePath, test.version)
			if err != nil {
				t.Fatal(err)
			}

			sort.Slice(gotPkg.Licenses, func(i, j int) bool {
				return gotPkg.Licenses[i].FilePath < gotPkg.Licenses[j].FilePath
			})
			if diff := cmp.Diff(test.want, gotPkg, cmpopts.IgnoreFields(internal.Package{}, "DocumentationHTML"), cmp.AllowUnexported(source.Info{})); diff != "" {
				t.Errorf("testDB.GetPackage(ctx, %q, %q) mismatch (-want +got):\n%s", test.pkg, test.version, diff)
			}
			if got, want := gotPkg.DocumentationHTML, test.want.DocumentationHTML; len(want) == 0 && len(got) != 0 {
				t.Errorf("got non-empty documentation but want empty:\ngot: %q\nwant: %q", got, want)
			} else if !strings.Contains(got, want) {
				t.Errorf("got documentation doesn't contain wanted documentation substring:\ngot: %q\nwant (substring): %q", got, want)
			}
			for _, dontWant := range test.dontWantDoc {
				if got := gotPkg.DocumentationHTML; strings.Contains(got, dontWant) {
					t.Errorf("got documentation contains unwanted documentation substring:\ngot: %q\ndontWant (substring): %q", got, dontWant)
				}
			}
		})
	}
}

func TestFetchAndInsertVersionTimeout(t *testing.T) {
	defer postgres.ResetTestDB(testDB, t)

	defer func(oldTimeout time.Duration) {
		fetchTimeout = oldTimeout
	}(fetchTimeout)
	fetchTimeout = 0

	client, teardownProxy := proxy.SetupTestProxy(t, nil)
	defer teardownProxy()

	name := "my.mod/version"
	version := "v1.0.0"
	wantErrString := "deadline exceeded"
	_, err := FetchAndInsertVersion(context.Background(), name, version, client, testDB)
	if err == nil || !strings.Contains(err.Error(), wantErrString) {
		t.Fatalf("FetchAndInsertVersion(%q, %q, %v, %v) returned error %v, want error containing %q",
			name, version, client, testDB, err, wantErrString)
	}
}

func TestHasFilename(t *testing.T) {
	for _, test := range []struct {
		file         string
		expectedFile string
		want         bool
	}{
		{
			file:         "github.com/my/module@v1.0.0/README.md",
			expectedFile: "README.md",
			want:         true,
		},
		{
			file:         "rEaDme",
			expectedFile: "README",
			want:         true,
		}, {
			file:         "README.FOO",
			expectedFile: "README",
			want:         true,
		},
		{
			file:         "FOO_README",
			expectedFile: "README",
			want:         false,
		},
		{
			file:         "README_FOO",
			expectedFile: "README",
			want:         false,
		},
		{
			file:         "README.FOO.FOO",
			expectedFile: "README",
			want:         false,
		},
		{
			file:         "",
			expectedFile: "README",
			want:         false,
		},
		{
			file:         "github.com/my/module@v1.0.0/LICENSE",
			expectedFile: "github.com/my/module@v1.0.0/LICENSE",
			want:         true,
		},
	} {
		{
			t.Run(test.file, func(t *testing.T) {
				got := hasFilename(test.file, test.expectedFile)
				if got != test.want {
					t.Errorf("hasFilename(%q, %q) = %t: %t", test.file, test.expectedFile, got, test.want)
				}
			})
		}
	}
}

func TestExtractReadmeFromZip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for _, test := range []struct {
		name, version, file, wantPath string
		wantContents                  string
		err                           error
	}{
		{
			name:         "github.com/my/module",
			version:      "v1.0.0",
			file:         "github.com/my/module@v1.0.0/README.md",
			wantPath:     "README.md",
			wantContents: "README FILE FOR TESTING.",
		},
		{
			name:    "emp.ty/module",
			version: "v1.0.0",
			err:     errReadmeNotFound,
		},
	} {
		t.Run(test.file, func(t *testing.T) {
			client, teardownProxy := proxy.SetupTestProxy(t, nil)
			defer teardownProxy()

			reader, err := client.GetZip(ctx, test.name, test.version)
			if err != nil {
				t.Fatal(err)
			}

			gotPath, gotContents, err := extractReadmeFromZip(test.name, test.version, reader)
			if err != nil {
				if test.err == nil || test.err.Error() != err.Error() {
					t.Errorf("extractFile(%q, %q): \n %v, want \n %v",
						fmt.Sprintf("%q %q", test.name, test.version), filepath.Base(test.file), err, test.err)
				} else {
					return
				}
			}

			if test.wantPath != gotPath {
				t.Errorf("extractFile(%q, %q) path = %q, want %q", test.name, test.file, gotPath, test.wantPath)
			}
			if test.wantContents != gotContents {
				t.Errorf("extractFile(%q, %q) contents = %q, want %q", test.name, test.file, gotContents, test.wantContents)
			}
		})
	}
}

func TestExtractPackagesFromZip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for _, test := range []struct {
		name                  string
		version               string
		packages              map[string]*internal.Package
		HasIncompletePackages bool
	}{
		{
			name:    "github.com/my/module",
			version: "v1.0.0",
			packages: map[string]*internal.Package{
				"foo": {
					Name:              "foo",
					Path:              "github.com/my/module/foo",
					Synopsis:          "package foo",
					DocumentationHTML: "FooBar returns the string &#34;foo bar&#34;.",
					Imports:           []string{"fmt", "github.com/my/module/bar"},
					V1Path:            "github.com/my/module/foo",
					GOOS:              "linux",
					GOARCH:            "amd64",
				},
				"bar": {
					Name:              "bar",
					Path:              "github.com/my/module/bar",
					Synopsis:          "package bar",
					DocumentationHTML: "Bar returns the string &#34;bar&#34;.",
					Imports:           []string{},
					V1Path:            "github.com/my/module/bar",
					GOOS:              "linux",
					GOARCH:            "amd64",
				},
			},
		},
		{
			name:    "no.mod/module",
			version: "v1.0.0",
			packages: map[string]*internal.Package{
				"p": {
					Name:              "p",
					Path:              "no.mod/module/p",
					Synopsis:          "Package p is inside a module where a go.mod file hasn't been explicitly added yet.",
					DocumentationHTML: "const Year = 2009",
					Imports:           []string{},
					V1Path:            "no.mod/module/p",
					GOOS:              "linux",
					GOARCH:            "amd64",
				},
			},
		},
		{
			name:     "emp.ty/module",
			version:  "v1.0.0",
			packages: map[string]*internal.Package{},
		},
		{
			name:    "emp.ty/package",
			version: "v1.0.0",
			packages: map[string]*internal.Package{
				"main": {
					Name:     "main",
					Path:     "emp.ty/package",
					Synopsis: "",
					Imports:  []string{},
					V1Path:   "emp.ty/package",
					GOOS:     "linux",
					GOARCH:   "amd64",
				},
			},
		},
		{
			name:    "bad.mod/module",
			version: "v1.0.0",
			packages: map[string]*internal.Package{
				"good": {
					Name:              "good",
					Path:              "bad.mod/module/good",
					Synopsis:          "Package good is inside a module that has bad packages.",
					DocumentationHTML: `const Good = <a href="/pkg/builtin#true">true</a>`,
					Imports:           []string{},
					V1Path:            "bad.mod/module/good",
					GOOS:              "linux",
					GOARCH:            "amd64",
				},
			},
		},
		{
			name:    "build.constraints/module",
			version: "v1.0.0",
			packages: map[string]*internal.Package{
				"cpu": {
					Name:              "cpu",
					Path:              "build.constraints/module/cpu",
					Synopsis:          "Package cpu implements processor feature detection used by the Go standard library.",
					DocumentationHTML: "const CacheLinePadSize = 3",
					Imports:           []string{},
					V1Path:            "build.constraints/module/cpu",
					GOOS:              "linux",
					GOARCH:            "amd64",
				},
			},
			HasIncompletePackages: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, teardownProxy := proxy.SetupTestProxy(t, nil)
			defer teardownProxy()

			reader, err := client.GetZip(ctx, test.name, test.version)
			if err != nil {
				t.Fatal(err)
			}

			packages, HasIncompletePackages, err := extractPackagesFromZip(context.Background(), test.name, test.version, reader, nil, nil)
			if err != nil && len(test.packages) != 0 {
				t.Fatalf("extractPackagesFromZip(%q, %q, reader, nil): %v", test.name, test.version, err)
			}

			if HasIncompletePackages != test.HasIncompletePackages {
				t.Fatalf("extractPackagesFromZip(%q, %q, reader, nil): HasIncompletePackages=%t, want %t",
					test.name, test.version, HasIncompletePackages, test.HasIncompletePackages)
			}

			for _, got := range packages {
				want, ok := test.packages[got.Name]
				if !ok {
					t.Errorf("extractPackagesFromZip(%q, %q, reader, nil) returned unexpected package: %q", test.name, test.version, got.Name)
					continue
				}

				sort.Strings(got.Imports)

				if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(internal.Package{}, "DocumentationHTML")); diff != "" {
					t.Errorf("extractPackagesFromZip(%q, %q, reader, nil) mismatch (-want +got):\n%s", test.name, test.version, diff)
				}

				if got, want := got.DocumentationHTML, want.DocumentationHTML; len(want) == 0 && len(got) != 0 {
					t.Errorf("got non-empty documentation but want empty:\ngot: %q\nwant: %q", got, want)
				} else if !strings.Contains(got, want) {
					t.Errorf("got documentation doesn't contain wanted documentation substring:\ngot: %q\nwant (substring): %q", got, want)
				}
			}
		})
	}
}

func TestFetch_parseVersionType(t *testing.T) {
	testCases := []struct {
		name, version   string
		wantVersionType version.Type
		wantErr         bool
	}{
		{
			name:            "pseudo major version",
			version:         "v1.0.0-20190311183353-d8887717615a",
			wantVersionType: version.TypePseudo,
		},
		{
			name:            "pseudo prerelease version",
			version:         "v1.2.3-pre.0.20190311183353-d8887717615a",
			wantVersionType: version.TypePseudo,
		},
		{
			name:            "pseudo minor version",
			version:         "v1.2.4-0.20190311183353-d8887717615a",
			wantVersionType: version.TypePseudo,
		},
		{
			name:            "pseudo version invalid",
			version:         "v1.2.3-20190311183353-d8887717615a",
			wantVersionType: version.TypePrerelease,
		},
		{
			name:            "valid release",
			version:         "v1.0.0",
			wantVersionType: version.TypeRelease,
		},
		{
			name:            "valid prerelease",
			version:         "v1.0.0-alpha.1",
			wantVersionType: version.TypePrerelease,
		},
		{
			name:            "invalid version",
			version:         "not_a_version",
			wantVersionType: "",
			wantErr:         true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if gotVt, err := version.ParseType(tc.version); (tc.wantErr == (err != nil)) && tc.wantVersionType != gotVt {
				t.Errorf("parseVersionType(%v) = %v, want %v", tc.version, gotVt, tc.wantVersionType)
			}
		})
	}
}