package vm

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/bwagner5/nimbus/pkg/launchplan"
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/fleets"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
	"github.com/bwagner5/nimbus/pkg/utils/ec2utils"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

type VMI interface {
	Launch(context.Context, launchplan.LaunchPlan) (launchplan.LaunchPlan, error)
	Describe(context.Context)
	Terminate(context.Context)
}

type AWSVM struct {
	awsCfg                *aws.Config
	securityGroupWatcher  securitygroups.Watcher
	subnetWatcher         subnets.Watcher
	vpcWatcher            vpcs.Watcher
	amiWatcher            amis.Watcher
	instanceTypeWatcher   instancetypes.Watcher
	instanceWatcher       instances.Watcher
	launchTemplateWatcher launchtemplates.Watcher
	fleetWatcher          fleets.Watcher
}

func New(awsCfg *aws.Config) AWSVM {
	ec2API := ec2.NewFromConfig(*awsCfg)
	ssmAPI := ssm.NewFromConfig(*awsCfg)
	return AWSVM{
		awsCfg:                awsCfg,
		securityGroupWatcher:  securitygroups.NewWatcher(ec2API),
		vpcWatcher:            vpcs.NewWatcher(*awsCfg, ec2API),
		subnetWatcher:         subnets.NewWatcher(ec2API),
		amiWatcher:            amis.NewWatcher(ec2API, ssmAPI),
		instanceWatcher:       instances.NewWatcher(ec2API),
		instanceTypeWatcher:   instancetypes.NewWatcher(*awsCfg),
		launchTemplateWatcher: launchtemplates.NewWatcher(ec2API),
		fleetWatcher:          fleets.NewWatcher(ec2API),
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan launchplan.LaunchPlan) (launchplan.LaunchPlan, error) {
	amis, err := v.amiWatcher.Resolve(ctx, launchPlan.Spec.AMISelectors)
	if err != nil {
		return launchPlan, err
	}
	instanceTypes, err := v.instanceTypeWatcher.Resolve(ctx, launchPlan.Spec.InstanceTypeSelectors)
	if err != nil {
		return launchPlan, err
	}

	// Validate that if either of SubnetSelectors or SecurityGroupSelectors are not specified, then BOTH should not be specified
	// IF a SubnetSelector is not specified, that means there is no place to launch instances, so we try to create new network infra (VPC, IGW, Subnets, Route Table, and Security Group)
	// IF a SecurityGroupSelector is not specified, the instance launch is invalid, since we need a SecurityGroup to launch.  (TODO: maybe we could default to the default SG)
	if len(launchPlan.Spec.SecurityGroupSelectors) != 0 && len(launchPlan.Spec.SubnetSelectors) == 0 {
		return launchPlan, fmt.Errorf("security group selector was specified without a subnet selector")
	}
	if len(launchPlan.Spec.SubnetSelectors) != 0 && len(launchPlan.Spec.SecurityGroupSelectors) == 0 {
		return launchPlan, fmt.Errorf("subnet selector was specified without a security group selector")
	}

	var vpc vpcs.VPC
	var subnetList []subnets.Subnet
	var securityGroups []securitygroups.SecurityGroup
	if len(launchPlan.Spec.SubnetSelectors) != 0 {
		subnetList, err = v.subnetWatcher.Resolve(ctx, launchPlan.Spec.SubnetSelectors)
		if err != nil {
			return launchPlan, err
		}
	} else {
		existingVPCs, err := v.vpcWatcher.Resolve(ctx, []vpcs.Selector{{
			Tags: map[string]string{
				tagutils.NamespaceTagKey: launchPlan.Metadata.Namespace,
			},
		}})
		if err != nil {
			return launchPlan, err
		}

		if len(existingVPCs) == 0 {
			vpcDetails, err := v.vpcWatcher.CreateVPC(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name)
			if err != nil {
				return launchPlan, err
			}
			vpc = vpcs.VPC{Vpc: *vpcDetails.VPC}
			subnetList = append(subnetList, lo.Map(vpcDetails.Subnets, func(subnet *ec2types.Subnet, _ int) subnets.Subnet { return subnets.Subnet{Subnet: *subnet} })...)
		} else {
			vpc = existingVPCs[0]
			subnetList, err = v.subnetWatcher.Resolve(ctx, []subnets.Selector{{
				VPCID: *vpc.VpcId,
			}})
			if err != nil {
				return launchPlan, err
			}
		}

		securityGroups, err = v.securityGroupWatcher.Resolve(ctx, []securitygroups.Selector{{
			Tags: tagutils.NamespacedTags(launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
		}})

		if len(securityGroups) == 0 {
			sgID, err := v.securityGroupWatcher.CreateSecurityGroup(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, securitygroups.CreateSecurityGroupOpts{
				Name:  fmt.Sprintf("%s/%s", launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
				VPCID: *vpc.VpcId,
			})
			if err != nil {
				return launchPlan, err
			}
			securityGroups, err = v.securityGroupWatcher.Resolve(ctx, []securitygroups.Selector{{
				ID: sgID,
			}})
			if err != nil {
				return launchPlan, err
			}
		}
	}

	if len(launchPlan.Spec.SecurityGroupSelectors) != 0 {
		securityGroups, err = v.securityGroupWatcher.Resolve(ctx, launchPlan.Spec.SecurityGroupSelectors)
		if err != nil {
			return launchPlan, err
		}
	}

	launchTemplateID, err := v.launchTemplateWatcher.CreateLaunchTemplate(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, launchPlan.Spec.UserData, launchPlan.Status.SecurityGroups)
	if err != nil && !ec2utils.IsAlreadyExistsErr(err) {
		return launchPlan, err
	}

	launchTemplates, err := v.launchTemplateWatcher.Resolve(ctx, []launchtemplates.Selector{{
		Tags: tagutils.NamespacedTags(launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
	}})
	if err != nil {
		return launchPlan, err
	}
	if len(launchTemplates) > 1 {
		return launchPlan, fmt.Errorf("expected 1 launch template resolved by ID, but found %d", len(launchTemplates))
	}
	if len(launchTemplates) == 0 {
		return launchPlan, fmt.Errorf("could not find launch template details for launch template %s", launchTemplateID)
	}

	launchPlan.Status = launchplan.LaunchStatus{
		SecurityGroups: securityGroups,
		Subnets:        subnetList,
		AMIs:           amis,
		InstanceTypes:  instanceTypes,
		LaunchTemplate: launchTemplates[0],
	}

	fleetID, err := v.fleetWatcher.CreateFleet(ctx, fleets.CreateFleetOptions{
		Name:           launchPlan.Metadata.Name,
		Namespace:      launchPlan.Metadata.Namespace,
		LaunchTemplate: launchPlan.Status.LaunchTemplate,
		InstanceTypes:  launchPlan.Status.InstanceTypes,
		Subnets:        launchPlan.Status.Subnets,
		AMIs:           launchPlan.Status.AMIs,
		IAMRole:        launchPlan.Spec.IAMRole,
		CapacityType:   launchPlan.Spec.CapacityType,
	})
	if err != nil {
		return launchPlan, err
	}

	fleets, err := v.fleetWatcher.Resolve(ctx, []fleets.Selector{{ID: fleetID}})
	if err != nil {
		return launchPlan, err
	}
	if len(fleets) == 0 {
		return launchPlan, fmt.Errorf("could not find fleet for %s", fleetID)
	}

	instanceIDSelectors := lo.FlatMap(fleets[0].Instances, func(fleet ec2types.DescribeFleetsInstances, _ int) []instances.Selector {
		selectors := make([]instances.Selector, 0, len(fleet.InstanceIds))
		for _, instanceID := range fleet.InstanceIds {
			selectors = append(selectors, instances.Selector{ID: instanceID})
		}
		return selectors
	})

	launchedInstances, err := v.instanceWatcher.Resolve(ctx, instanceIDSelectors)
	if err != nil {
		return launchPlan, nil
	}
	launchPlan.Status.Instances = launchedInstances
	return launchPlan, nil
}

func (v AWSVM) List(ctx context.Context, namespace string, name string) ([]instances.Instance, error) {
	return v.instanceWatcher.Resolve(ctx, []instances.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
}

func (v AWSVM) Delete(ctx context.Context, namespace string, name string) ([]string, error) {
	instances, err := v.instanceWatcher.Resolve(ctx, []instances.Selector{{
		Tags:  tagutils.NamespacedTags(namespace, name),
		State: "running",
	}})
	if err != nil {
		return nil, err
	}
	var resourcesTerminated []string
	for _, instance := range instances {
		if err := v.instanceWatcher.TerminateInstance(ctx, *instance.InstanceId); err != nil {
			return resourcesTerminated, err
		}
		resourcesTerminated = append(resourcesTerminated, *instance.InstanceId)
	}
	return resourcesTerminated, nil
}
