package builder

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws/arn"
	api "github.com/weaveworks/eksctl/pkg/apis/eksctl.io/v1alpha5"
	cft "github.com/weaveworks/eksctl/pkg/cfn/template"
	gfn "github.com/weaveworks/goformation/v4/cloudformation"
	gfniam "github.com/weaveworks/goformation/v4/cloudformation/iam"
	gfnt "github.com/weaveworks/goformation/v4/cloudformation/types"
	"k8s.io/apimachinery/pkg/util/sets"
)

type cfnTemplate interface {
	attachAllowPolicy(name string, refRole *gfnt.Value, resources interface{}, actions []string)
	newResource(name string, resource gfn.Resource) *gfnt.Value
}

// createRole creates an IAM role with policies required for the worker nodes and addons
func createRole(cfnTemplate cfnTemplate, clusterIAMConfig *api.ClusterIAM, iamConfig *api.NodeGroupIAM, managed, enableSSM bool) error {
	managedPolicyARNs, err := makeManagedPolicies(clusterIAMConfig, iamConfig, managed, enableSSM)
	if err != nil {
		return err
	}
	role := gfniam.Role{
		Path:                     gfnt.NewString("/"),
		AssumeRolePolicyDocument: cft.MakeAssumeRolePolicyDocumentForServices(MakeServiceRef("EC2")),
		ManagedPolicyArns:        managedPolicyARNs,
	}

	if iamConfig.InstanceRoleName != "" {
		role.RoleName = gfnt.NewString(iamConfig.InstanceRoleName)
	}

	if iamConfig.InstanceRolePermissionsBoundary != "" {
		role.PermissionsBoundary = gfnt.NewString(iamConfig.InstanceRolePermissionsBoundary)
	}

	refIR := cfnTemplate.newResource(cfnIAMInstanceRoleName, &role)

	if api.IsEnabled(iamConfig.WithAddonPolicies.AutoScaler) {
		cfnTemplate.attachAllowPolicy("PolicyAutoScaling", refIR, "*",
			[]string{
				"autoscaling:DescribeAutoScalingGroups",
				"autoscaling:DescribeAutoScalingInstances",
				"autoscaling:DescribeLaunchConfigurations",
				"autoscaling:DescribeTags",
				"autoscaling:SetDesiredCapacity",
				"autoscaling:TerminateInstanceInAutoScalingGroup",
				"ec2:DescribeLaunchTemplateVersions",
			},
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.CertManager) {
		cfnTemplate.attachAllowPolicy("PolicyCertManagerChangeSet", refIR, addARNPartitionPrefix("route53:::hostedzone/*"),
			[]string{
				"route53:ChangeResourceRecordSets",
			},
		)

		hostedZonePolicy := []string{
			"route53:ListResourceRecordSets",
			"route53:ListHostedZonesByName",
		}

		if api.IsEnabled(iamConfig.WithAddonPolicies.ExternalDNS) {
			hostedZonePolicy = append(hostedZonePolicy, "route53:ListHostedZones", "route53:ListTagsForResource")
		}

		cfnTemplate.attachAllowPolicy("PolicyCertManagerHostedZones", refIR, "*", hostedZonePolicy)
		cfnTemplate.attachAllowPolicy("PolicyCertManagerGetChange", refIR, addARNPartitionPrefix("route53:::change/*"),
			[]string{
				"route53:GetChange",
			},
		)
	} else if api.IsEnabled(iamConfig.WithAddonPolicies.ExternalDNS) {
		cfnTemplate.attachAllowPolicy("PolicyExternalDNSChangeSet", refIR, addARNPartitionPrefix("route53:::hostedzone/*"),
			[]string{
				"route53:ChangeResourceRecordSets",
			},
		)
		cfnTemplate.attachAllowPolicy("PolicyExternalDNSHostedZones", refIR, "*",
			[]string{
				"route53:ListHostedZones",
				"route53:ListResourceRecordSets",
				"route53:ListTagsForResource",
			},
		)
	}

	appMeshActions := []string{
		"servicediscovery:CreateService",
		"servicediscovery:DeleteService",
		"servicediscovery:GetService",
		"servicediscovery:GetInstance",
		"servicediscovery:RegisterInstance",
		"servicediscovery:DeregisterInstance",
		"servicediscovery:ListInstances",
		"servicediscovery:ListNamespaces",
		"servicediscovery:ListServices",
		"servicediscovery:GetInstancesHealthStatus",
		"servicediscovery:UpdateInstanceCustomHealthStatus",
		"servicediscovery:GetOperation",
		"route53:GetHealthCheck",
		"route53:CreateHealthCheck",
		"route53:UpdateHealthCheck",
		"route53:ChangeResourceRecordSets",
		"route53:DeleteHealthCheck",
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.AppMesh) {
		cfnTemplate.attachAllowPolicy("PolicyAppMesh", refIR, "*",
			append(appMeshActions, "appmesh:*"),
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.AppMeshPreview) {
		cfnTemplate.attachAllowPolicy("PolicyAppMeshPreview", refIR, "*",
			append(appMeshActions, "appmesh-preview:*"),
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.EBS) {
		cfnTemplate.attachAllowPolicy("PolicyEBS", refIR, "*",
			[]string{
				"ec2:AttachVolume",
				"ec2:CreateSnapshot",
				"ec2:CreateTags",
				"ec2:CreateVolume",
				"ec2:DeleteSnapshot",
				"ec2:DeleteTags",
				"ec2:DeleteVolume",
				"ec2:DescribeAvailabilityZones",
				"ec2:DescribeInstances",
				"ec2:DescribeSnapshots",
				"ec2:DescribeTags",
				"ec2:DescribeVolumes",
				"ec2:DescribeVolumesModifications",
				"ec2:DetachVolume",
				"ec2:ModifyVolume",
			},
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.FSX) {
		cfnTemplate.attachAllowPolicy("PolicyFSX", refIR, "*",
			[]string{
				"fsx:*",
			},
		)
		cfnTemplate.attachAllowPolicy("PolicyServiceLinkRole", refIR, addARNPartitionPrefix("iam::*:role/aws-service-role/*"),
			[]string{
				"iam:CreateServiceLinkedRole",
				"iam:AttachRolePolicy",
				"iam:PutRolePolicy",
			},
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.EFS) {
		cfnTemplate.attachAllowPolicy("PolicyEFS", refIR, "*",
			[]string{
				"elasticfilesystem:*",
			},
		)
		cfnTemplate.attachAllowPolicy("PolicyEFSEC2", refIR, "*",
			[]string{
				"ec2:DescribeSubnets",
				"ec2:CreateNetworkInterface",
				"ec2:DescribeNetworkInterfaces",
				"ec2:DeleteNetworkInterface",
				"ec2:ModifyNetworkInterfaceAttribute",
				"ec2:DescribeNetworkInterfaceAttribute",
			},
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.ALBIngress) {
		cfnTemplate.attachAllowPolicy("PolicyALBIngress", refIR, "*",
			[]string{
				"acm:DescribeCertificate",
				"acm:ListCertificates",
				"acm:GetCertificate",
				"ec2:AuthorizeSecurityGroupIngress",
				"ec2:CreateSecurityGroup",
				"ec2:CreateTags",
				"ec2:DeleteTags",
				"ec2:DeleteSecurityGroup",
				"ec2:DescribeAccountAttributes",
				"ec2:DescribeAddresses",
				"ec2:DescribeInstances",
				"ec2:DescribeInstanceStatus",
				"ec2:DescribeInternetGateways",
				"ec2:DescribeNetworkInterfaces",
				"ec2:DescribeSecurityGroups",
				"ec2:DescribeSubnets",
				"ec2:DescribeTags",
				"ec2:DescribeVpcs",
				"ec2:ModifyInstanceAttribute",
				"ec2:ModifyNetworkInterfaceAttribute",
				"ec2:RevokeSecurityGroupIngress",
				"elasticloadbalancing:AddListenerCertificates",
				"elasticloadbalancing:AddTags",
				"elasticloadbalancing:CreateListener",
				"elasticloadbalancing:CreateLoadBalancer",
				"elasticloadbalancing:CreateRule",
				"elasticloadbalancing:CreateTargetGroup",
				"elasticloadbalancing:DeleteListener",
				"elasticloadbalancing:DeleteLoadBalancer",
				"elasticloadbalancing:DeleteRule",
				"elasticloadbalancing:DeleteTargetGroup",
				"elasticloadbalancing:DeregisterTargets",
				"elasticloadbalancing:DescribeListenerCertificates",
				"elasticloadbalancing:DescribeListeners",
				"elasticloadbalancing:DescribeLoadBalancers",
				"elasticloadbalancing:DescribeLoadBalancerAttributes",
				"elasticloadbalancing:DescribeRules",
				"elasticloadbalancing:DescribeSSLPolicies",
				"elasticloadbalancing:DescribeTags",
				"elasticloadbalancing:DescribeTargetGroups",
				"elasticloadbalancing:DescribeTargetGroupAttributes",
				"elasticloadbalancing:DescribeTargetHealth",
				"elasticloadbalancing:ModifyListener",
				"elasticloadbalancing:ModifyLoadBalancerAttributes",
				"elasticloadbalancing:ModifyRule",
				"elasticloadbalancing:ModifyTargetGroup",
				"elasticloadbalancing:ModifyTargetGroupAttributes",
				"elasticloadbalancing:RegisterTargets",
				"elasticloadbalancing:RemoveListenerCertificates",
				"elasticloadbalancing:RemoveTags",
				"elasticloadbalancing:SetIpAddressType",
				"elasticloadbalancing:SetSecurityGroups",
				"elasticloadbalancing:SetSubnets",
				"elasticloadbalancing:SetWebACL",
				"iam:CreateServiceLinkedRole",
				"iam:GetServerCertificate",
				"iam:ListServerCertificates",
				"waf-regional:GetWebACLForResource",
				"waf-regional:GetWebACL",
				"waf-regional:AssociateWebACL",
				"waf-regional:DisassociateWebACL",
				"tag:GetResources",
				"tag:TagResources",
				"waf:GetWebACL",
				"wafv2:GetWebACL",
				"wafv2:GetWebACLForResource",
				"wafv2:AssociateWebACL",
				"wafv2:DisassociateWebACL",
				"shield:DescribeProtection",
				"shield:GetSubscriptionState",
				"shield:DeleteProtection",
				"shield:CreateProtection",
				"shield:DescribeSubscription",
				"shield:ListProtections",
			},
		)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.XRay) {
		cfnTemplate.attachAllowPolicy("PolicyXRay", refIR, "*",
			[]string{
				"xray:PutTraceSegments",
				"xray:PutTelemetryRecords",
				"xray:GetSamplingRules",
				"xray:GetSamplingTargets",
				"xray:GetSamplingStatisticSummaries",
			},
		)
	}
	return nil
}

func makeManagedPolicies(iamCluster *api.ClusterIAM, iamConfig *api.NodeGroupIAM, managed, enableSSM bool) (*gfnt.Value, error) {
	managedPolicyNames := sets.NewString()
	if len(iamConfig.AttachPolicyARNs) == 0 {
		managedPolicyNames.Insert(iamDefaultNodePolicies...)
		if !api.IsEnabled(iamCluster.WithOIDC) {
			managedPolicyNames.Insert(iamPolicyAmazonEKSCNIPolicy)
		}
		if managed {
			// The Managed Nodegroup API requires this managed policy to be present, even though
			// AmazonEC2ContainerRegistryPowerUser (attached if imageBuilder is enabled) contains a superset of the
			// actions allowed by this managed policy
			managedPolicyNames.Insert(iamPolicyAmazonEC2ContainerRegistryReadOnly)
		}
	}

	if enableSSM {
		managedPolicyNames.Insert(iamPolicyAmazonSSMManagedInstanceCore)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.ImageBuilder) {
		managedPolicyNames.Insert(iamPolicyAmazonEC2ContainerRegistryPowerUser)
	} else if !managed {
		// attach this policy even if `AttachPolicyARNs` is specified to preserve existing behaviour for unmanaged
		// nodegroups
		managedPolicyNames.Insert(iamPolicyAmazonEC2ContainerRegistryReadOnly)
	}

	if api.IsEnabled(iamConfig.WithAddonPolicies.CloudWatch) {
		managedPolicyNames.Insert(iamPolicyCloudWatchAgentServerPolicy)
	}

	for _, policyARN := range iamConfig.AttachPolicyARNs {
		parsedARN, err := arn.Parse(policyARN)
		if err != nil {
			return nil, err
		}
		start := strings.IndexRune(parsedARN.Resource, '/')
		if start == -1 || start+1 == len(parsedARN.Resource) {
			return nil, fmt.Errorf("failed to find ARN resource name: %s", parsedARN.Resource)
		}
		resourceName := parsedARN.Resource[start+1:]
		managedPolicyNames.Delete(resourceName)
	}

	return gfnt.NewSlice(append(
		makeStringSlice(iamConfig.AttachPolicyARNs...),
		makePolicyARNs(managedPolicyNames.List()...)...,
	)...), nil
}

// NormalizeARN returns the ARN with just the last element in the resource path preserved. If the
// input does not contain at least one forward-slash then the input is returned unmodified.
//
// When providing an existing instanceRoleARN that contains a path other than "/", nodes may
// fail to join the cluster as the AWS IAM Authenticator does not recognize such ARNs declared in
// the aws-auth ConfigMap.
//
// See: https://docs.aws.amazon.com/eks/latest/userguide/troubleshooting.html#troubleshoot-container-runtime-network
func NormalizeARN(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) <= 1 {
		return arn
	}
	return fmt.Sprintf("%s/%s", parts[0], parts[len(parts)-1])
}
