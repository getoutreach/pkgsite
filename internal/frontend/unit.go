// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package frontend

import (
	"context"
	"net/http"

	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/derrors"
	"golang.org/x/pkgsite/internal/middleware"
)

// UnitPage contains data needed to render the unit template.
type UnitPage struct {
	basePage
	// Unit is the unit for this page.
	Unit *internal.UnitMeta

	// Breadcrumb contains data used to render breadcrumb UI elements.
	Breadcrumb breadcrumb

	// Title is the title of the page.
	Title string

	// URLPath is the path suitable for links on the page.
	URLPath string

	// CanonicalURLPath is the representation of the URL path for the details
	// page, after the requested version and module path have been resolved.
	// For example, if the latest version of /my.module/pkg is version v1.5.2,
	// the canonical url for that path would be /my.module@v1.5.2/pkg
	CanonicalURLPath string

	// The version string formatted for display.
	DisplayVersion string

	// LinkVersion is version string suitable for links used to compute
	// latest badges.
	LinkVersion string

	// LatestURL is a url pointing to the latest version of a unit.
	LatestURL string

	// PageType is the type of page (pkg, cmd, dir, std, or mod).
	PageType string

	// PageLabels are the labels that will be displayed
	// for a given page.
	PageLabels []string

	// CanShowDetails indicates whether details can be shown or must be
	// hidden due to issues like license restrictions.
	CanShowDetails bool

	// Tabs contains data to render the varioius tabs on each details page.
	Tabs []TabSettings

	// Settings contains settings for the selected tab.
	SelectedTab TabSettings

	// Details contains data specific to the type of page being rendered.
	Details interface{}
}

// serveUnitPage serves a unit page for a path using the paths,
// modules, documentation, readmes, licenses, and package_imports tables.
func (s *Server) serveUnitPage(ctx context.Context, w http.ResponseWriter, r *http.Request,
	ds internal.DataSource, um *internal.UnitMeta, requestedVersion string) (err error) {
	defer derrors.Wrap(&err, "serveUnitPage(ctx, w, r, ds, %v, %q)", um, requestedVersion)

	tab := r.FormValue("tab")
	if tab == "" {
		// Default to details tab when there is no tab param.
		tab = tabMain
	}
	tabSettings, ok := unitTabLookup[tab]
	if !ok || tab == tabLicenses && !um.IsRedistributable {
		// Redirect to clean URL path when tab param is invalid.
		// If the path is not redistributable, licenses is an invalid tab.
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
		return nil
	}

	title := pageTitle(um)
	basePage := s.newBasePage(r, title)
	basePage.AllowWideContent = true
	page := UnitPage{
		basePage:    basePage,
		Unit:        um,
		Breadcrumb:  displayBreadcrumb(um, requestedVersion),
		Title:       title,
		Tabs:        unitTabs,
		SelectedTab: tabSettings,
		URLPath: constructPackageURL(
			um.Path,
			um.ModulePath,
			requestedVersion,
		),
		CanonicalURLPath: constructPackageURL(
			um.Path,
			um.ModulePath,
			linkVersion(um.Version, um.ModulePath),
		),
		DisplayVersion: displayVersion(um.Version, um.ModulePath),
		LinkVersion:    linkVersion(um.Version, um.ModulePath),
		LatestURL:      constructPackageURL(um.Path, um.ModulePath, middleware.LatestMinorVersionPlaceholder),
		PageLabels:     pageLabels(um),
		PageType:       pageType(um),
	}
	d, err := fetchDetailsForUnit(r, tab, ds, um)
	if err != nil {
		return err
	}
	page.Details = d
	s.servePage(ctx, w, tabSettings.TemplateName, page)
	return nil
}
