package igws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

// Watcher discovers Internet Gateways based on selectors
type Watcher struct {
	ec2API SDKIGWOps
}

// SDKIGWOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKIGWOps interface {
	ec2.DescribeInternetGatewaysAPIClient
	CreateInternetGateway(context.Context, *ec2.CreateInternetGatewayInput, ...func(*ec2.Options)) (*ec2.CreateInternetGatewayOutput, error)
	DeleteInternetGateway(context.Context, *ec2.DeleteInternetGatewayInput, ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error)
	AttachInternetGateway(context.Context, *ec2.AttachInternetGatewayInput, ...func(*ec2.Options)) (*ec2.AttachInternetGatewayOutput, error)
	DetachInternetGateway(context.Context, *ec2.DetachInternetGatewayInput, ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error)
}

// Selector is a struct that represents an Internet Gateway selector
type Selector struct {
	Tags  map[string]string
	ID    string
	VPCID string
}

// InternetGateway represent an AWS Internet Gateway
// This is not the AWS SDK InternetGateway type, but a wrapper around it so that we can add additional data
type InternetGateway struct {
	ec2types.InternetGateway
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Internet Gateway selectors: %w", err)
	}
	internetGatewaySelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		internetGatewaySelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				internetGatewaySelector.ID = v
			default:
				return nil, fmt.Errorf("invalid Internet Gateway selector key: %s", k)
			}
		}
		internetGatewaySelectors = append(internetGatewaySelectors, internetGatewaySelector)
	}
	return internetGatewaySelectors, nil
}

// NewWatcher creates a new InternetGateway Watcher
func NewWatcher(ec2API SDKIGWOps) Watcher {
	return Watcher{
		ec2API: ec2API,
	}
}

// Resolve returns a list of igws that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]InternetGateway, error) {
	var igws []InternetGateway
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeInternetGatewaysPaginator(w.ec2API, &ec2.DescribeInternetGatewaysInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe Internet Gateways: %w", err)
			}

			igws = append(igws, lo.Map(page.InternetGateways, func(sdkInternetGateway ec2types.InternetGateway, _ int) InternetGateway {
				return InternetGateway{sdkInternetGateway}
			})...)
		}
	}
	return igws, nil
}

func (w Watcher) Create(ctx context.Context, namespace, name string, vpc vpcs.VPC) (*InternetGateway, error) {
	igwOut, err := w.ec2API.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInternetGateway,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if _, err := w.ec2API.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: igwOut.InternetGateway.InternetGatewayId,
		VpcId:             vpc.VpcId,
	}); err != nil {
		return &InternetGateway{*igwOut.InternetGateway}, err
	}
	return &InternetGateway{*igwOut.InternetGateway}, nil
}

func (w Watcher) Delete(ctx context.Context, igw InternetGateway) error {
	for _, attachment := range igw.Attachments {
		_, err := w.ec2API.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
			VpcId:             attachment.VpcId,
		})
		if err != nil {
			return err
		}
	}
	_, err := w.ec2API.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
		InternetGatewayId: igw.InternetGatewayId,
	})
	return err
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("internet-gateway-id")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.VPCID != "":
			filterResult = append(filterResult, []ec2types.Filter{{
				Name:   aws.String("attachment.vpc-id"),
				Values: []string{term.VPCID},
			}})
		default:
			var filters []ec2types.Filter
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
