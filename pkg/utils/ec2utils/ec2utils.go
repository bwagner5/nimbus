package ec2utils

import (
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
