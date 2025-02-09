package selectors

import (
	"fmt"
	"strings"
)

type Selector struct {
	Tags map[string]string
	Name string
	ID   string
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
// Selectors are parsed as a set of terms. Each term is separated by a semicolon.
// Terms are AND'd together.
// Within a term, individual selection criteria is separated by a comma. Criteria are OR'd together.
//
// Example:
//
// "tag:Name=fancyOS,tag:Environment=dev;id:ami-0123456"
//
// This will parse into two selectors:
//  1. tag:Name=fancyOS,tag:Environment=dev (AND'd together, so the resource must have both tags)
//  2. id:resource-0123456 (OR'd together, so the resource must have the given ID)
//
// The resources selected will be the given resource ID and resources that have both tags "Name=fancyOS" and "Environment=dev"
func ParseSelectors(selectors string) ([]*Selector, error) {
	selectors = strings.TrimSpace(selectors)
	var parsedSelectors []*Selector
	selectorTerms := strings.Split(selectors, ";")
	for _, term := range selectorTerms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		selector := Selector{
			Tags: make(map[string]string),
		}
		components := strings.Split(term, ",")
		for _, s := range components {
			switch {
			case strings.HasPrefix(strings.ToLower(s), "tag:"):
				tokens := strings.Split(s, ":")
				if len(tokens) != 2 {
					return nil, fmt.Errorf("invalid tag selector: %s. Expected 1 \":\", but found %d", s, len(tokens)-1)
				}
				tagKeyValue := tokens[1]
				tagTokens := strings.Split(tagKeyValue, "=")
				if len(tagTokens) > 2 {
					return nil, fmt.Errorf("invalid tag selector: %s. Expected 0 or 1 \"=\", but found %d", tagKeyValue, len(tagTokens)-1)
				}
				// if only the tag key was given, then we set the value to the empty string and use it as a wildcard
				if len(tagTokens) == 1 {
					selector.Tags[tagTokens[0]] = ""
				}
				if len(tagTokens) == 2 {
					selector.Tags[tagTokens[0]] = tagTokens[1]
				}
			case strings.HasPrefix(strings.ToLower(s), "id:"):
				tokens := strings.Split(s, ":")
				if len(tokens) != 2 {
					return nil, fmt.Errorf("invalid id selector: %s. Expected 1 \":\", but found %d", s, len(tokens)-1)
				}
				selector.ID = tokens[1]
			case strings.HasPrefix(strings.ToLower(s), "name:"):
				tokens := strings.Split(s, ":")
				if len(tokens) != 2 {
					return nil, fmt.Errorf("invalid name selector: %s. Expected 1 \":\", but found %d", s, len(tokens)-1)
				}
				selector.Name = tokens[1]
			}
		}
		parsedSelectors = append(parsedSelectors, &selector)
	}
	return parsedSelectors, nil
}
