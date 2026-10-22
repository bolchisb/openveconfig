package assistant

import (
	"fmt"
	"maps"
	"slices"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
)

// ErrAlreadyThere means the image already holds the assistant.
var ErrAlreadyThere = fmt.Errorf("assistant: the image already holds this assistant")

// ErrNotThere means the image does not hold the assistant.
var ErrNotThere = fmt.Errorf("assistant: the image does not hold this assistant")

// answersIn reads back what the assistants of img were answered with, so
// that rebuilding the image does not reconfigure the ones left alone.
// ids are the image's user assistants, in program order.
func answersIn(dir, lang string, img []byte, ids []uint16, labels map[string]asm.Value) (wizard.Answers, error) {
	all := wizard.Answers{}
	if len(ids) == 0 {
		return all, nil
	}
	c, err := Load(dir, lang, ids...)
	if err != nil {
		return nil, err
	}
	c.Labels = labels
	prev, _, err := c.ReadBack(img)
	if err != nil {
		return nil, fmt.Errorf("assistant: reading the assistants already in the file: %w", err)
	}
	maps.Copy(all, prev)
	return all, nil
}

// Replace returns the image that holds what img holds, with the answers
// of the assistant with id replaced by answers. The others keep theirs,
// and the order of the assistants does not change. dir, lang and labels
// are as for Load and Composer.Labels.
//
// It fails with ErrNotThere when the image does not hold the assistant,
// and with ErrTooBig, before making an image, when the new answers no
// longer fit.
func Replace(dir, lang string, img []byte, id uint16, labels map[string]asm.Value, answers wizard.Answers) (*Image, error) {
	l, err := ParseLayout(img)
	if err != nil {
		return nil, err
	}
	ids := l.Users()
	if !slices.Contains(ids, id) {
		return nil, ErrNotThere
	}
	all, err := answersIn(dir, lang, img, ids, labels)
	if err != nil {
		return nil, err
	}
	maps.Copy(all, answers)

	c, err := Load(dir, lang, ids...)
	if err != nil {
		return nil, err
	}
	c.Labels = labels
	return c.Build(all)
}

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
	all, err := answersIn(dir, lang, img, old, labels)
	if err != nil {
		return nil, err
	}
	maps.Copy(all, answers)

	c, err := Load(dir, lang, append(append([]uint16(nil), old...), id)...)
	if err != nil {
		return nil, err
	}
	c.Labels = labels
	return c.Build(all)
}
