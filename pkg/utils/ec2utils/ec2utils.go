package ec2utils

import (
	"errors"
	"slices"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

func NormalizeCapacityType(capacityType string) string {
	capacityType = strings.TrimSpace(strings.ToLower(capacityType))
	switch {
	case capacityType == "spot":
		return string(ec2types.DefaultTargetCapacityTypeSpot)
	case strings.HasPrefix(capacityType, "on"):
		return string(ec2types.DefaultTargetCapacityTypeOnDemand)
	case strings.HasPrefix(capacityType, "capacity"):
		return string(ec2types.DefaultTargetCapacityTypeCapacityBlock)
	}
	return ""
}

func IsAlreadyExistsErr(err error) bool {
	var ae smithy.APIError
	errors.As(err, &ae)
	return slices.Contains([]string{
		"InvalidLaunchTemplateName.AlreadyExistsException",
	}, ae.ErrorCode())
}
