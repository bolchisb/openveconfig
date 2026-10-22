package assistant_test

// How far every assistant in a package folder gets: the sweep behind the
// supported-assistants list in the README (#32). Packages are the user's
// own and are never committed, so this is skipped unless both are given:
//
//	ASSISTANT_PACKAGES  the package folder
//	ASSISTANT_RVSC      a settings file of the unit to compile for

import (
	"os"
	"sort"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
)

// reach is how far one assistant got.
type reach struct {
	title string
	id    uint16
	steps int    // steps walked before it stopped or ended
	built bool   // an image was built from the answers
	err   string // why it stopped, if it did
}

// TestRealPackagesReach walks every assistant of the folder with the
// default answer at each step and tries to build the result, and prints
// one line per assistant. It does not fail on an assistant that stops:
// the point is the list, and a package this program cannot work through
// yet is a known gap, not a broken test.
func TestRealPackagesReach(t *testing.T) {
	dir, file := os.Getenv("ASSISTANT_PACKAGES"), os.Getenv("ASSISTANT_RVSC")
	if dir == "" || file == "" {
		t.Skip("ASSISTANT_PACKAGES and ASSISTANT_RVSC not set")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rvsc.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil || len(units) == 0 {
		t.Fatalf("units: %v", err)
	}
	list, bad, err := vap.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range bad {
		t.Logf("package %s cannot be read: %v", name, e)
	}
	labels := assistant.Labels(essDevice(t, dir, f, units[0]))

	var out []reach
	for name, in := range vap.Latest(list) {
		r := reach{title: name, id: in.ID}
		c, err := assistant.Load(dir, "en", in.ID)
		if err != nil {
			r.err = err.Error()
			out = append(out, r)
			continue
		}
		c.Labels = labels
		s := c.Program().NewSession(nil)
		s.Edit = c.Users[0].Name
		st, err := s.Start()
		for err == nil && st != nil && r.steps < 60 {
			r.steps++
			var vals []asm.Value
			n := 0
			for i := range st.Labels {
				switch {
				case st.Type != 'E' && i == 0:
					vals = append(vals, asm.IntVal(int64(st.Default)))
				case n < len(st.Formats):
					vals = append(vals, asm.IntVal(st.Formats[n].Default))
					n++
				default:
					v := asm.Value{}
					if i < len(st.Values) {
						v = st.Values[i]
					}
					vals = append(vals, v)
				}
			}
			st, err = s.Next(vals)
		}
		if err != nil {
			r.err = err.Error()
		} else if _, err := c.Build(s.Answers()); err != nil {
			r.err = "build: " + err.Error()
		} else {
			r.built = true
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].title < out[j].title })

	walked, built := 0, 0
	for _, r := range out {
		if r.steps > 0 {
			walked++
		}
		if r.built {
			built++
		}
		state := "builds an image"
		if !r.built {
			state = "stops: " + r.err
		}
		t.Logf("%-34s 0x%04X  %2d steps  %s", r.title, r.id, r.steps, state)
	}
	t.Logf("%d assistants: %d reach their first step, %d build an image", len(out), walked, built)
}
