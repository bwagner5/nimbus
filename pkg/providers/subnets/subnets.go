package subnets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

// Watcher discovers subnets based on selectors
type Watcher struct {
	subnetAPI SDKSubnetsOps
}

// SDKSubnetsOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKSubnetsOps interface {
	ec2.DescribeSubnetsAPIClient
}

// Selector is a struct that represents a subnet selector
type Selector struct {
	Tags map[string]string
	ID   string
}

// Subnet represent an AWS Subnet
// This is not the AWS SDK Subnet type, but a wrapper around it so that we can add additional data
type Subnet struct {
	ec2types.Subnet
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subnet selectors: %w", err)
	}
	subnetSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		subnetSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				subnetSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid subnet selector key: %s", k)
			}
		}
		subnetSelectors = append(subnetSelectors, subnetSelector)
	}
	return subnetSelectors, nil
}

// NewWatcher creates a new Subnet Watcher
func NewWatcher(subnetAPI SDKSubnetsOps) Watcher {
	return Watcher{
		subnetAPI: subnetAPI,
	}
}

// Resolve returns a list of subnets that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]Subnet, error) {
	var subnets []Subnet
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeSubnetsPaginator(w.subnetAPI, &ec2.DescribeSubnetsInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe subnets: %w", err)
			}

			subnets = append(subnets, lo.Map(page.Subnets, func(sdkSubnet ec2types.Subnet, _ int) Subnet {
				return Subnet{sdkSubnet}
			})...)
		}
	}
	return subnets, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("subnet-id")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		default:
			var filters []ec2types.Filter
			for k, v := range term.Tags {
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
			filterResult = append(filterResult, filters)
		}
	}
	if len(idFilter.Values) > 0 {
		filterResult = append(filterResult, []ec2types.Filter{idFilter})
	}
	return filterResult
}
