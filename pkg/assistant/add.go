package assistant

import (
	"fmt"
	"maps"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
)

// ErrAlreadyThere means the image already holds the assistant.
var ErrAlreadyThere = fmt.Errorf("assistant: the image already holds this assistant")

// Add returns the image that holds what img holds and, after it, the
// assistant with id answered with answers. The assistants already there
// keep what the image gives them: their answers are read back from it
// first, so adding one does not reconfigure the rest. dir, lang and
// labels are as for Load and Composer.Labels.
//
// It fails with ErrTooBig, before making an image, when the assistants no
// longer fit.
func Add(dir, lang string, img []byte, id uint16, labels map[string]asm.Value, answers wizard.Answers) (*Image, error) {
	l, err := ParseLayout(img)
	if err != nil {
		return nil, err
	}
	old := l.Users()
	for _, o := range old {
		if o == id {
			return nil, ErrAlreadyThere
		}
	}
	all := wizard.Answers{}
	if len(old) > 0 {
		c, err := Load(dir, lang, old...)
		if err != nil {
			return nil, err
		}
		c.Labels = labels
		prev, _, err := c.ReadBack(img)
		if err != nil {
			return nil, fmt.Errorf("assistant: reading the assistants already in the file: %w", err)
		}
		maps.Copy(all, prev)
	}
	maps.Copy(all, answers)

	c, err := Load(dir, lang, append(append([]uint16(nil), old...), id)...)
	if err != nil {
		return nil, err
	}
	c.Labels = labels
	return c.Build(all)
}
