package resources

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailDistribution struct {
	id, name, state, origin, domain string
}

func (d LightsailDistribution) ID() string       { return d.id }
func (d LightsailDistribution) Name() string     { return d.name }
func (d LightsailDistribution) Status() string   { return d.state }
func (d LightsailDistribution) Region() string   { return "global" }
func (d LightsailDistribution) Columns() []string { return []string{"NAME", "STATUS", "ORIGIN", "DOMAIN"} }
func (d LightsailDistribution) Values() []string  { return []string{d.name, d.state, d.origin, d.domain} }

type LightsailDistributionProvider struct {
	client *aws.Client
}

func NewLightsailDistributionProvider(client *aws.Client) *LightsailDistributionProvider {
	return &LightsailDistributionProvider{client: client}
}

func (p *LightsailDistributionProvider) Kind() string { return "lightsail/distributions" }

func (p *LightsailDistributionProvider) Headers() []string {
	return []string{"NAME", "STATUS", "ORIGIN", "DOMAIN"}
}

func (p *LightsailDistributionProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	// Distributions are global, always use us-east-1
	svc := lightsail.NewFromConfig(p.client.WithRegion("us-east-1").Config())
	var res []Resource
	var pageToken *string
	for {
		out, err := svc.GetDistributions(ctx, &lightsail.GetDistributionsInput{PageToken: pageToken})
		if err != nil {
			return res, err
		}
		for _, d := range out.Distributions {
			origin := ""
			if d.Origin != nil && d.Origin.Name != nil {
				origin = *d.Origin.Name
			}
			res = append(res, LightsailDistribution{
				id:     *d.Arn,
				name:   *d.Name,
				state:  *d.Status,
				origin: origin,
				domain: *d.DomainName,
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
