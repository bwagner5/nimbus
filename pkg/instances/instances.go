package instances

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/amis"
	"github.com/bwagner5/nimbus/pkg/instancetypes"
	"github.com/bwagner5/nimbus/pkg/launchplan"
	"github.com/bwagner5/nimbus/pkg/securitygroups"
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
	CreateFleet(context.Context, *ec2.CreateFleetInput, ...func(*ec2.Options)) (*ec2.CreateFleetOutput, error)
	CreateLaunchTemplate(context.Context, *ec2.CreateLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error)
	CreateLaunchTemplateVersion(context.Context, *ec2.CreateLaunchTemplateVersionInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
}

// Selector is a struct that represents an instance selector
type Selector struct {
	Tags map[string]string
	ID   string
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

func (w Watcher) CreateVMs(ctx context.Context, launchPlan launchplan.LaunchPlan) ([]string, error) {
	launchTemplateID, err := w.createLaunchTemplate(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, launchPlan.Spec.UserData, launchPlan.Status.SecurityGroups)
	if err != nil {
		return nil, fmt.Errorf("unable to create launch template for %s/%s, %w", launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, err)
	}

	fleetOutput, err := w.instanceAPI.CreateFleet(ctx, &ec2.CreateFleetInput{
		Type:                  ec2types.FleetTypeInstant,
		LaunchTemplateConfigs: w.launchTemplateConfigs(launchTemplateID, launchPlan),
		TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
			TotalTargetCapacity:       aws.Int32(1),
			DefaultTargetCapacityType: ec2types.DefaultTargetCapacityTypeSpot,
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
				Tags:         tagutils.EC2NamespacedTags(launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
			},
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         tagutils.EC2NamespacedTags(launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	instanceIDs := lo.FlatMap(fleetOutput.Instances, func(instance ec2types.CreateFleetInstance, _ int) []string { return instance.InstanceIds })
	return instanceIDs, nil
}

func (w Watcher) launchTemplateConfigs(launchTemplateID string, launchPlan launchplan.LaunchPlan) []ec2types.FleetLaunchTemplateConfigRequest {

	// LaunchTemplateConfigs are Fleet's way of specifying launch parameters for things like subnets, AMI, security groups, user-data, etc.
	// The parameters are spread between LaunchTemplates and Fleet Launch Template Overrides.
	// We only use LaunchTemplates to specify parameters that cannot be expressed in Fleet Launch Template Overrides since Overrides are much easier to deal with.
	// Specifying multiple subnets and AMIs tells Fleet that it is able to launch capacity into those subnets with those AMIs.
	// The AMIs' cpu architecture will determine which Instance Types it is allowed to launch (arm64 vs x86_64)
	// Fleet chooses the best location and instance type based on the allocation strategy specified.
	//
	// Logically, the LaunchTemplateConfigs are telling Fleet:
	//
	// I am flexible to launch:
	//   - ami-1 in subnet-1 w/ security-group-1 and security-group-2 AND user-data-1 on instance-type-1
	//   - ami-1 in subnet-2 w/ security-group-1 and security-group-2 AND user-data-1 on instance-type-1
	//   - ami-2 in subnet-1 w/ security-group-1 and security-group-2 AND user-data-1 on instance-type-2 and instance-type-3
	//   - ami-2 in subnet-2 w/ security-group-1 and security-group-2 AND user-data-1 on instance-type-2 and instance-type-3

	var amiArchs []amis.AMI
	if arm64AMI, ok := lo.Find(launchPlan.Status.AMIs, func(ami amis.AMI) bool { return ami.Architecture == ec2types.ArchitectureValuesArm64 }); ok {
		amiArchs = append(amiArchs, arm64AMI)
	}
	if x86AMI, ok := lo.Find(launchPlan.Status.AMIs, func(ami amis.AMI) bool { return ami.Architecture == ec2types.ArchitectureValuesX8664 }); ok {
		amiArchs = append(amiArchs, x86AMI)
	}

	var launchTemplateConfigs []ec2types.FleetLaunchTemplateConfigRequest
	for _, ami := range amiArchs {
		supportedInstanceTypesForArch := lo.Filter(launchPlan.Status.InstanceTypes, func(instanceType instancetypes.InstanceType, _ int) bool {
			_, ok := lo.Find(instanceType.ProcessorInfo.SupportedArchitectures, func(arch ec2types.ArchitectureType) bool {
				return string(arch) == string(ami.Architecture)
			})
			return ok
		})

		for _, instanceType := range supportedInstanceTypesForArch {
			for _, subnet := range launchPlan.Status.Subnets {
				launchTemplateConfigs = append(launchTemplateConfigs, ec2types.FleetLaunchTemplateConfigRequest{
					LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
						LaunchTemplateId: aws.String(launchTemplateID),
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

func (w Watcher) createLaunchTemplate(ctx context.Context, namespace string, name string, userData string, securityGroups []securitygroups.SecurityGroup) (string, error) {
	out, err := w.instanceAPI.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String(fmt.Sprintf("%s/%s", namespace, name)),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			UserData:         aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
			SecurityGroupIds: lo.Map(securityGroups, func(sg securitygroups.SecurityGroup, _ int) string { return *sg.GroupId }),
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeLaunchTemplate,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return "", err
	}
	return *out.LaunchTemplate.LaunchTemplateId, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("instance-id")}
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
