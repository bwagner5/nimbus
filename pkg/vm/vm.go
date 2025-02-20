package vm

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/plans"
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/azs"
	"github.com/bwagner5/nimbus/pkg/providers/fleets"
	"github.com/bwagner5/nimbus/pkg/providers/igws"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/routetables"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
	"github.com/bwagner5/nimbus/pkg/utils/ec2utils"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

type VMI interface {
	Launch(context.Context, plans.LaunchPlan) (plans.LaunchPlan, error)
	Describe(context.Context)
	Terminate(context.Context)
}

type AWSVM struct {
	awsCfg                *aws.Config
	vpcWatcher            vpcs.Watcher
	subnetWatcher         subnets.Watcher
	azWatcher             azs.Watcher
	igwWatcher            igws.Watcher
	routeTableWatcher     routetables.Watcher
	securityGroupWatcher  securitygroups.Watcher
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
		vpcWatcher:            vpcs.NewWatcher(*awsCfg, ec2API),
		subnetWatcher:         subnets.NewWatcher(ec2API),
		azWatcher:             azs.NewWatcher(ec2API),
		igwWatcher:            igws.NewWatcher(ec2API),
		routeTableWatcher:     routetables.NewWatcher(ec2API),
		securityGroupWatcher:  securitygroups.NewWatcher(ec2API),
		amiWatcher:            amis.NewWatcher(ec2API, ssmAPI),
		instanceWatcher:       instances.NewWatcher(ec2API),
		instanceTypeWatcher:   instancetypes.NewWatcher(*awsCfg),
		launchTemplateWatcher: launchtemplates.NewWatcher(ec2API),
		fleetWatcher:          fleets.NewWatcher(ec2API),
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan plans.LaunchPlan) (plans.LaunchPlan, error) {
	logging.FromContext(ctx).Debug("Executing Launch Plan")
	launchPlan.Status = plans.LaunchStatus{}

	logging.FromContext(ctx).Debug("Resolving AMIs")
	amis, err := v.amiWatcher.Resolve(ctx, launchPlan.Spec.AMISelectors)
	if err != nil {
		return launchPlan, err
	}
	launchPlan.Status.AMIs = amis

	logging.FromContext(ctx).Debug("Resolving EC2 Instances")
	instanceTypes, err := v.instanceTypeWatcher.Resolve(ctx, launchPlan.Spec.InstanceTypeSelectors)
	if err != nil {
		return launchPlan, err
	}
	launchPlan.Status.InstanceTypes = instanceTypes

	// Validate that if either of SubnetSelectors or SecurityGroupSelectors are not specified, then BOTH should not be specified
	// IF a SubnetSelector is not specified, that means there is no place to launch instances, so we try to create new network infra (VPC, IGW, Subnets, Route Table, and Security Group)
	// IF a SecurityGroupSelector is not specified, the instance launch is invalid, since we need a SecurityGroup to launch.  (TODO: maybe we could default to the default SG)
	if len(launchPlan.Spec.SecurityGroupSelectors) != 0 && len(launchPlan.Spec.SubnetSelectors) == 0 {
		return launchPlan, fmt.Errorf("security group selector was specified without a subnet selector")
	}
	if len(launchPlan.Spec.SubnetSelectors) != 0 && len(launchPlan.Spec.SecurityGroupSelectors) == 0 {
		return launchPlan, fmt.Errorf("subnet selector was specified without a security group selector")
	}

	var vpc *vpcs.VPC
	var subnetList []subnets.Subnet
	var securityGroups []securitygroups.SecurityGroup
	if len(launchPlan.Spec.SubnetSelectors) != 0 {
		logging.FromContext(ctx).Debug("Resolving Subnets")
		subnetList, err = v.subnetWatcher.Resolve(ctx, launchPlan.Spec.SubnetSelectors)
		if err != nil {
			return launchPlan, err
		}
		launchPlan.Status.Subnets = subnetList
	} else {
		logging.FromContext(ctx).Debug("No subnet selectors specified, checking if a VPC already exists")
		existingVPCs, err := v.vpcWatcher.Resolve(ctx, []vpcs.Selector{{
			Tags: map[string]string{
				tagutils.NamespaceTagKey: launchPlan.Metadata.Namespace,
			},
		}})
		if err != nil {
			return launchPlan, err
		}

		if len(existingVPCs) == 0 {
			logging.FromContext(ctx).Debug("No existing VPC found, constructing a new network")
			logging.FromContext(ctx).Debug("Creating a VPC")
			vpc, err = v.vpcWatcher.Create(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, "10.0.0.0/16")
			if err != nil {
				return launchPlan, err
			}
			launchPlan.Status.VPC = *vpc

			logging.FromContext(ctx).Debug("Resolving Availability Zones")
			availabilityZones, err := v.azWatcher.Resolve(ctx, []azs.Selector{{Region: v.awsCfg.Region}})
			if err != nil {
				return launchPlan, err
			}

			subnetSpecs := lo.Map(lo.Subset(availabilityZones, 0, 3), func(az azs.AvailabilityZone, i int) subnets.SubnetSpec {
				return subnets.SubnetSpec{
					AZ:     *az.ZoneName,
					CIDR:   fmt.Sprintf("10.0.%d.0/24", i),
					Public: true,
				}
			})

			logging.FromContext(ctx).Debug("Creating subnets")
			subnetList, err = v.subnetWatcher.Create(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, vpc, subnetSpecs)
			if err != nil {
				return launchPlan, err
			}
			launchPlan.Status.Subnets = subnetList

			logging.FromContext(ctx).Debug("Creating Internet Gateway")
			igw, err := v.igwWatcher.Create(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, *vpc)
			if err != nil {
				return launchPlan, err
			}
			launchPlan.Status.InternetGateway = *igw

			logging.FromContext(ctx).Debug("Creating public route table")
			publicRouteTable, _, err := v.routeTableWatcher.Create(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, subnetList, igw, nil)
			if err != nil {
				return launchPlan, err
			}
			launchPlan.Status.RouteTables = append(launchPlan.Status.RouteTables, *publicRouteTable)

		} else {
			logging.FromContext(ctx).Debug("Found existing VPC")
			vpc = &existingVPCs[0]
			logging.FromContext(ctx).Debug("Resolving Subnets in existing VPC")
			subnetList, err = v.subnetWatcher.Resolve(ctx, []subnets.Selector{{
				VPCID: *vpc.VpcId,
			}})
			if err != nil {
				return launchPlan, err
			}
			launchPlan.Status.VPC = *vpc
		}

		logging.FromContext(ctx).Debug("Resolving Security Groups")
		securityGroups, err = v.securityGroupWatcher.Resolve(ctx, []securitygroups.Selector{{
			Tags: tagutils.NamespacedTags(launchPlan.Metadata.Namespace, launchPlan.Metadata.Name),
		}})

		if len(securityGroups) == 0 {
			logging.FromContext(ctx).Debug("No Security Groups found")
			logging.FromContext(ctx).Debug("Creating Security Group")
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
		launchPlan.Status.SecurityGroups = securityGroups
	}

	if len(launchPlan.Spec.SecurityGroupSelectors) != 0 {
		logging.FromContext(ctx).Debug("Resolving Security Groups")
		securityGroups, err = v.securityGroupWatcher.Resolve(ctx, launchPlan.Spec.SecurityGroupSelectors)
		if err != nil {
			return launchPlan, err
		}
		launchPlan.Status.SecurityGroups = securityGroups
	}

	logging.FromContext(ctx).Debug("Creating Launch Template")
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
	launchPlan.Status.LaunchTemplate = launchTemplates[0]

	logging.FromContext(ctx).Debug("Creating EC2 Fleet")
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

	logging.FromContext(ctx).Debug("Resolving EC2 Instance")
	launchedInstances, err := v.instanceWatcher.Resolve(ctx, instanceIDSelectors)
	if err != nil {
		return launchPlan, nil
	}
	launchPlan.Status.Instances = launchedInstances
	logging.FromContext(ctx).Debug("Completed Launch Plan Execution Successfully")
	return launchPlan, nil
}

func (v AWSVM) List(ctx context.Context, namespace string, name string) ([]instances.Instance, error) {
	return v.instanceWatcher.Resolve(ctx, []instances.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
}

// DeletionPlan constructs a plan of all resources that should be deleted.
// The DeletionPlan can be confirmed by the user and then passed to the Delete func for actual deletion.
func (v AWSVM) DeletionPlan(ctx context.Context, namespace, name string) (plans.DeletionPlan, error) {
	logging.FromContext(ctx).Debug("Constructing a deletion plan")
	deletionPlan := plans.DeletionPlan{
		Metadata: plans.DeletionMetadata{
			Namespace: namespace,
			Name:      name,
		},
		Spec:   plans.DeletionSpec{},
		Status: plans.DeletionStatus{},
	}
	logging.FromContext(ctx).Debug("Resolving EC2 Instances")
	instances, err := v.instanceWatcher.Resolve(ctx, []instances.Selector{{
		Tags:  tagutils.NamespacedTags(namespace, name),
		State: "running",
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.Instances = instances

	logging.FromContext(ctx).Debug("Resolving Launch Templates")
	launchTemplates, err := v.launchTemplateWatcher.Resolve(ctx, []launchtemplates.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.LaunchTemplates = launchTemplates

	logging.FromContext(ctx).Debug("Resolving Security Groups")
	securityGroups, err := v.securityGroupWatcher.Resolve(ctx, []securitygroups.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.SecurityGroups = securityGroups

	logging.FromContext(ctx).Debug("Resolving Internet Gateways")
	internetGateways, err := v.igwWatcher.Resolve(ctx, []igws.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.InternetGateways = internetGateways

	logging.FromContext(ctx).Debug("Resolving Route Tables")
	routeTables, err := v.routeTableWatcher.Resolve(ctx, []routetables.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.RouteTables = routeTables

	logging.FromContext(ctx).Debug("Resolving Subnets")
	subnets, err := v.subnetWatcher.Resolve(ctx, []subnets.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.Subnets = subnets

	logging.FromContext(ctx).Debug("Resolving VPCs")
	vpcs, err := v.vpcWatcher.Resolve(ctx, []vpcs.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
	if err != nil {
		return deletionPlan, err
	}
	deletionPlan.Spec.VPCs = vpcs

	logging.FromContext(ctx).Debug("Deletion Plan construction completed")
	return deletionPlan, nil
}

// Delete executes a DeletionPlan. It is idempotent by keeping track of deletions in the DeletionPlan.Status
func (v AWSVM) Delete(ctx context.Context, deletionPlan plans.DeletionPlan) (plans.DeletionPlan, error) {
	logging.FromContext(ctx).Debug("Executing Deletion Plan")
	logging.FromContext(ctx).Debug("Terminating EC2 instances...")
	for _, instance := range deletionPlan.Spec.Instances {
		if deletionPlan.Status.Instances[*instance.InstanceId] {
			logging.FromContext(ctx).Debug("Already terminated EC2 instance, skipping", "instance-id", *instance.InstanceId)
			continue
		}
		if err := v.instanceWatcher.TerminateInstance(ctx, *instance.InstanceId); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.Instances == nil {
			deletionPlan.Status.Instances = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Terminated EC2 instance", "instance-id", *instance.InstanceId)
		deletionPlan.Status.Instances[*instance.InstanceId] = true
	}

	logging.FromContext(ctx).Debug("Deleting Launch Templates...")
	for _, launchTemplate := range deletionPlan.Spec.LaunchTemplates {
		if deletionPlan.Status.LaunchTemplates[*launchTemplate.LaunchTemplateId] {
			logging.FromContext(ctx).Debug("Already deleted launch template, skipping", "launch-template-id", *launchTemplate.LaunchTemplateId)
			continue
		}
		if err := v.launchTemplateWatcher.DeleteLaunchTemplate(ctx, *launchTemplate.LaunchTemplateId); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.LaunchTemplates == nil {
			deletionPlan.Status.LaunchTemplates = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted Launch Template", "launch-template-id", *launchTemplate.LaunchTemplateId)
		deletionPlan.Status.LaunchTemplates[*launchTemplate.LaunchTemplateId] = true
	}

	logging.FromContext(ctx).Debug("Deleting Security Groups...")
	for _, securityGroup := range deletionPlan.Spec.SecurityGroups {
		if deletionPlan.Status.SecurityGroups[*securityGroup.GroupId] {
			logging.FromContext(ctx).Debug("Already deleted security group, skipping", "security-group-id", *securityGroup.GroupId)
			continue
		}
		if err := v.securityGroupWatcher.DeleteSecurityGroup(ctx, *securityGroup.GroupId); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.SecurityGroups == nil {
			deletionPlan.Status.SecurityGroups = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted security group", "security-group-id", *securityGroup.GroupId)
		deletionPlan.Status.SecurityGroups[*securityGroup.GroupId] = true
	}

	logging.FromContext(ctx).Debug("Deleting Internet Gateways...")
	for _, igw := range deletionPlan.Spec.InternetGateways {
		if deletionPlan.Status.InternetGateways[*igw.InternetGatewayId] {
			logging.FromContext(ctx).Debug("Already deleted Internet Gateway, skipping", "internet-gateway-id", *igw.InternetGatewayId)
			continue
		}
		if err := v.igwWatcher.Delete(ctx, igw); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.InternetGateways == nil {
			deletionPlan.Status.InternetGateways = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted Internet Gateway", "internet-gateway-id", *igw.InternetGatewayId)
		deletionPlan.Status.InternetGateways[*igw.InternetGatewayId] = true
	}

	logging.FromContext(ctx).Debug("Deleting Route Tables...")
	for _, routeTable := range deletionPlan.Spec.RouteTables {
		if deletionPlan.Status.RouteTables[*routeTable.RouteTableId] {
			logging.FromContext(ctx).Debug("Already deleted Route Table, skipping", "route-table-id", *routeTable.RouteTableId)
			continue
		}
		if err := v.routeTableWatcher.Delete(ctx, routeTable); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.RouteTables == nil {
			deletionPlan.Status.RouteTables = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted Route Table", "route-table-id", *routeTable.RouteTableId)
		deletionPlan.Status.RouteTables[*routeTable.RouteTableId] = true
	}

	logging.FromContext(ctx).Debug("Deleting Subnets...")
	for _, subnet := range deletionPlan.Spec.Subnets {
		if deletionPlan.Status.Subnets[*subnet.SubnetId] {
			logging.FromContext(ctx).Debug("Already deleted Subnet", "subnet-id", *subnet.SubnetId)
			continue
		}
		if err := v.subnetWatcher.Delete(ctx, *subnet.SubnetId); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.Subnets == nil {
			deletionPlan.Status.Subnets = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted subnet", "subnet-id", *subnet.SubnetId)
		deletionPlan.Status.Subnets[*subnet.SubnetId] = true
	}

	logging.FromContext(ctx).Debug("Deleting VPCs...")
	for _, vpc := range deletionPlan.Spec.VPCs {
		if deletionPlan.Status.VPCs[*vpc.VpcId] {
			logging.FromContext(ctx).Debug("Already deleted VPC, skipping", "vpc-id", *vpc.VpcId)
			continue
		}
		if err := v.vpcWatcher.Delete(ctx, *vpc.VpcId); err != nil {
			return deletionPlan, err
		}
		if deletionPlan.Status.VPCs == nil {
			deletionPlan.Status.VPCs = map[string]bool{}
		}
		logging.FromContext(ctx).Debug("Deleted VPC", "vpc-id", *vpc.VpcId)
		deletionPlan.Status.VPCs[*vpc.VpcId] = true
	}
	logging.FromContext(ctx).Debug("Deletion Plan Completed Successfully")
	return deletionPlan, nil
}
