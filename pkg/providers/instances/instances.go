package instances

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
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

// PrettyInstance represents an instance for UI elements like the static and TUI tables
type PrettyInstance struct {
	Name         string `table:"Name"`
	Status       string `table:"Status"`
	IAMRole      string `table:"Role"`
	Age          string `table:"Age"`
	Arch         string `table:"Arch"`
	InstanceType string `table:"Instance-Type"`
	Zone         string `table:"Zone"`
	CapacityType string `table:"Capacity-Type"`
	InstanceID   string `table:"ID"`
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
	// wait for instance to go into terminated
	// this is required for other resources to delete cleanly
	for range time.NewTicker(2 * time.Second).C {
		termiantedInstances, err := w.Resolve(ctx, []Selector{{ID: instanceID, State: "terminated"}})
		if err != nil {
			return err
		}
		if len(termiantedInstances) > 0 {
			break
		}
	}
	return nil
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
				Name:   aws.String("instance-id"),
				Values: []string{term.ID},
			})
		}
		if term.State != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("instance-state-name"),
				Values: []string{term.State},
			})
		}
		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}

func (i Instance) Prettify() PrettyInstance {
	instanceProfileID := ""
	if i.IamInstanceProfile != nil {
		instanceProfileID = strings.Split(*i.IamInstanceProfile.Arn, "/")[1]
	}
	return PrettyInstance{
		Name:         tagutils.EC2TagsToMap(i.Tags)["Name"],
		Status:       string(i.State.Name),
		IAMRole:      instanceProfileID,
		Age:          time.Since(lo.FromPtr(i.LaunchTime)).Truncate(time.Second).String(),
		Arch:         string(i.Architecture),
		InstanceType: string(i.InstanceType),
		Zone:         lo.FromPtr(i.Placement.AvailabilityZone),
		CapacityType: string(i.InstanceLifecycle),
		InstanceID:   lo.FromPtr(i.InstanceId),
	}
}

func (i Instance) Name() string {
	return tagutils.EC2TagsToMap(i.Tags)[tagutils.NameTagKey]
}

func (i Instance) Namespace() string {
	return tagutils.EC2TagsToMap(i.Tags)[tagutils.NamespaceTagKey]
}
