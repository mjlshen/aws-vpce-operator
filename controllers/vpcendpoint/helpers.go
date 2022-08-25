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

package vpcendpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	avov1alpha1 "github.com/openshift/aws-vpce-operator/api/v1alpha1"
	"github.com/openshift/aws-vpce-operator/pkg/aws_client"
	"github.com/openshift/aws-vpce-operator/pkg/dnses"
	"github.com/openshift/aws-vpce-operator/pkg/infrastructures"
	"github.com/openshift/aws-vpce-operator/pkg/util"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// defaultAVOLogger returns a zap.Logger using RFC3339 timestamps for the vpcendpoint controller
func defaultAVOLogger() (logr.Logger, error) {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)
	// TODO: Make this configurable
	// config.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)

	zapBase, err := config.Build()
	if err != nil {
		return logr.Logger{}, err
	}

	logger := zapr.NewLogger(zapBase)
	return logger.WithName(controllerName), nil
}

// defaultAVORateLimiter returns a rate limiter that reconciles more slowly than the default.
// The default is 5ms --> 1000s, but resources are created much more slowly in AWS than in
// Kubernetes, so this helps avoid AWS rate limits.
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/throttling.html#throttling-limits
func defaultAVORateLimiter() workqueue.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 5000*time.Second),
		// 10 qps, 100 bucket size, only for overall retry limiting (not per item)
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(10, 100)},
	)
}

// parseClusterInfo fills in the clusterInfo struct values inside the VpcEndpointReconciler
// and gets a new AWS session if refreshAWSSession is true.
// Generally, refreshAWSSession is only set to false during testing to mock the AWS client.
func (r *VpcEndpointReconciler) parseClusterInfo(ctx context.Context, refreshAWSSession bool) error {
	r.clusterInfo = new(clusterInfo)

	region, err := infrastructures.GetAWSRegion(ctx, r.Client)
	if err != nil {
		return err
	}
	r.clusterInfo.region = region
	r.log.V(1).Info("Parsed region from infrastructure", "region", region)

	if refreshAWSSession {
		sess, err := session.NewSession(&aws.Config{
			Region: &region,
		})
		if err != nil {
			return err
		}
		r.awsClient = aws_client.NewAwsClient(sess)
	}

	infraName, err := infrastructures.GetInfrastructureName(ctx, r.Client)
	if err != nil {
		return err
	}
	r.clusterInfo.infraName = infraName
	r.log.V(1).Info("Found infrastructure name:", "name", infraName)

	clusterTag, err := util.GetClusterTagKey(infraName)
	if err != nil {
		return err
	}
	r.clusterInfo.clusterTag = clusterTag
	r.log.V(1).Info("Found cluster tag:", "clusterTag", clusterTag)

	vpcId, err := r.awsClient.GetVPCId(r.clusterInfo.clusterTag)
	if err != nil {
		return err
	}
	r.clusterInfo.vpcId = vpcId
	r.log.V(1).Info("Found vpc id:", "vpcId", vpcId)

	domainName, err := dnses.GetPrivateHostedZoneDomainName(ctx, r.Client)
	if err != nil {
		return err
	}
	r.clusterInfo.domainName = domainName
	r.log.V(1).Info("Found domain name:", "domainName", domainName)

	return nil
}

