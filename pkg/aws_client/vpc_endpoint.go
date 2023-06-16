/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws_client

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	avov1alpha2 "github.com/openshift/aws-vpce-operator/api/v1alpha2"
	"github.com/openshift/aws-vpce-operator/pkg/util"
)

// SelectVPCForVPCEndpoint uses a "least connection" strategy to place a VPC Endpoint in the provided VPC ID with the
// fewest existing VPC Endpoints in it to balance out quota usage.
// https://docs.aws.amazon.com/vpc/latest/userguide/amazon-vpc-limits.html#vpc-limits-endpoints
func (c *VpcEndpoint) SelectVPCForVPCEndpoint(ctx context.Context, ids ...string) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("must specify vpc id when counting VPC Endpoints per VPC")
	}

	input := &ec2.DescribeVpcEndpointsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: ids,
			},
		},
	}

	minVpcId := ""
	minVpceConsumed := math.MaxInt
	vpcePerVpc := map[string]int{}
	for _, id := range ids {
		vpcePerVpc[id] = 0
	}

	paginator := ec2.NewDescribeVpcEndpointsPaginator(c.EC2API, input)
	for paginator.HasMorePages() {
		resp, err := paginator.NextPage(ctx)
		if err != nil {
			return "", err
		}

		for _, vpce := range resp.VpcEndpoints {
			vpcePerVpc[*vpce.VpcId]++
		}
	}

	for vpcId, vpceCount := range vpcePerVpc {
		if vpceCount < minVpceConsumed {
			minVpceConsumed = vpceCount
			minVpcId = vpcId
		}
	}

	if minVpcId == "" {
		return "", errors.New("unexpectedly did not select a VPC for the VPC Endpoint")
	}

	return minVpcId, nil
}

// FilterVpcIdsByTags tags in a list of tags and returns a list of AWS VPC Ids that have all of the provided tags
func (c *VpcEndpoint) FilterVpcIdsByTags(ctx context.Context, tags []avov1alpha2.Tag) ([]string, error) {
	if len(tags) == 0 {
		return nil, errors.New("must specify tags when filtering VPCs by tags")
	}

	filters := make([]types.Filter, len(tags))
	for i, tag := range tags {
		filters[i] = types.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", tag.Key)),
			Values: []string{tag.Value},
		}
	}

	resp, err := c.EC2API.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: filters,
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Vpcs) == 0 {
		return nil, fmt.Errorf("no VPCs found when filtering by tags: %v", tags)
	}

	ids := make([]string, len(resp.Vpcs))
	for i, vpc := range resp.Vpcs {
		ids[i] = *vpc.VpcId
	}

	return ids, nil
}

// DescribeSingleVPCEndpointById returns information about a VPC endpoint with a given id.
func (c *VpcEndpoint) DescribeSingleVPCEndpointById(ctx context.Context, id string) (*ec2.DescribeVpcEndpointsOutput, error) {
	if id == "" {
		// Otherwise, AWS will return all VPC endpoints (interpreting as no specified filter)
		return &ec2.DescribeVpcEndpointsOutput{}, nil
	}

	input := &ec2.DescribeVpcEndpointsInput{
		VpcEndpointIds: []string{id},
	}

	resp, err := c.EC2API.DescribeVpcEndpoints(ctx, input)
	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) {
			// Don't return an error if the VPC endpoint with the specified ID doesn't exist
			if ae.ErrorCode() == "InvalidVpcEndpointId.NotFound" {
				return nil, nil
			}
		}
		return nil, err
	}

	if len(resp.VpcEndpoints) != 1 {
		return nil, fmt.Errorf("expected 1 VPC endpoint, got %d", len(resp.VpcEndpoints))
	}

	return resp, err
}

// FilterVPCEndpointByDefaultTags returns information about a VPC endpoint with the default expected tags.
func (c *VpcEndpoint) FilterVPCEndpointByDefaultTags(ctx context.Context, clusterTag, vpceNameTag string) (*ec2.DescribeVpcEndpointsOutput, error) {
	if clusterTag == "" {
		return &ec2.DescribeVpcEndpointsOutput{}, nil
	}

	return c.EC2API.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: []string{vpceNameTag},
			},
			{
				Name:   aws.String("tag-key"),
				Values: []string{clusterTag},
			},
			{
				Name:   aws.String("tag:" + util.OperatorTagKey),
				Values: []string{util.OperatorTagValue},
			},
		},
	})
}

// CreateDefaultInterfaceVPCEndpoint creates an interface VPC endpoint with
// the default (open to all) VPC Endpoint policy. It attaches no security groups
// nor associates the VPC Endpoint with any subnets.
func (c *VpcEndpoint) CreateDefaultInterfaceVPCEndpoint(ctx context.Context, name, vpcId, serviceName, tagKey string) (*ec2.CreateVpcEndpointOutput, error) {
	tags, err := util.GenerateAwsTags(name, tagKey)
	if err != nil {
		return nil, err
	}

	input := &ec2.CreateVpcEndpointInput{
		// TODO: Implement ClientToken for idempotency guarantees
		// ClientToken:     "token",
		VpcId:           &vpcId,
		ServiceName:     &serviceName,
		VpcEndpointType: types.VpcEndpointTypeInterface,
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpcEndpoint,
				Tags:         tags,
			},
		},
	}

	return c.EC2API.CreateVpcEndpoint(ctx, input)
}

// DeleteVPCEndpoint deletes a VPC endpoint with the given id.
func (c *VpcEndpoint) DeleteVPCEndpoint(ctx context.Context, id string) (*ec2.DeleteVpcEndpointsOutput, error) {
	input := &ec2.DeleteVpcEndpointsInput{
		VpcEndpointIds: []string{id},
	}

	return c.EC2API.DeleteVpcEndpoints(ctx, input)
}

// ModifyVpcEndpoint modifies a VPC endpoint
func (c *VpcEndpoint) ModifyVpcEndpoint(ctx context.Context, input *ec2.ModifyVpcEndpointInput) (*ec2.ModifyVpcEndpointOutput, error) {
	return c.EC2API.ModifyVpcEndpoint(ctx, input)
}
