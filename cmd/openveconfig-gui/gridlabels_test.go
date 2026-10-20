package main

import (
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
)

// TestGridLabels checks that every gridcode name the grid page shows has a
// label, so that none is left untranslated.
func TestGridLabels(t *testing.T) {
	labels := gridLabels()
	need := func(where, name string) {
		t.Helper()
		if _, ok := labels[name]; !ok {
			t.Errorf("%s: no label for %q", where, name)
		}
	}
	// The pages as each grid code shows them, with the reactive-power
	// group of every layout.
	pages := gridcode.Pages()
	for _, ci := range gridcode.Codes() {
		for _, m := range []gridcode.Model{{}, gridcode.TableModel(nil)} {
			if p, _ := gridcode.PageOf(ci.Code, m); p != nil {
				pages = append(pages, p)
			}
		}
	}
	for _, p := range pages {
		need("page", p.Name)
		if p.Title != "" {
			need("page", p.Title)
		}
		for _, prm := range p.Params {
			need(p.Name, prm.Name)
			if prm.Group != "" {
				need(p.Name, prm.Group)
			}
			if strings.Contains(prm.Unit, " ") {
				need(p.Name, prm.Unit)
			}
			for _, c := range prm.Choices {
				need(p.Name, c)
			}
		}
		for _, n := range p.Unsupported {
			need(p.Name, n)
		}
	}
	for _, ci := range gridcode.Codes() {
		name := ci.Region
		if name == "" {
			name = ci.Description
		}
		if _, ok := gridCodeLabels()[name]; !ok {
			t.Errorf("grid code %d: no label for %q", ci.Code, name)
		}
	}
}
