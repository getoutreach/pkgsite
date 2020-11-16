// Copyright 2019-2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package middleware

import (
	"fmt"
	"net/http"
	"strings"
)

var scriptHashes = []string{
	// From content/static/html/base.tmpl
	"'sha256-CgM7SjnSbDyuIteS+D1CQuSnzyKwL0qtXLU6ZW2hB+g='",
	"'sha256-qPGTOKPn+niRiNKQIEX0Ktwuj+D+iPQWIxnlhPicw58='",
	"'sha256-LIQd8c4GSueKwR3q2fz3AB92cOdy2Ld7ox8pfvMPHns='",
	"'sha256-dwce5DnVX7uk6fdvvNxQyLTH/cJrTMDK6zzrdKwdwcg='",
	// From content/static/html/pages/badge.tmpl
	"'sha256-T7xOt6cgLji3rhOWyKK7t5XKv8+LASQwOnHiHHy8Kwk='",
	// From content/static/html/pages/details.tmpl
	"'sha256-EWdCQW4XtY7zS2MZgs76+2EhMbqpaPtC+9EPGnbHBtM='",
	// From content/static/html/pages/fetch.tmpl
	"'sha256-1J6DWwTWs/QDZ2+ORDuUQCibmFnXXaNXYOtc0Jk6VU4='",
	// From content/static/html/worker/index.tmpl
	"'sha256-y5EX2GR3tCwSK0/kmqZnsWVeBROA8tA75L+I+woljOE='",
	// From content/static/html/pages/pkg_doc.tmpl
	"'sha256-91GG/273d2LdEV//lJMbTodGN501OuKZKYYphui+wDQ='",
	"'sha256-ABETDefmLMyKpLsjAartd0H1SHvPVqmVWv6841qII1U='",
	"'sha256-uQODpjQEw2CWPIl6zEmpUU1uULk5RYVCofnBw59UOOw='",
	// From content/static/html/pages/unit.tmpl
	"'sha256-hsHIJwO1h0Vzwa75j0l07kUfQ7MEZGI/HlSPB/8leZ0='",
	// From content/static/html/pages/unit_details.tmpl
	"'sha256-CFun5NgnYeEpye8qcbQPq5Ycwavi4IXuZiIzSMNqRUw='",
	"'sha256-IHdniK/yZ8URNA2OYbc4R7BssOAe3/dFrSQW7PxEEfM='",
	"'sha256-MBIVDkCvJUTM2/rxXDRYO9B+ovOUGLVJOww8fxur+LU='",
}

// SecureHeaders adds a content-security-policy and other security-related
// headers to all responses.
func SecureHeaders(enableCSP bool) Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			csp := []string{
				// Disallow plugin content: pkg.go.dev does not use it.
				"object-src 'none'",
				// Disallow <base> URIs, which prevents attackers from changing the
				// locations of scripts loaded from relative URLs. The site doesn’t have
				// a <base> tag anyway.
				"base-uri 'none'",
				fmt.Sprintf("script-src 'unsafe-inline' 'strict-dynamic' https: http: %s",
					strings.Join(scriptHashes, " ")),
			}
			if enableCSP {
				w.Header().Set("Content-Security-Policy", strings.Join(csp, "; "))
			}
			// Don't allow frame embedding.
			w.Header().Set("X-Frame-Options", "deny")
			// Prevent MIME sniffing.
			w.Header().Set("X-Content-Type-Options", "nosniff")

			h.ServeHTTP(w, r)
		})
	}
}
