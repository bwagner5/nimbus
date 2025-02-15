package instancetypes

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aws/amazon-ec2-instance-selector/v3/pkg/bytequantity"
	"github.com/aws/amazon-ec2-instance-selector/v3/pkg/instancetypes"
	"github.com/aws/amazon-ec2-instance-selector/v3/pkg/selector"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

type Selector struct {
	selector.Filters
}

type InstanceType struct {
	instancetypes.Details
}

type Watcher struct {
	instanceSelector *selector.Selector
}

func NewWatcher(awsCfg aws.Config) Watcher {
	instanceSelector, err := selector.New(context.Background(), awsCfg)
	if err != nil {
		// instantiating ec2-instance-selector without a cache should never return an error.
		// TODO: fix selector constructor to not return an error
		panic(err)
	}

	return Watcher{
		instanceSelector: instanceSelector,
	}
}

func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]InstanceType, error) {
	var allInstanceTypes []InstanceType
	for _, s := range selectors {
		instanceTypes, err := w.instanceSelector.FilterVerbose(ctx, s.Filters)
		if err != nil {
			return nil, err
		}
		allInstanceTypes = append(allInstanceTypes, lo.Map(instanceTypes, func(instanceType *instancetypes.Details, _ int) InstanceType { return InstanceType{*instanceType} })...)
	}
	return lo.UniqBy(allInstanceTypes, func(instanceType InstanceType) string { return string(instanceType.InstanceType) }), nil
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse instance type selectors: %w", err)
	}
	instanceTypeSelectors := make([]Selector, 0, len(selectors))
	for _, s := range selectors {
		instanceTypeSelector := Selector{}
		for k, v := range s.KeyVals {
			switch k {
			case "vcpus":
				lowerBound, upperBound, err := parseIntRange(v)
				if err != nil {
					return nil, fmt.Errorf("invalid vcpus selector, %w", err)
				}
				instanceTypeSelector.VCpusRange = &selector.Int32RangeFilter{
					LowerBound: int32(lowerBound),
					UpperBound: lo.Ternary(upperBound == -1, math.MaxInt32, int32(upperBound)),
				}
			case "memory":
				lowerBoundStr, upperBoundStr, err := parseStringRange(v)
				if err != nil {
					return nil, fmt.Errorf("invalid memory selector, %w", err)
				}
				lowerBound := bytequantity.ByteQuantity{Quantity: 0}
				if lowerBoundStr != "" {
					lowerBound, err = bytequantity.ParseToByteQuantity(lowerBoundStr)
					if err != nil {
						return nil, fmt.Errorf("invalid memory selector, lower bound error, %w", err)
					}
				}

				upperBound := bytequantity.ByteQuantity{Quantity: math.MaxUint64}
				if upperBoundStr != "" {
					upperBound, err = bytequantity.ParseToByteQuantity(upperBoundStr)
					if err != nil {
						return nil, fmt.Errorf("invalid memory selector, upper bound error, %w", err)
					}
				}

				instanceTypeSelector.MemoryRange = &selector.ByteQuantityRangeFilter{
					LowerBound: lowerBound,
					UpperBound: upperBound,
				}
			case "arch":
				instanceTypeSelector.CPUArchitecture = lo.ToPtr(ec2types.ArchitectureType(v))
			case "generation":
				lowerBound, upperBound, err := parseIntRange(v)
				if err != nil {
					return nil, fmt.Errorf("invalid generation selector, %w", err)
				}
				instanceTypeSelector.Generation = &selector.IntRangeFilter{
					LowerBound: lowerBound,
					UpperBound: lo.Ternary(upperBound == -1, int(math.MaxInt), upperBound),
				}
			case "cpu-manufacturer":
				instanceTypeSelector.CPUManufacturer = lo.ToPtr(selector.CPUManufacturer(v))
			case "gpus":
				lowerBound, upperBound, err := parseIntRange(v)
				if err != nil {
					return nil, fmt.Errorf("invalid gpus selector, %w", err)
				}
				instanceTypeSelector.GpusRange = &selector.Int32RangeFilter{
					LowerBound: int32(lowerBound),
					UpperBound: lo.Ternary(upperBound == -1, math.MaxInt32, int32(upperBound)),
				}
			case "gpu-manufacturer":
				instanceTypeSelector.GPUManufacturer = lo.ToPtr(v)
			case "gpu-model":
				instanceTypeSelector.GPUModel = lo.ToPtr(v)
			case "local-storage":
				lowerBoundStr, upperBoundStr, err := parseStringRange(v)
				if err != nil {
					return nil, fmt.Errorf("invalid local-storage selector, %w", err)
				}
				lowerBound := bytequantity.ByteQuantity{Quantity: 0}
				if lowerBoundStr != "" {
					lowerBound, err = bytequantity.ParseToByteQuantity(lowerBoundStr)
					if err != nil {
						return nil, fmt.Errorf("invalid local-storage selector, lower bound error, %w", err)
					}
				}

				upperBound := bytequantity.ByteQuantity{Quantity: math.MaxUint64}
				if upperBoundStr != "" {
					upperBound, err = bytequantity.ParseToByteQuantity(upperBoundStr)
					if err != nil {
						return nil, fmt.Errorf("invalid local-storage selector, upper bound error, %w", err)
					}
				}

				instanceTypeSelector.InstanceStorageRange = &selector.ByteQuantityRangeFilter{
					LowerBound: lowerBound,
					UpperBound: upperBound,
				}
			default:
				return nil, fmt.Errorf("invalid instance type selector key: %s", k)
			}
		}
		instanceTypeSelectors = append(instanceTypeSelectors, instanceTypeSelector)
	}
	return instanceTypeSelectors, nil
}

