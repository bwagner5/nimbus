package vm

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/bwagner5/vm/pkg/amis"
	"github.com/bwagner5/vm/pkg/launchplan"
	"github.com/bwagner5/vm/pkg/securitygroups"
	"github.com/bwagner5/vm/pkg/subnets"
	"github.com/samber/lo"
)

type VMI interface {
	Launch(context.Context, launchplan.LaunchPlan) (launchplan.LaunchPlan, error)
	Describe(context.Context)
	Terminate(context.Context)
}

type SDKEC2Ops interface {
	CreateFleet(context.Context, *ec2.CreateFleetInput, ...func(*ec2.Options)) (*ec2.CreateFleetOutput, error)
	CreateLaunchTemplate(context.Context, *ec2.CreateLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error)
	CreateLaunchTemplateVersion(context.Context, *ec2.CreateLaunchTemplateVersionInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
}

type AWSVM struct {
	awsCfg               *aws.Config
	ec2API               SDKEC2Ops
	securityGroupWatcher securitygroups.Watcher
	subnetWatcher        subnets.Watcher
	amiWatcher           amis.Watcher
}

func New(awsCfg *aws.Config) AWSVM {
	ec2API := ec2.NewFromConfig(*awsCfg)
	ssmAPI := ssm.NewFromConfig(*awsCfg)
	return AWSVM{
		awsCfg:               awsCfg,
		ec2API:               ec2API,
		securityGroupWatcher: securitygroups.NewWatcher(ec2API),
		subnetWatcher:        subnets.NewWatcher(ec2API),
		amiWatcher:           amis.NewWatcher(ec2API, ssmAPI),
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan launchplan.LaunchPlan) (launchplan.LaunchPlan, error) {
	securityGroups, err := v.securityGroupWatcher.Resolve(ctx, launchPlan.Spec.SecurityGroupSelectors)
	if err != nil {
		return launchPlan, err
	}
	subnets, err := v.subnetWatcher.Resolve(ctx, launchPlan.Spec.SubnetSelectors)
	if err != nil {
		return launchPlan, err
	}
	amis, err := v.amiWatcher.Resolve(ctx, launchPlan.Spec.AMISelectors)
	if err != nil {
		return launchPlan, err
	}
	launchPlan.Status = launchplan.LaunchStatus{
		SecurityGroups: securityGroups,
		Subnets:        subnets,
		AMIs:           amis,
	}
	return launchPlan, nil
}

func (v AWSVM) createVMs(ctx context.Context, launchPlan launchplan.LaunchPlan) ([]string, error) {
	launchTemplateID, err := v.createLaunchTemplate(ctx, launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, launchPlan.Spec.UserData, launchPlan.Status.SecurityGroups)
	if err != nil {
		return nil, fmt.Errorf("unable to create launch template for %s/%s, %w", launchPlan.Metadata.Namespace, launchPlan.Metadata.Name, err)
	}
	var launchTemplateConfigs []ec2types.FleetLaunchTemplateConfigRequest
	for _, amiAndSubnet := range lo.CrossJoin2(launchPlan.Status.AMIs, launchPlan.Status.Subnets) {
		launchTemplateConfigs = append(launchTemplateConfigs, ec2types.FleetLaunchTemplateConfigRequest{

			LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
				LaunchTemplateId: aws.String(launchTemplateID),
				Version:          aws.String("$Latest"),
			},
			Overrides: []ec2types.FleetLaunchTemplateOverridesRequest{
				{
					ImageId:  amiAndSubnet.A.ImageId,
					SubnetId: amiAndSubnet.B.SubnetId,
				},
			},
		},
		)
	}

	fleetOutput, err := v.ec2API.CreateFleet(ctx, &ec2.CreateFleetInput{
		Type:                  ec2types.FleetTypeInstant,
		LaunchTemplateConfigs: launchTemplateConfigs,
		TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
			TotalTargetCapacity:       aws.Int32(1),
			DefaultTargetCapacityType: ec2types.DefaultTargetCapacityTypeSpot,
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeFleet,
				Tags: []ec2types.Tag{
					{
						Key:   aws.String(""),
						Value: aws.String(""),
					},
				},
			},
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{
						Key:   aws.String(""),
						Value: aws.String(""),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	instanceIDs := lo.FlatMap(fleetOutput.Instances, func(instance ec2types.CreateFleetInstance, _ int) []string { return instance.InstanceIds })
	return instanceIDs, nil
}

func (v AWSVM) createLaunchTemplate(ctx context.Context, namespace string, name string, userData string, securityGroups []securitygroups.SecurityGroup) (string, error) {
	out, err := v.ec2API.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String(fmt.Sprintf("%s/%s", namespace, name)),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			UserData:         aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
			SecurityGroupIds: lo.Map(securityGroups, func(sg securitygroups.SecurityGroup, _ int) string { return *sg.GroupId }),
		},
	})
	if err != nil {
		return "", err
	}
	return *out.LaunchTemplate.LaunchTemplateId, nil
}
