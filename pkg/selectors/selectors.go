package selectors

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// GenericSelector is a struct that represents a set of selectors
// Tags are treated special and returned as a map of key-value pairs
// All other keywords are treated as key-value pairs in the KevVals map.
// The caller must parse the Keys of the KeyVals map to check if they are supported.
type GenericSelector struct {
	Tags    map[string]string
	KeyVals map[string]string
}

// ParseSelectorsTokens parses a string of selectors into a GenericSelector struct
//
// Example:
//
// "tag:Name=fancyOS,tag:Environment=dev;id:ami-0123456"
//
// Returns:
//
//	[]GenericSelector{
//		{
//			Tags: map[string]string{
//				"Name":       "fancyOS",
//				"Environment": "dev",
//			},
//		},
//		{
//	     	KeyVals: map[string]string{
//				"id": "ami-0123456",
//			},
//		},
//	}
//
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
func ParseSelectorsTokens(selectors string) ([]GenericSelector, error) {
	selectors = strings.TrimSpace(selectors)
	selectorTerms := strings.Split(selectors, ";")
	genericSelectors := make([]GenericSelector, 0, len(selectorTerms))
	for _, term := range selectorTerms {
		if strings.TrimSpace(term) == "" {
			continue
		}
		genericSelector := GenericSelector{}
		components := strings.Split(term, ",")
		for _, c := range components {
			keyword, value, found := strings.Cut(c, ":")
			if !found {
				return nil, fmt.Errorf("invalid selector: %s", c)
			}
			if keyword == "tag" {
				if genericSelector.Tags == nil {
					genericSelector.Tags = make(map[string]string)
				}
				tagTokens := strings.Split(value, "=")
				if len(tagTokens) > 2 {
					return nil, fmt.Errorf("invalid tag selector: %s. Expected 0 or 1 \"=\", but found %d", value, len(tagTokens)-1)
				}
				// if only the tag key was given, then we set the value to the empty string and use it as a wildcard
				if len(tagTokens) == 1 {
					genericSelector.Tags[tagTokens[0]] = ""
				}
				if len(tagTokens) == 2 {
					genericSelector.Tags[tagTokens[0]] = tagTokens[1]
				}
			} else {
				if genericSelector.KeyVals == nil {
					genericSelector.KeyVals = make(map[string]string)
				}
				genericSelector.KeyVals[strings.ToLower(keyword)] = value
			}
		}
		genericSelectors = append(genericSelectors, genericSelector)
	}
	return genericSelectors, nil
}

func TagsToEC2Filters(tags map[string]string) []ec2types.Filter {
	var filters []ec2types.Filter
	for k, v := range tags {
		if v == "*" || v == "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("tag-key"),
				Values: []string{k},
			})
		} else {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String(fmt.Sprintf("tag:%s", k)),
				Values: []string{v},
			})
		}
	}
	return filters
}
