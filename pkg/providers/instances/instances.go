package instances

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

// Watcher discovers instances based on selectors
type Watcher struct {
	instanceAPI SDKInstancesOps
}

// SDKInstancesOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKInstancesOps interface {
	ec2.DescribeInstancesAPIClient
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}

// Selector is a struct that represents an instance selector
type Selector struct {
	Tags map[string]string
	ID   string
	// State is one of: pending | running | shutting-down | terminated | stopping | stopped
	State string
}

// Instance represents an Amazon EC2 Instance
// This is not the AWS SDK Instance type, but a wrapper around it so that we can add additional data
type Instance struct {
	ec2types.Instance
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse instance selectors: %w", err)
	}
	instanceSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		instanceSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				instanceSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid instance selector key: %s", k)
			}
		}
		instanceSelectors = append(instanceSelectors, instanceSelector)
	}
	return instanceSelectors, nil
}

// NewWatcher creates a new Instance Watcher
func NewWatcher(instanceAPI SDKInstancesOps) Watcher {
	return Watcher{
		instanceAPI: instanceAPI,
	}
}

// Resolve returns a list of instances that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]Instance, error) {
	var instances []Instance
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeInstancesPaginator(w.instanceAPI, &ec2.DescribeInstancesInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe instances: %w", err)
			}
			instances = append(instances, lo.FlatMap(page.Reservations, func(sdkReservation ec2types.Reservation, _ int) []Instance {
				return lo.Map(sdkReservation.Instances, func(sdkInstance ec2types.Instance, _ int) Instance {
					return Instance{sdkInstance}
				})
			})...)
		}
	}
	return instances, nil
}

func (w Watcher) TerminateInstance(ctx context.Context, instanceID string) error {
	_, err := w.instanceAPI.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return err
	}
	return nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("instance-id")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.State != "":
			filterResult = append(filterResult, []ec2types.Filter{
				{
					Name:   aws.String("instance-state-name"),
					Values: []string{term.State},
				},
			})
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