// parseStringRange parses selector ranges into string tokens
//
// Selector ranges can be in the following forms:
//
//	"[a-zA-Z0-9 ]+\-[a-zA-Z0-9 ]+"
//	"\-[a-zA-Z0-9 ]+"
//	"[a-zA-Z0-9 ]+\-"
//	"[a-zA-Z0-9 ]+"
//
// Examples:
//
//		"1-9"
//		"1GiB - 10 GiB"
//		"1-" upper bound is infinite
//		"-10" lower bound is a zero value
//	    "1" lower and upper bound are both 1
func parseStringRange(rangeStr string) (string, string, error) {
	rangeStr = strings.TrimSpace(rangeStr)
	if strings.Contains(rangeStr, "-") {
		tokens := strings.Split(rangeStr, "-")
		if len(tokens) > 2 {
			return "", "", fmt.Errorf("found %d tokens, expected at most 2 tokens", len(tokens))
		}
		if len(tokens) == 1 {
			if strings.HasPrefix(rangeStr, "-") {
				return "", strings.TrimSpace(tokens[0]), nil
			}
			return strings.TrimSpace(tokens[0]), "", nil
		}
		return strings.TrimSpace(tokens[0]), strings.TrimSpace(tokens[1]), nil
	}
	return rangeStr, rangeStr, nil
}

// parseIntRange parses a selector string into an int range
//
// Selector ranges can be in the following forms:
//
//	     "0-9"
//		 "-9" lower bound is 0
//		 "1-" upper bound is infinite, but we return -1 for upper bound
//		 "1"  lower and upper bound are 1
//		 "1 - 9"
func parseIntRange(rangeStr string) (int, int, error) {
	lowerBoundStr, upperBoundStr, err := parseStringRange(rangeStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid int range, %w", err)
	}
	lowerBound := 0
	if lowerBoundStr != "" {
		lowerBound, err = strconv.Atoi(lowerBoundStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid int range, %w", err)
		}
	}

	upperBound := -1
	if upperBoundStr != "" {
		upperBound, err = strconv.Atoi(upperBoundStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid int range, %w", err)
		}
	}

	if upperBound != -1 && upperBound < lowerBound {
		return 0, 0, fmt.Errorf("invalid int range, lower bound should be less than or equal to upper bound")
	}
	return lowerBound, upperBound, nil
}
