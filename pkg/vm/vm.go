package vm

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/bwagner5/vm/pkg/amis"
	"github.com/bwagner5/vm/pkg/launchplan"
	"github.com/bwagner5/vm/pkg/securitygroups"
	"github.com/bwagner5/vm/pkg/subnets"
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
}

func New(awsCfg *aws.Config) AWSVM {
	ec2API := ec2.NewFromConfig(*awsCfg)
	ssmAPI := ssm.NewFromConfig(*awsCfg)
	return AWSVM{
		awsCfg:               awsCfg,
		securityGroupWatcher: securitygroups.NewWatcher(ec2API),
		subnetWatcher:        subnets.NewWatcher(ec2API),
		amiWatcher:           amis.NewWatcher(ec2API, ssmAPI),
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan launchplan.LaunchPlan) (launchplan.LaunchPlan, error) {
	securityGroups, err := v.securityGroupWatcher.Resolve(ctx, launchPlan.Spec.SecurityGroupsSelectors)
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
