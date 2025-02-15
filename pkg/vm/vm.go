package vm

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/bwagner5/nimbus/pkg/amis"
	"github.com/bwagner5/nimbus/pkg/instances"
	"github.com/bwagner5/nimbus/pkg/instancetypes"
	"github.com/bwagner5/nimbus/pkg/launchplan"
	"github.com/bwagner5/nimbus/pkg/securitygroups"
	"github.com/bwagner5/nimbus/pkg/subnets"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
)

type VMI interface {
	Launch(context.Context, launchplan.LaunchPlan) (launchplan.LaunchPlan, error)
	Describe(context.Context)
	Terminate(context.Context)
}

type AWSVM struct {
	awsCfg               *aws.Config
	securityGroupWatcher securitygroups.Watcher
	subnetWatcher        subnets.Watcher
	amiWatcher           amis.Watcher
	instanceTypeWatcher  instancetypes.Watcher
	instanceWatcher      instances.Watcher
}

func New(awsCfg *aws.Config) AWSVM {
	ec2API := ec2.NewFromConfig(*awsCfg)
	ssmAPI := ssm.NewFromConfig(*awsCfg)
	return AWSVM{
		awsCfg:               awsCfg,
		securityGroupWatcher: securitygroups.NewWatcher(ec2API),
		subnetWatcher:        subnets.NewWatcher(ec2API),
		amiWatcher:           amis.NewWatcher(ec2API, ssmAPI),
		instanceWatcher:      instances.NewWatcher(ec2API),
		instanceTypeWatcher:  instancetypes.NewWatcher(*awsCfg),
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
	instanceTypes, err := v.instanceTypeWatcher.Resolve(ctx, launchPlan.Spec.InstanceTypeSelectors)
	if err != nil {
		return launchPlan, err
	}

	launchPlan.Status = launchplan.LaunchStatus{
		SecurityGroups: securityGroups,
		Subnets:        subnets,
		AMIs:           amis,
		InstanceTypes:  instanceTypes,
	}

	instanceIDs, err := v.instanceWatcher.CreateVMs(ctx, launchPlan)
	if err != nil {
		return launchPlan, err
	}
	fmt.Printf("Instances: %v\n", instanceIDs)
	return launchPlan, nil
}

func (v AWSVM) List(ctx context.Context, namespace string, name string) ([]instances.Instance, error) {
	return v.instanceWatcher.Resolve(ctx, []instances.Selector{{
		Tags: tagutils.NamespacedTags(namespace, name),
	}})
}