// findOrCreateVpcEndpoint queries AWS and returns the VPC Endpoint for the provided CR and returns its ID.
// It first tries to use the VPC Endpoint ID that may be in the resource's status and falls back on
// searching for the VPC Endpoint by tags in case the status is lost. If it still cannot find a VPC
// Endpoint, it creates the VPC Endpoint and returns its ID.
func (r *VpcEndpointReconciler) findOrCreateVpcEndpoint(resource *avov1alpha1.VpcEndpoint) (*ec2.VpcEndpoint, error) {
	var vpce *ec2.VpcEndpoint

	r.log.V(1).Info("Searching for VPC Endpoint by ID", "id", resource.Status.VPCEndpointId)
	resp, err := r.awsClient.DescribeSingleVPCEndpointById(resource.Status.VPCEndpointId)
	if err != nil {
		return nil, err
	}

	// If there's no VPC Endpoint returned by ID, look for one by tag
	if resp == nil || len(resp.VpcEndpoints) == 0 {
		r.log.V(1).Info("Searching for VPC Endpoint by tags")
		resp, err = r.awsClient.FilterVPCEndpointByDefaultTags(r.clusterInfo.clusterTag)
		if err != nil {
			return nil, err
		}

		// If there are still no VPC Endpoints found, it needs to be created
		if resp == nil || len(resp.VpcEndpoints) == 0 {
			vpceName, err := util.GenerateVPCEndpointName(r.clusterInfo.infraName, resource.Name)
			if err != nil {
				return nil, err
			}
			creationResp, err := r.awsClient.CreateDefaultInterfaceVPCEndpoint(vpceName, r.clusterInfo.vpcId, resource.Spec.ServiceName, r.clusterInfo.clusterTag)
			if err != nil {
				return nil, fmt.Errorf("failed to create vpc endpoint: %v", err)
			}

			vpce = creationResp.VpcEndpoint
			r.log.V(0).Info("Created vpc endpoint:", "vpcEndpoint", *vpce.VpcEndpointId)
		} else {
			// TODO: Pending fix in FilterVpcEndpointByDefaultTags this should only return one match
			vpce = resp.VpcEndpoints[0]
		}
	} else {
		// There can only be one match returned by DescribeSingleVpcEndpointById
		vpce = resp.VpcEndpoints[0]
	}

	return vpce, nil
}

// ensureVpcEndpointSubnets ensures that the subnets attached to the VPC Endpoint are the cluster's private subnets
func (r *VpcEndpointReconciler) ensureVpcEndpointSubnets(vpce *ec2.VpcEndpoint) error {
	subnetsToAdd, subnetsToRemove, err := r.diffVpcEndpointSubnets(vpce)
	if err != nil {
		return err
	}

	// Removing subnets first before adding to avoid
	// DuplicateSubnetsInSameZone: Found another VPC endpoint subnet in the availability zone of <existing subnet>
	if len(subnetsToRemove) > 0 {
		r.log.V(1).Info("Removing subnet(s) from VPC Endpoint", "subnetsToRemove", subnetsToRemove)
		if _, err := r.awsClient.ModifyVpcEndpoint(&ec2.ModifyVpcEndpointInput{
			RemoveSubnetIds: subnetsToRemove,
			VpcEndpointId:   vpce.VpcEndpointId,
		}); err != nil {
			return err
		}
	}

	if len(subnetsToAdd) > 0 {
		r.log.V(1).Info("Adding subnet(s) to VPC Endpoint", "subnetsToAdd", subnetsToAdd)
		if _, err := r.awsClient.ModifyVpcEndpoint(&ec2.ModifyVpcEndpointInput{
			AddSubnetIds:  subnetsToAdd,
			VpcEndpointId: vpce.VpcEndpointId,
		}); err != nil {
			return err
		}
	}

	return nil
}

// diffVpcEndpointSubnets searches for the cluster's private subnets and compares them to the subnets associated with
// the VPC Endpoint, returning subnets that need to be added to the VPC Endpoint and subnets that need to be removed
// from the VPC Endpoint.
func (r *VpcEndpointReconciler) diffVpcEndpointSubnets(vpce *ec2.VpcEndpoint) ([]*string, []*string, error) {
	if r.clusterInfo == nil || r.clusterInfo.clusterTag == "" {
		return nil, nil, fmt.Errorf("unable to parse cluster tag: %v", r.clusterInfo)
	}

	subnetsResp, err := r.awsClient.DescribePrivateSubnets(r.clusterInfo.clusterTag)
	if err != nil {
		return nil, nil, err
	}

	privateSubnetIds := make([]*string, len(subnetsResp.Subnets))
	for i := range subnetsResp.Subnets {
		privateSubnetIds[i] = subnetsResp.Subnets[i].SubnetId
	}

	subnetsToAdd, subnetsToRemove := util.StringSliceTwoWayDiff(vpce.SubnetIds, privateSubnetIds)
	return subnetsToAdd, subnetsToRemove, nil
}

