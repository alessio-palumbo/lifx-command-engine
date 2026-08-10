// Package modelcatalog defines optional models known to the engine tooling.
package modelcatalog

import "fmt"

type Model struct {
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	Source      string `json:"source"`
	Handle      string `json:"handle"`
	Revision    string `json:"revision"`
	ApproxBytes int64  `json:"approx_bytes,omitempty"`
	Runtime     string `json:"runtime"`
	LicenseURL  string `json:"license_url"`
}

var models = []Model{{
	Name:       "functiongemma-270m-it",
	Purpose:    "optional natural-language command fallback",
	Source:     "kaggle",
	Handle:     "google/functiongemma/transformers/functiongemma-270m-it/1",
	Revision:   "1",
	Runtime:    "runtimes/functiongemma/runner.py",
	LicenseURL: "https://ai.google.dev/gemma/terms",
}}

func List() []Model { return append([]Model(nil), models...) }

func Find(name, source string) (Model, error) {
	for _, model := range models {
		if model.Name == name && model.Source == source {
			return model, nil
		}
	}
	return Model{}, fmt.Errorf("unknown model %q from source %q", name, source)
}
