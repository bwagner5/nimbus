package subnets

import (
	"fmt"

	"github.com/bwagner5/vm/pkg/selectors"
)

type Selector struct {
	Tags map[string]string
	Name string
	ID   string
}

type Subnet struct {
}

func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectors(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subnet selectors: %w", err)
	}
	subnetSelectors := make([]Selector, len(selectors))
	for i, selector := range selectors {
		subnetSelectors[i] = Selector{
			Tags: selector.Tags,
			Name: selector.Name,
			ID:   selector.ID,
		}
	}
	return subnetSelectors, nil
}