// ensureVpcEndpointSecurityGroups ensures that the security group associated with the VPC Endpoint
// is only the expected one.
func (r *VpcEndpointReconciler) ensureVpcEndpointSecurityGroups(vpce *ec2.VpcEndpoint, resource *avov1alpha1.VpcEndpoint) error {
	sgToAdd, sgToRemove, err := r.diffVpcEndpointSecurityGroups(vpce, resource)
	if err != nil {
		return err
	}

	if len(sgToAdd) > 0 {
		r.log.V(1).Info("Adding security group(s) to VPC Endpoint", "sgToAdd", sgToAdd)
		if _, err := r.awsClient.ModifyVpcEndpoint(&ec2.ModifyVpcEndpointInput{
			AddSecurityGroupIds: sgToAdd,
			VpcEndpointId:       vpce.VpcEndpointId,
		}); err != nil {
			return err
		}
	}

	if len(sgToRemove) > 0 {
		r.log.V(1).Info("Removing security group(s) from VPC Endpoint", "sgToRemove", sgToRemove)
		if _, err := r.awsClient.ModifyVpcEndpoint(&ec2.ModifyVpcEndpointInput{
			RemoveSecurityGroupIds: sgToRemove,
			VpcEndpointId:          vpce.VpcEndpointId,
		}); err != nil {
			return err
		}
	}

	return nil
}

// diffVpcEndpointSecurityGroups compares the security groups associated with the VPC Endpoint with
// the security group ID recorded in the resource's status, returning security groups that need to be added
// and security groups that need to be removed from the VPC Endpoint.
func (r *VpcEndpointReconciler) diffVpcEndpointSecurityGroups(vpce *ec2.VpcEndpoint, resource *avov1alpha1.VpcEndpoint) ([]*string, []*string, error) {
	vpceSgIds := make([]*string, len(vpce.Groups))
	for i := range vpce.Groups {
		vpceSgIds[i] = vpce.Groups[i].GroupId
	}

	sgToAdd, sgToRemove := util.StringSliceTwoWayDiff(
		vpceSgIds,
		[]*string{&resource.Status.SecurityGroupId},
	)

	return sgToAdd, sgToRemove, nil
}

// generateRoute53Record generates the expected Route53 Record for a provided VpcEndpoint CR
func (r *VpcEndpointReconciler) generateRoute53Record(resource *avov1alpha1.VpcEndpoint) (*route53.ResourceRecord, error) {
	if resource.Status.VPCEndpointId == "" {
		return nil, fmt.Errorf("VPCEndpointID status is missing")
	}

	vpceResp, err := r.awsClient.DescribeSingleVPCEndpointById(resource.Status.VPCEndpointId)
	if err != nil {
		return nil, err
	}

	// VPCEndpoint doesn't exist anymore for some reason
	if vpceResp == nil || len(vpceResp.VpcEndpoints) == 0 {
		return nil, nil
	}

	// DNSEntries won't be populated until the state is available
	if *vpceResp.VpcEndpoints[0].State != "available" {
		return nil, fmt.Errorf("VPCEndpoint is not in the available state")
	}

	if len(vpceResp.VpcEndpoints[0].DnsEntries) == 0 {
		return nil, fmt.Errorf("VPCEndpoint has no DNS entries")
	}

	return &route53.ResourceRecord{
		Value: vpceResp.VpcEndpoints[0].DnsEntries[0].DnsName,
	}, nil
}

// generateExternalNameService generates the expected ExternalName service for a VpcEndpoint CustomResource
func (r *VpcEndpointReconciler) generateExternalNameService(resource *avov1alpha1.VpcEndpoint) (*corev1.Service, error) {
	if resource == nil {
		// Should never happen
		return nil, fmt.Errorf("resource must be specified")
	}

	if resource.Spec.SubdomainName == "" {
		return nil, fmt.Errorf("subdomainName is a required field")
	}

	if r.clusterInfo.domainName == "" {
		return nil, fmt.Errorf("empty domainName")
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resource.Spec.ExternalNameService.Name,
			Namespace: resource.Spec.ExternalNameService.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: fmt.Sprintf("%s.%s", resource.Spec.SubdomainName, r.clusterInfo.domainName),
		},
	}

	if err := controllerutil.SetControllerReference(resource, svc, r.Scheme); err != nil {
		return nil, err
	}

	return svc, nil
}

// tagsContains returns true if the all the tags in tagsToCheck exist in tags
func tagsContains(tags []*ec2.Tag, tagsToCheck map[string]string) bool {
	for k, v := range tagsToCheck {
		found := false
		for _, tag := range tags {
			if *tag.Key == k && *tag.Value == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
