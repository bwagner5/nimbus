package resources

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailDatabase struct {
	id, name, state, region, engine, size string
}

func (d LightsailDatabase) ID() string       { return d.id }
func (d LightsailDatabase) Name() string     { return d.name }
func (d LightsailDatabase) Status() string   { return d.state }
func (d LightsailDatabase) Region() string   { return d.region }
func (d LightsailDatabase) Columns() []string { return []string{"NAME", "STATE", "ENGINE", "SIZE", "REGION"} }
func (d LightsailDatabase) Values() []string  { return []string{d.name, d.state, d.engine, d.size, d.region} }

type LightsailDatabaseProvider struct {
	client *aws.Client
}

func NewLightsailDatabaseProvider(client *aws.Client) *LightsailDatabaseProvider {
	return &LightsailDatabaseProvider{client: client}
}

func (p *LightsailDatabaseProvider) Kind() string { return "lightsail/databases" }

func (p *LightsailDatabaseProvider) Headers() []string {
	return []string{"NAME", "STATE", "ENGINE", "SIZE", "REGION"}
}

func (p *LightsailDatabaseProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	var res []Resource
	var pageToken *string
	for {
		out, err := svc.GetRelationalDatabases(ctx, &lightsail.GetRelationalDatabasesInput{PageToken: pageToken})
		if err != nil {
			return res, err
		}
		for _, db := range out.RelationalDatabases {
			res = append(res, LightsailDatabase{
				id:     *db.Arn,
				name:   *db.Name,
				state:  *db.State,
				region: region,
				engine: *db.Engine + " " + *db.EngineVersion,
				size:   *db.RelationalDatabaseBundleId,
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
