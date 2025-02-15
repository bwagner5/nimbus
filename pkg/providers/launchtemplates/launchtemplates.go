package launchtemplates

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

// Watcher discovers fleets based on selectors
type Watcher struct {
	launchTemplateAPI SDKLaunchTemplatesOps
}

// SDKLaunchTemplatesOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKLaunchTemplatesOps interface {
	ec2.DescribeLaunchTemplatesAPIClient
	ec2.DescribeLaunchTemplateVersionsAPIClient
	CreateLaunchTemplate(context.Context, *ec2.CreateLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error)
	DeleteLaunchTemplate(context.Context, *ec2.DeleteLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error)
	// CreateLaunchTemplateVersion(context.Context, *ec2.CreateLaunchTemplateVersionInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
}

// Selector is a struct that represents an launchTemplate selector
type Selector struct {
	Tags map[string]string
	ID   string
	Name string
}

// LaunchTemplate represents an Amazon EC2 LaunchTemplate
// This is not the AWS SDK LaunchTemplate type, but a wrapper around it so that we can add additional data
type LaunchTemplate struct {
	ec2types.LaunchTemplate
	LaunchTemplateVersions []LaunchTemplateVersion
}

type LaunchTemplateVersion struct {
	ec2types.LaunchTemplateVersion
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse launchTemplate selectors: %w", err)
	}
	launchTemplateSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		launchTemplateSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				launchTemplateSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid launchTemplate selector key: %s", k)
			}
		}
		launchTemplateSelectors = append(launchTemplateSelectors, launchTemplateSelector)
	}
	return launchTemplateSelectors, nil
}

// NewWatcher creates a new LaunchTemplate Watcher
func NewWatcher(launchTemplateAPI SDKLaunchTemplatesOps) Watcher {
	return Watcher{
		launchTemplateAPI: launchTemplateAPI,
	}
}

// Resolve returns a list of launch templates that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]LaunchTemplate, error) {
	var launchTemplates []LaunchTemplate
	for i, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeLaunchTemplatesPaginator(w.launchTemplateAPI, &ec2.DescribeLaunchTemplatesInput{
			Filters:           filters,
			LaunchTemplateIds: lo.Ternary(selectors[i].ID == "", nil, []string{selectors[i].ID}),
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe launch templates: %w", err)
			}
			for _, lt := range page.LaunchTemplates {
				ltVersions, err := w.resolveLaunchTemplateVersions(ctx, *lt.LaunchTemplateId)
				if err != nil {
					return nil, err
				}
				launchTemplates = append(launchTemplates, LaunchTemplate{LaunchTemplate: lt, LaunchTemplateVersions: ltVersions})
			}
		}
	}
	return launchTemplates, nil
}

func (w Watcher) resolveLaunchTemplateVersions(ctx context.Context, launchTemplateID string) ([]LaunchTemplateVersion, error) {
	var launchTemplateVersions []LaunchTemplateVersion
	pager := ec2.NewDescribeLaunchTemplateVersionsPaginator(w.launchTemplateAPI, &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateId: aws.String(launchTemplateID),
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to describe launch template versions for %s: %w", launchTemplateID, err)
		}
		launchTemplateVersions = append(launchTemplateVersions, lo.Map(page.LaunchTemplateVersions, func(ltVersion ec2types.LaunchTemplateVersion, _ int) LaunchTemplateVersion {
			return LaunchTemplateVersion{ltVersion}
		})...)
	}
	return launchTemplateVersions, nil
}

func (w Watcher) CreateLaunchTemplate(ctx context.Context, namespace string, name string, userData string, securityGroups []securitygroups.SecurityGroup) (string, error) {
	out, err := w.launchTemplateAPI.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String(fmt.Sprintf("%s/%s", namespace, name)),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			UserData:         aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
			SecurityGroupIds: lo.Map(securityGroups, func(sg securitygroups.SecurityGroup, _ int) string { return *sg.GroupId }),
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeLaunchTemplate,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return "", err
	}
	return *out.LaunchTemplate.LaunchTemplateId, nil
}

func (w Watcher) DeleteLaunchTemplate(ctx context.Context, launchTemplateID string) error {
	_, err := w.launchTemplateAPI.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{LaunchTemplateId: &launchTemplateID})
	if err != nil {
		return err
	}
	return nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectors {
		switch {
		case term.Name != "":
			filterResult = append(filterResult, []ec2types.Filter{
				{
					Name:   aws.String("launch-template-name"),
					Values: []string{term.Name},
				},
			})
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
	return filterResult
}
