package vm

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/bwagner5/vm/pkg/launchplan"
)

type VMI interface {
	Launch(context.Context, launchplan.LaunchPlan) (launchplan.LaunchPlan, error)
	Describe(context.Context)
	Terminate(context.Context)
}

type AWSVM struct {
	awsCfg *aws.Config
}

func New(awsCfg *aws.Config) AWSVM {
	return AWSVM{
		awsCfg: awsCfg,
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan launchplan.LaunchPlan) (launchplan.LaunchPlan, error) {
	return launchPlan, nil
}
