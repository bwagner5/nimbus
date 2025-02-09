package amis

import (
	"fmt"

	"github.com/bwagner5/vm/pkg/selectors"
)

type Selector struct {
	Tags map[string]string
	Name string
	ID   string
}

type AMI struct {
}

func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectors(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AMI selectors: %w", err)
	}
	amiSelectors := make([]Selector, len(selectors))
	for i, selector := range selectors {
		amiSelectors[i] = Selector{
			Tags: selector.Tags,
			Name: selector.Name,
			ID:   selector.ID,
		}
	}
	return amiSelectors, nil
}
