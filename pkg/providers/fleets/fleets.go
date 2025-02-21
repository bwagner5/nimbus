package fleets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/ec2utils"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

// Watcher discovers fleets based on selectors
type Watcher struct {
	fleetAPI SDKFleetsOps
}

// SDKFleetsOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKFleetsOps interface {
	CreateFleet(context.Context, *ec2.CreateFleetInput, ...func(*ec2.Options)) (*ec2.CreateFleetOutput, error)
	DescribeFleets(context.Context, *ec2.DescribeFleetsInput, ...func(*ec2.Options)) (*ec2.DescribeFleetsOutput, error)
	DeleteFleets(context.Context, *ec2.DeleteFleetsInput, ...func(*ec2.Options)) (*ec2.DeleteFleetsOutput, error)
}

// Selector is a struct that represents an fleet selector
type Selector struct {
	Tags map[string]string
	ID   string
}

type CreateFleetOptions struct {
	Name           string
	Namespace      string
	LaunchTemplate launchtemplates.LaunchTemplate
	Subnets        []subnets.Subnet
	AMIs           []amis.AMI
	InstanceTypes  []instancetypes.InstanceType
	IAMRole        string
	CapacityType   string
}

// Fleet represents an Amazon EC2 Fleet
// This is not the AWS SDK Fleet type, but a wrapper around it so that we can add additional data
type Fleet struct {
	ec2types.FleetData
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fleet selectors: %w", err)
	}
	fleetSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		fleetSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				fleetSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid fleet selector key: %s", k)
			}
		}
		fleetSelectors = append(fleetSelectors, fleetSelector)
	}
	return fleetSelectors, nil
}

// NewWatcher creates a new Fleet Watcher
func NewWatcher(fleetAPI SDKFleetsOps) Watcher {
	return Watcher{
		fleetAPI: fleetAPI,
	}
}

// Resolve returns a list of fleets that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]Fleet, error) {
	var fleets []Fleet
	for i, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeFleetsPaginator(w.fleetAPI, &ec2.DescribeFleetsInput{
			Filters:  filters,
			FleetIds: lo.Ternary(selectors[i].ID == "", nil, []string{selectors[i].ID}),
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe fleets: %w", err)
			}
			fleets = append(fleets, lo.Map(page.Fleets, func(fleet ec2types.FleetData, _ int) Fleet { return Fleet{fleet} })...)
		}
	}
	return fleets, nil
}

func (w Watcher) CreateFleet(ctx context.Context, createOpts CreateFleetOptions) (string, error) {
	fleetOutput, err := w.fleetAPI.CreateFleet(ctx, &ec2.CreateFleetInput{
		Type:                  ec2types.FleetTypeInstant,
		LaunchTemplateConfigs: w.launchTemplateConfigs(createOpts.LaunchTemplate, createOpts),
		TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
			TotalTargetCapacity:       aws.Int32(1),
			DefaultTargetCapacityType: ec2types.DefaultTargetCapacityType(ec2utils.NormalizeCapacityType(createOpts.CapacityType)),
		},
		OnDemandOptions: &ec2types.OnDemandOptionsRequest{
			AllocationStrategy: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
		},
		SpotOptions: &ec2types.SpotOptionsRequest{
			AllocationStrategy: ec2types.SpotAllocationStrategyPriceCapacityOptimized,
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeFleet,
				Tags:         tagutils.EC2NamespacedTags(createOpts.Namespace, createOpts.Name),
			},
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         tagutils.EC2NamespacedTags(createOpts.Namespace, createOpts.Name),
			},
		},
	})
	if err != nil {
		return "", err
	}
	return *fleetOutput.FleetId, nil
}

func (w Watcher) DeleteFleet(ctx context.Context, fleetID string) error {
	out, err := w.fleetAPI.DeleteFleets(ctx, &ec2.DeleteFleetsInput{
		FleetIds: []string{fleetID},
	})
	if err != nil {
		return err
	}
	if len(out.UnsuccessfulFleetDeletions) > 0 {
		return fmt.Errorf("code: %s, %s", out.UnsuccessfulFleetDeletions[0].Error.Code, *out.UnsuccessfulFleetDeletions[0].Error.Message)
	}
	return nil
}

func (w Watcher) launchTemplateConfigs(launchTemplate launchtemplates.LaunchTemplate, createOpts CreateFleetOptions) []ec2types.FleetLaunchTemplateConfigRequest {

	// LaunchTemplateConfigs are Fleet's way of specifying launch parameters for things like subnets, AMI, security groups, user-data, etc.
	// The parameters are spread between LaunchTemplates and Fleet Launch Template Overrides.
	// We only use LaunchTemplates to specify parameters that cannot be expressed in Fleet Launch Template Overrides since Overrides are much easier to deal with.
	// Specifying multiple subnets and AMIs tells Fleet that it is able to launch capacity into those subnets with those AMIs.
	// The AMIs' cpu architecture will determine which Fleet Types it is allowed to launch (arm64 vs x86_64)
	// Fleet chooses the best location and fleet type based on the allocation strategy specified.
	//
	// Logically, the LaunchTemplateConfigs are telling Fleet:
	//
	// I am flexible to launch:
	//   - ami-1 in subnet-1 w/ security-group-1 and security-group-2 AND user-data-1 on fleet-type-1
	//   - ami-1 in subnet-2 w/ security-group-1 and security-group-2 AND user-data-1 on fleet-type-1
	//   - ami-2 in subnet-1 w/ security-group-1 and security-group-2 AND user-data-1 on fleet-type-2 and fleet-type-3
	//   - ami-2 in subnet-2 w/ security-group-1 and security-group-2 AND user-data-1 on fleet-type-2 and fleet-type-3

	var amiArchs []amis.AMI
	if arm64AMI, ok := lo.Find(createOpts.AMIs, func(ami amis.AMI) bool { return ami.Architecture == ec2types.ArchitectureValuesArm64 }); ok {
		amiArchs = append(amiArchs, arm64AMI)
	}
	if x86AMI, ok := lo.Find(createOpts.AMIs, func(ami amis.AMI) bool { return ami.Architecture == ec2types.ArchitectureValuesX8664 }); ok {
		amiArchs = append(amiArchs, x86AMI)
	}

	var launchTemplateConfigs []ec2types.FleetLaunchTemplateConfigRequest
	for _, ami := range amiArchs {
		supportedInstanceTypesForArch := lo.Filter(createOpts.InstanceTypes, func(instanceType instancetypes.InstanceType, _ int) bool {
			_, ok := lo.Find(instanceType.ProcessorInfo.SupportedArchitectures, func(arch ec2types.ArchitectureType) bool {
				return string(arch) == string(ami.Architecture)
			})
			return ok
		})

		for _, instanceType := range supportedInstanceTypesForArch {
			for _, subnet := range createOpts.Subnets {
				launchTemplateConfigs = append(launchTemplateConfigs, ec2types.FleetLaunchTemplateConfigRequest{
					LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
						LaunchTemplateId: aws.String(*launchTemplate.LaunchTemplateId),
						Version:          aws.String("$Latest"),
					},
					Overrides: []ec2types.FleetLaunchTemplateOverridesRequest{
						{
							ImageId:      ami.ImageId,
							SubnetId:     subnet.SubnetId,
							InstanceType: instanceType.InstanceType,
						},
					},
				})
			}
		}
	}
	return launchTemplateConfigs
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
// Each filter is executed as a separate list call.
// Terms within a Selector are AND'd and between Selectors are OR'd
func filterSets(selectorList []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectorList {
		filters := []ec2types.Filter{}
		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}
