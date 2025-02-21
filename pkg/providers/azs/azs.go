package azs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

// Watcher discovers availability zones based on selectors
type Watcher struct {
	ec2API SDKAvailabilityZoneOps
}

// SDKAvailabilityZoneOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKAvailabilityZoneOps interface {
	DescribeAvailabilityZones(context.Context, *ec2.DescribeAvailabilityZonesInput, ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
}

// Selector is a struct that represents a security group selector
type Selector struct {
	Tags   map[string]string
	Name   string
	ID     string
	Region string
}

type CreateAvailabilityZoneOpts struct {
	Name  string
	VPCID string
}

// AvailabilityZone represent an AWS Security Group
// This is not the AWS SDK AvailabilityZone type, but a wrapper around it so that we can add additional data
type AvailabilityZone struct {
	ec2types.AvailabilityZone
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse security group selectors: %w", err)
	}
	availabilityZoneSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		availabilityZoneSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				availabilityZoneSelector.ID = v
			case "name":
				availabilityZoneSelector.Name = v
			default:
				return nil, fmt.Errorf("invalid security group selector key: %s", k)
			}
		}
		availabilityZoneSelectors = append(availabilityZoneSelectors, availabilityZoneSelector)
	}
	return availabilityZoneSelectors, nil
}

// NewWatcher creates a new Security Group Watcher
func NewWatcher(ec2API SDKAvailabilityZoneOps) Watcher {
	return Watcher{
		ec2API: ec2API,
	}
}

// Resolve returns a list of availability zones that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]AvailabilityZone, error) {
	var availabilityZones []AvailabilityZone
	for _, filters := range filterSets(selectors) {
		azsOut, err := w.ec2API.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
			Filters: filters,
		})
		if err != nil {
			return nil, err
		}
		availabilityZones = append(availabilityZones,
			lo.Map(azsOut.AvailabilityZones, func(az ec2types.AvailabilityZone, _ int) AvailabilityZone { return AvailabilityZone{az} })...)
	}
	return availabilityZones, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
// Each filter is executed as a separate list call.
// Terms within a Selector are AND'd and between Selectors are OR'd
func filterSets(selectorList []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectorList {
		filters := []ec2types.Filter{}
		if term.ID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("zone-id"),
				Values: []string{term.ID},
			})
		}
		if term.Name != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("group-name"),
				Values: []string{term.Name},
			})
		}
		if term.Region != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("region-name"),
				Values: []string{term.Region},
			})
		}
		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}
