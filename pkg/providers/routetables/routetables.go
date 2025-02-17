package routetables

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

// Watcher discovers route tables based on selectors
type Watcher struct {
	routeTableAPI SDKRouteTablesOps
}

// SDKRouteTablesOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKRouteTablesOps interface {
	ec2.DescribeRouteTablesAPIClient
}

// Selector is a struct that represents a routeTable selector
type Selector struct {
	Tags  map[string]string
	ID    string
	VPCID string
}

// RouteTable represent an AWS RouteTable
// This is not the AWS SDK RouteTable type, but a wrapper around it so that we can add additional data
type RouteTable struct {
	ec2types.RouteTable
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse routeTable selectors: %w", err)
	}
	routeTableSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		routeTableSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				routeTableSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid routeTable selector key: %s", k)
			}
		}
		routeTableSelectors = append(routeTableSelectors, routeTableSelector)
	}
	return routeTableSelectors, nil
}

// NewWatcher creates a new RouteTable Watcher
func NewWatcher(routeTableAPI SDKRouteTablesOps) Watcher {
	return Watcher{
		routeTableAPI: routeTableAPI,
	}
}

// Resolve returns a list of route tables that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]RouteTable, error) {
	var routeTables []RouteTable
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeRouteTablesPaginator(w.routeTableAPI, &ec2.DescribeRouteTablesInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe route tables: %w", err)
			}

			routeTables = append(routeTables, lo.Map(page.RouteTables, func(sdkRouteTable ec2types.RouteTable, _ int) RouteTable {
				return RouteTable{sdkRouteTable}
			})...)
		}
	}
	return routeTables, nil
}

func (w Watcher) Create(ctx context.Context) (string, error) {

}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("route-table-id")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.VPCID != "":
			filterResult = append(filterResult, []ec2types.Filter{{
				Name:   aws.String("vpc-id"),
				Values: []string{term.VPCID},
			}})
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
