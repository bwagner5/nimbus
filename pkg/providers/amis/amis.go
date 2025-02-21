package amis

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/samber/lo"
)

var (
	aliases = map[string][]string{
		"al2023": {
			"/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64",
			"/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64",
		},
		"al2023-minimal": {
			"/aws/service/ami-amazon-linux-latest/al2023-ami-minimal-kernel-default-arm64",
			"/aws/service/ami-amazon-linux-latest/al2023-ami-minimal-kernel-default-x86_64",
		},
		"al2": {
			"/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-arm64-gp2",
			"/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2",
		},
	}
)

type Selector struct {
	Tags         map[string]string
	Name         string
	ID           string
	OwnerID      string
	SSM          string
	Alias        string
	Architecture string
}

// Watcher discovers AMIs based on selectors
type Watcher struct {
	imageAPI SDKImageOps
	ssmAPI   SDKSSMOps
}

// SDKImageOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKImageOps interface {
	ec2.DescribeImagesAPIClient
}

type SDKSSMOps interface {
	GetParameters(context.Context, *ssm.GetParametersInput, ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

// AMI represent an AWS Machine Image (AMI)
// This is not the AWS SDK Subnet type, but a wrapper around it so that we can add additional data
type AMI struct {
	ec2types.Image
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AMI selectors: %w", err)
	}
	amiSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		amiSelector := Selector{
			Tags: selector.Tags,
		}
		_, hasAlias := selector.KeyVals["alias"]
		_, hasSSM := selector.KeyVals["ssm"]
		if hasAlias && hasSSM {
			return nil, fmt.Errorf("cannot have both alias and ssm in the same selector term")
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				amiSelector.ID = v
			case "name":
				amiSelector.Name = v
			case "owner":
				amiSelector.OwnerID = v
			case "ssm":
				amiSelector.SSM = v
			case "architecture":
				amiSelector.Architecture = v
			case "alias":
				if _, ok := aliases[v]; !ok {
					return nil, fmt.Errorf("invalid ami alias: %s", v)
				}
				amiSelector.Alias = v
			default:
				return nil, fmt.Errorf("invalid ami selector key: %s", k)
			}
		}
		amiSelectors = append(amiSelectors, amiSelector)
	}
	return amiSelectors, nil
}

// NewWatcher creates a new AMI Watcher
func NewWatcher(imageAPI SDKImageOps, ssmAPI SDKSSMOps) Watcher {
	return Watcher{
		imageAPI: imageAPI,
		ssmAPI:   ssmAPI,
	}
}

// Resolve returns a list of AMIs that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]AMI, error) {
	var amis []AMI
	// resolveAMIDetails is used to store the AMI IDs from SSM that should be Described later
	var resolveAMIDetails []string
	// run through each selector's filterset and retrieve the AMIs
	for i, filters := range filterSets(selectors) {
		// if an SSM AMI alias is specific, then resolve the AMI ID and add to the resolveAMIDetails to be resolved later
		// Currently, an SSM path can only return one AMI ID
		var paths []string
		if selectors[i].Alias != "" {
			paths = append(paths, aliases[selectors[i].Alias]...)
		}
		if selectors[i].SSM != "" {
			paths = append(paths, selectors[i].SSM)
		}
		if len(paths) != 0 {
			pathOut, err := w.ssmAPI.GetParameters(ctx, &ssm.GetParametersInput{
				Names: paths,
			})
			if err != nil {
				return amis, err
			}

			if len(pathOut.Parameters) != 0 {
				resolveAMIDetails = append(resolveAMIDetails,
					lo.Map(pathOut.Parameters, func(param ssmtypes.Parameter, _ int) string { return *param.Value })...)
			}
		}
		// if there are no filters in this selector term and no AMI IDs to resolve from SSM, then return an error
		// We have to account for the default owner-alias=self,amazon filter, so we need to check if there are more than one filter
		if len(filters) <= 1 && len(resolveAMIDetails) == 0 {
			return amis, fmt.Errorf("no selectors provided for AMI selector")
		}
		// describe the AMIs based on the selector's filterset
		pager := ec2.NewDescribeImagesPaginator(w.imageAPI, &ec2.DescribeImagesInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe security groups: %w", err)
			}

			amis = append(amis, lo.Map(page.Images, func(sdkAMI ec2types.Image, _ int) AMI {
				return AMI{sdkAMI}
			})...)
		}
		// if there are AMI IDs to resolve from SSM, then describe them now
		if len(resolveAMIDetails) != 0 {
			amiCandidates := make([]AMI, 0, len(resolveAMIDetails))
			pager := ec2.NewDescribeImagesPaginator(w.imageAPI, &ec2.DescribeImagesInput{
				ImageIds: resolveAMIDetails,
			})
			for pager.HasMorePages() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return nil, fmt.Errorf("failed to describe security groups: %w", err)
				}

				amiCandidates = append(amiCandidates, lo.Map(page.Images, func(sdkAMI ec2types.Image, _ int) AMI {
					return AMI{sdkAMI}
				})...)
			}
			// if there were no filters in this selector term, then add all the AMIs from SSM
			if len(filters) == 0 {
				amis = append(amis, amiCandidates...)
			} else {
				// if there were filters in this selector term, then intersect the AMIs from SSM with the AMIs from the filters
				amiIDs := lo.Map(amis, func(ami AMI, _ int) string { return *ami.ImageId })
				amiCandidateIDs := lo.Map(amiCandidates, func(ami AMI, _ int) string { return *ami.ImageId })
				filteredAMIs := lo.Intersect(amiIDs, amiCandidateIDs)
				amis = lo.Map(filteredAMIs, func(id string, _ int) AMI {
					for _, ami := range amiCandidates {
						if *ami.ImageId == id {
							return ami
						}
					}
					return AMI{}
				})
			}
		}
	}
	return amis, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
// Each filter is executed as a separate list call.
// Terms within a Selector are AND'd and between Selectors are OR'd
func filterSets(selectorList []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectorList {
		filters := []ec2types.Filter{}
		if term.ID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("image-id"),
				Values: []string{term.ID},
			})
		}
		if term.OwnerID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("owner-alias"),
				Values: []string{term.OwnerID},
			})
		} else {
			// THIS CASE IS VERY IMPORANT TO PREVENT WhoAMI attack
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("owner-alias"),
				Values: []string{"self", "amazon"},
			})
		}
		if term.Name != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("name"),
				Values: []string{term.Name},
			})
		}
		if term.Architecture != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("architecture"),
				Values: []string{term.Architecture},
			})
		}

		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}
