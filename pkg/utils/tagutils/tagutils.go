package tagutils

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/samber/lo"
)

var (
	SystemPrefixKey = "nimbus"
	NamespaceTagKey = fmt.Sprintf("%s-Namespace", SystemPrefixKey)
	NameTagKey      = fmt.Sprintf("%s-Name", SystemPrefixKey)
	CreatedByTagKey = fmt.Sprintf("%s-CreatedBy", SystemPrefixKey)
)

// NamespacedTags returns a map of tag key/value pairs in standardized way.
// name is optional to get tags back for a selector
func NamespacedTags(namespace string, name string) map[string]string {
	tags := map[string]string{
		NamespaceTagKey: namespace,
		CreatedByTagKey: SystemPrefixKey,
	}
	if name != "" {
		tags["Name"] = fmt.Sprintf("%s/%s", namespace, name)
		tags[NameTagKey] = name
	}
	return tags
}

// EC2NamespacedTags returns the standard tags for namepaced name items in the EC2 tag format
// name is optional
func EC2NamespacedTags(namespace, name string) []ec2types.Tag {
	tags := NamespacedTags(namespace, name)
	return MapToEC2Tags(tags)
}

// EC2TagsToMap converts EC2 typed tags to simple key/value strings in a map
func EC2TagsToMap(ec2Tags []ec2types.Tag) map[string]string {
	tags := map[string]string{}
	for _, t := range ec2Tags {
		tags[lo.FromPtr(t.Key)] = lo.FromPtr(t.Value)
	}
	return tags
}

// MapToEC2Tags takes simple key/value strings in a map and converts them to EC2 tag types
func MapToEC2Tags(tags map[string]string) []ec2types.Tag {
	var ec2Tags []ec2types.Tag
	for k, v := range tags {
		ec2Tags = append(ec2Tags, ec2types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return ec2Tags
}
