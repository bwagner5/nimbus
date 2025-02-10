package securitygroups

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/vm/pkg/selectors"
	"github.com/samber/lo"
)

// Watcher discovers security groups based on selectors
type Watcher struct {
	sg SDKSecurityGroupOps
}

// SDKSecurityGroupOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKSecurityGroupOps interface {
	ec2.DescribeSecurityGroupsAPIClient
	ec2.DescribeSecurityGroupRulesAPIClient
}

// Selector is a struct that represents a security group selector
type Selector struct {
	Tags map[string]string
	Name string
	ID   string
}

// SecurityGroup represent an AWS Security Group
// This is not the AWS SDK SecurityGroup type, but a wrapper around it so that we can add additional data
type SecurityGroup struct {
	ec2types.SecurityGroup
}

// ParseSelectors converts a string of selectors into a slice of security group selectors
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectors(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse security group selectors: %w", err)
	}
	sgSelectors := make([]Selector, len(selectors))
	for i, selector := range selectors {
		sgSelectors[i] = Selector{
			Tags: selector.Tags,
			Name: selector.Name,
			ID:   selector.ID,
		}
	}
	return sgSelectors, nil
}

// NewWatcher creates a new Security Group Watcher
func NewWatcher(sg SDKSecurityGroupOps) Watcher {
	return Watcher{
		sg: sg,
	}
}

// Resolve returns a list of security groups that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]SecurityGroup, error) {
	var securityGroups []SecurityGroup
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeSecurityGroupsPaginator(w.sg, &ec2.DescribeSecurityGroupsInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe security groups: %w", err)
			}

			securityGroups = append(securityGroups, lo.Map(page.SecurityGroups, func(sdkSG ec2types.SecurityGroup, _ int) SecurityGroup {
				return SecurityGroup{sdkSG}
			})...)
		}
	}
	return securityGroups, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("group-id")}
	nameFilter := ec2types.Filter{Name: aws.String("group-name")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.Name != "":
			nameFilter.Values = append(nameFilter.Values, term.Name)
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
	if len(nameFilter.Values) > 0 {
		filterResult = append(filterResult, []ec2types.Filter{nameFilter})
	}
	return filterResult
}
