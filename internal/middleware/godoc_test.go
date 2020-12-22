// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package middleware

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGodocURL(t *testing.T) {
	mw := GodocURL()
	mwh := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := []byte(`<a href="$$GODISCOVERY_GODOCURL$$">godoc</a>`)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("w.Write(%q) = %v", body, err)
		}
	}))

	testCases := []struct {
		desc string

		// Request values
		path    string
		cookies map[string]string

		// Response values
		code    int
		body    []byte
		headers map[string]string
	}{
		{
			desc: "Unaffected request",
			path: "/cloud.google.com/go/storage",
			code: http.StatusOK,
			body: []byte(`<a href="">godoc</a>`),
		},
		{
			desc: "Strip utm_source, set temporary cookie, and redirect",
			path: "/cloud.google.com/go/storage?utm_source=godoc",
			code: http.StatusFound,
			headers: map[string]string{
				"Location":   "/cloud.google.com/go/storage",
				"Set-Cookie": "tmp-from-godoc=1; SameSite=Lax",
			},
		},
		{
			desc: "Delete temporary cookie; godoc URL should be set",
			path: "/cloud.google.com/go/storage",
			cookies: map[string]string{
				"tmp-from-godoc": "1",
			},
			code: http.StatusOK,
			body: []byte(`<a href="https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc">godoc</a>`),
			headers: map[string]string{
				"Set-Cookie": "tmp-from-godoc=; Max-Age=0",
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			req := httptest.NewRequest("GET", test.path, nil)
			for k, v := range test.cookies {
				req.AddCookie(&http.Cookie{
					Name:  k,
					Value: v,
				})
			}
			w := httptest.NewRecorder()
			mwh.ServeHTTP(w, req)
			resp := w.Result()
			defer resp.Body.Close()
			if got, want := resp.StatusCode, test.code; got != want {
				t.Errorf("Status code = %d; want %d", got, want)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				body, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("ioutil.ReadAll(resp.Body) = %v", err)
				}
				if got, want := body, test.body; !bytes.Equal(got, want) {
					t.Errorf("Response body = %q; want %q", got, want)
				}
			}
			for k, v := range test.headers {
				if _, ok := resp.Header[k]; !ok {
					t.Errorf("%q not present in response headers", k)
					continue
				}
				if got, want := resp.Header.Get(k), v; got != want {
					t.Errorf("Response header mismatch for %q: got %q; want %q", k, got, want)
				}
			}
		})
	}
}

func TestGodoc(t *testing.T) {
	testCases := []struct {
		from, to string
	}{
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage",
			to:   "https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=overview",
			to:   "https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=versions",
			to:   "https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=licenses",
			to:   "https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=subdirectories",
			to:   "https://godoc.org/cloud.google.com/go/storage?utm_source=backtogodoc#pkg-subdirectories",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=imports",
			to:   "https://godoc.org/cloud.google.com/go/storage?imports=&utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/cloud.google.com/go/storage?tab=importedby",
			to:   "https://godoc.org/cloud.google.com/go/storage?importers=&utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/std?tab=packages",
			to:   "https://godoc.org/-/go?utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/search?q=foo",
			to:   "https://godoc.org/?q=foo&utm_source=backtogodoc",
		},
		{
			from: "https://pkg.go.dev/about",
			to:   "https://godoc.org/-/about?utm_source=backtogodoc",
		},
	}

	for _, test := range testCases {
		u, err := url.Parse(test.from)
		if err != nil {
			t.Errorf("url.Parse(%q): %v", test.from, err)
			continue
		}
		to := godoc(u)
		if got, want := to, test.to; got != want {
			t.Errorf("godocURL(%q) = %q; want %q", u, got, want)
		}
	}
}
