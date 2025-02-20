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
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("zone-id")}
	nameFilter := ec2types.Filter{Name: aws.String("group-name")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.Name != "":
			nameFilter.Values = append(nameFilter.Values, term.Name)
		case term.Region != "":
			filterResult = append(filterResult, []ec2types.Filter{{
				Name:   aws.String("region-name"),
				Values: []string{term.Region},
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
	if len(nameFilter.Values) > 0 {
		filterResult = append(filterResult, []ec2types.Filter{nameFilter})
	}
	return filterResult
}
