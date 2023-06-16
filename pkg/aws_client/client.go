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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// VpcEndpointEC2API defines the subset of the AWS EC2 API that AVO needs to interact with
type VpcEndpointEC2API interface {
	AuthorizeSecurityGroupEgress(ctx context.Context, params *ec2.AuthorizeSecurityGroupEgressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DeleteSecurityGroup(ctx context.Context, params *ec2.DeleteSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeSecurityGroupRules(ctx context.Context, params *ec2.DescribeSecurityGroupRulesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupRulesOutput, error)

	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)

	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)

	CreateVpcEndpoint(ctx context.Context, params *ec2.CreateVpcEndpointInput, optFns ...func(*ec2.Options)) (*ec2.CreateVpcEndpointOutput, error)
	DeleteVpcEndpoints(ctx context.Context, params *ec2.DeleteVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointsOutput, error)
	DescribeVpcEndpoints(ctx context.Context, params *ec2.DescribeVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error)
	ModifyVpcEndpoint(ctx context.Context, params *ec2.ModifyVpcEndpointInput, optFns ...func(*ec2.Options)) (*ec2.ModifyVpcEndpointOutput, error)

	DescribeVpcEndpointServices(ctx context.Context, params *ec2.DescribeVpcEndpointServicesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServicesOutput, error)
}

type VpcAssociationRoute53API interface {
	AssociateVPCWithHostedZone(ctx context.Context, params *route53.AssociateVPCWithHostedZoneInput, optFns ...func(*route53.Options)) (*route53.AssociateVPCWithHostedZoneOutput, error)
}

type VpcAssociation struct {
	Route53API VpcAssociationRoute53API
}

// VpcEndpointRoute53API defines the subset of the AWS Route53 API that AVO needs to interact with
type VpcEndpointRoute53API interface {
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	ChangeTagsForResource(ctx context.Context, input *route53.ChangeTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ChangeTagsForResourceOutput, error)
	CreateHostedZone(ctx context.Context, params *route53.CreateHostedZoneInput, optFns ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
	CreateVPCAssociationAuthorization(ctx context.Context, params *route53.CreateVPCAssociationAuthorizationInput, optFns ...func(*route53.Options)) (*route53.CreateVPCAssociationAuthorizationOutput, error)
	DeleteHostedZone(ctx context.Context, params *route53.DeleteHostedZoneInput, optFns ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error)
	GetHostedZone(ctx context.Context, params *route53.GetHostedZoneInput, optFns ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	ListHostedZonesByVPC(ctx context.Context, params *route53.ListHostedZonesByVPCInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error)
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ListTagsForResource(ctx context.Context, params *route53.ListTagsForResourceInput, optFns ...func(*route53.Options)) (*route53.ListTagsForResourceOutput, error)
}

type VpcEndpoint struct {
	EC2API     VpcEndpointEC2API
	Route53API VpcEndpointRoute53API
}

type VpcEndpointAcceptanceEC2API interface {
	AcceptVpcEndpointConnections(ctx context.Context, params *ec2.AcceptVpcEndpointConnectionsInput, optFns ...func(*ec2.Options)) (*ec2.AcceptVpcEndpointConnectionsOutput, error)
	DescribeVpcEndpointConnections(ctx context.Context, params *ec2.DescribeVpcEndpointConnectionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointConnectionsOutput, error)
}

type VpcEndpointAcceptance struct {
	EC2API VpcEndpointAcceptanceEC2API
}

// NewAwsClient returns an VpcEndpoint with the provided session
func NewAwsClient(cfg aws.Config) *VpcEndpoint {
	return NewAwsClientWithServiceClients(ec2.NewFromConfig(cfg), route53.NewFromConfig(cfg))
}

// NewAwsClientWithServiceClients returns an VpcEndpoint with the provided EC2 and Route53 clients.
// Typically, not used directly except for building a mock for testing.
func NewAwsClientWithServiceClients(ec2 VpcEndpointEC2API, r53 VpcEndpointRoute53API) *VpcEndpoint {
	return &VpcEndpoint{
		EC2API:     ec2,
		Route53API: r53,
	}
}

// NewVpcAssociationClient returns a VpcAssociation with the provided session
func NewVpcAssociationClient(cfg aws.Config) *VpcAssociation {
	return NewVpcAssociationClientWithServiceClients(route53.NewFromConfig(cfg))
}

// NewVpcAssociationClientWithServiceClients returns an VpcAssociation with the provided Route53 client.
// Typically, not used directly except for building a mock for testing.
func NewVpcAssociationClientWithServiceClients(r53 VpcAssociationRoute53API) *VpcAssociation {
	return &VpcAssociation{
		Route53API: r53,
	}
}

// NewVpcEndpointAcceptanceAwsClient returns an VpcEndpointAcceptance with the provided session
func NewVpcEndpointAcceptanceAwsClient(cfg aws.Config) *VpcEndpointAcceptance {
	return &VpcEndpointAcceptance{
		EC2API: ec2.NewFromConfig(cfg),
	}
}

// NewVpcEndpointAcceptanceAwsClientWithServiceClients returns a VpcEndpointAcceptance with the provided
// EC2 client. Typically, not used directly except for building a mock for testing.
func NewVpcEndpointAcceptanceAwsClientWithServiceClients(ec2 VpcEndpointAcceptanceEC2API) *VpcEndpointAcceptance {
	return &VpcEndpointAcceptance{
		EC2API: ec2,
	}
}
