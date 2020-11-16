// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package internal contains data used through x/pkgsite.
package internal

const (
	ExperimentAutocomplete        = "autocomplete"
	ExperimentFasterDecoding      = "faster-decoding"
	ExperimentFrontendRenderDoc   = "frontend-render-doc"
	ExperimentGetUnitWithOneQuery = "get-unit-with-one-query"
	ExperimentGoldmark            = "goldmark"
	ExperimentRemoveUnusedAST     = "remove-unused-ast"
	ExperimentUnitPage            = "unit-page"
)

// Experiments represents all of the active experiments in the codebase and
// a description of each experiment.
var Experiments = map[string]string{
	ExperimentAutocomplete:        "Enable autocomplete with search.",
	ExperimentFasterDecoding:      "Decode ASTs faster.",
	ExperimentFrontendRenderDoc:   "Render documentation on the frontend if possible.",
	ExperimentGetUnitWithOneQuery: "Fetch data for GetUnit using a single query.",
	ExperimentGoldmark:            "Enable the usage of rendering markdown using goldmark instead of blackfriday.",
	ExperimentRemoveUnusedAST:     "Prune AST prior to rendering documentation HTML.",
	ExperimentUnitPage:            "Enable the redesigned details page.",
}

// Experiment holds data associated with an experimental feature for frontend
// or worker.
type Experiment struct {
	// This struct is used to decode dynamic config (see
	// internal/config/dynconfig). Make sure that changes to this struct are
	// coordinated with the deployment of config files.

	// Name is the name of the feature.
	Name string

	// Rollout is the percentage of requests enrolled in the experiment.
	Rollout uint

	// Description provides a description of the experiment.
	Description string
}
