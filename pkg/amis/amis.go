package amis

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/vm/pkg/selectors"
	"github.com/samber/lo"
)

type Selector struct {
	Tags    map[string]string
	Name    string
	ID      string
	OwnerID string
	SSM     string
}

// Watcher discovers AMIs based on selectors
type Watcher struct {
	imageAPI SDKImageOps
}

// SDKImageOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKImageOps interface {
	ec2.DescribeImagesAPIClient
}

// AMI represent an AWS Machine Image (AMI)
// This is not the AWS SDK Subnet type, but a wrapper around it so that we can add additional data
type AMI struct {
	ec2types.Image
}

func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectors(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AMI selectors: %w", err)
	}
	amiSelectors := make([]Selector, len(selectors))
	for i, selector := range selectors {
		amiSelectors[i] = Selector{
			Tags: selector.Tags,
			Name: selector.Name,
			ID:   selector.ID,
		}
	}
	return amiSelectors, nil
}

// NewWatcher creates a new AMI Watcher
func NewWatcher(imageAPI SDKImageOps) Watcher {
	return Watcher{
		imageAPI: imageAPI,
	}
}

// Resolve returns a list of AMIs that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]AMI, error) {
	var amis []AMI
	for _, filters := range filterSets(selectors) {
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
	}
	return amis, nil
}

func filterSets(selectors []Selector) [][]ec2types.Filter {
	idFilter := ec2types.Filter{Name: aws.String("image-id")}
	var filterResult [][]ec2types.Filter
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		default:
			var filters []ec2types.Filter

			if term.OwnerID == "" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("owner-alias"),
					Values: []string{"self", "amazon"},
				})
			} else {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("owner-id"),
					Values: []string{term.OwnerID},
				})
			}

			if term.Name != "" {
				filters = append(filters, ec2types.Filter{
					Name:   aws.String("name"),
					Values: []string{term.Name},
				})
			}

			for k, v := range term.Tags {
				if v == "*" || v == "" {
					filters = append(filters, ec2types.Filter{
						Name:   aws.String("tag-key"),
						Values: []string{k},
					})
				} else {
					filters = append(filters, ec2types.Filter{
						Name:   aws.String(fmt.Sprintf("tag:%s", k)),
						Values: []string{v},
					})
				}
			}
			filterResult = append(filterResult, filters)
		}
	}
	if len(idFilter.Values) > 0 {
		filterResult = append(filterResult, []ec2types.Filter{idFilter})
	}
	return filterResult
}
