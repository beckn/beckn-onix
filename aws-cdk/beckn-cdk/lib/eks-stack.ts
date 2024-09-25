import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as eks from 'aws-cdk-lib/aws-eks';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as cdk from 'aws-cdk-lib';
import { KubectlV30Layer } from '@aws-cdk/lambda-layer-kubectl-v30';
// import { CfnAutoScalingGroup } from 'aws-cdk-lib/aws-autoscaling';
import { Construct } from 'constructs';
import { ConfigProps } from './config';

export interface EksStackProps extends cdk.StackProps {
  config: ConfigProps;
  vpc: ec2.Vpc;
}

export class EksStack extends cdk.Stack {
  public readonly cluster: eks.Cluster;
  public readonly eksSecGrp: ec2.SecurityGroup;

  constructor(scope: Construct, id: string, props: EksStackProps) {
    super(scope, id, props);

    const config = props.config;
    

    const vpc = props.vpc;
    const cidr = config.CIDR; // from config file
    const EKS_CLUSTER_NAME = config.EKS_CLUSTER_NAME; // take it from config file
    // const ROLE_ARN = 'ROLE_ARN'; // take form config file
    const ROLE_ARN = config.ROLE_ARN;

    const securityGroupEKS = new ec2.SecurityGroup(this, "EKSSecurityGroup", {
        vpc: vpc,
        allowAllOutbound: true,
        description: "Security group for EKS",
    });

    securityGroupEKS.addIngressRule(
        ec2.Peer.ipv4(cidr),
        ec2.Port.allTraffic(),
        "Allow EKS traffic"

    );
    // securityGroupEKS.addIngressRule(
    //     ec2.Peer.securityGroupId(securityGroupEKS.securityGroupId),
    //     ec2.Port.allTraffic(),
    //     "Allow EKS traffic"
    // );

    const iamRole = iam.Role.fromRoleArn(this, "MyIAMRole", ROLE_ARN);

    // Create the EKS cluster
    this.cluster = new eks.Cluster(this, 'EksCluster', {
        vpc: vpc,
        vpcSubnets: [{ subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS }],
        defaultCapacity: 0,
        // defaultCapacityInstance: new ec2.InstanceType(config.EC2_INSTANCE_TYPE),
        kubectlLayer: new KubectlV30Layer(this, 'KubectlLayer'),
        version: eks.KubernetesVersion.V1_30,
        securityGroup: securityGroupEKS,
        endpointAccess: eks.EndpointAccess.PUBLIC_AND_PRIVATE,
        ipFamily: eks.IpFamily.IP_V4,
        clusterName: EKS_CLUSTER_NAME,
        mastersRole: iamRole, // Assign the admin role to the cluster
        outputClusterName: true,
        outputConfigCommand: true,
        authenticationMode: eks.AuthenticationMode.API_AND_CONFIG_MAP,
        bootstrapClusterCreatorAdminPermissions: true,
        
        albController: {
          version: eks.AlbControllerVersion.V2_8_1,
          repository: "public.ecr.aws/eks/aws-load-balancer-controller",
        },
    });

    const key1 = this.cluster.openIdConnectProvider.openIdConnectProviderIssuer;
    const stringEquals = new cdk.CfnJson(this, 'ConditionJson', {
      value: { 
        [`${key1}:sub`]: ['system:serviceaccount:kube-system:ebs-csi-controller-sa', 'system:serviceaccount:kube-system:efs-csi-controller-sa'],
        [`${key1}:aud`]: 'sts.amazonaws.com' 
      },
    })

    const oidcEKSCSIRole = new iam.Role(this, "OIDCRole", {
        assumedBy: new iam.FederatedPrincipal(
            `arn:aws:iam::${this.account}:oidc-provider/${this.cluster.clusterOpenIdConnectIssuer}`,
            {
                StringEquals: stringEquals,

            },
            "sts:AssumeRoleWithWebIdentity"
        ),
    });

    // Attach a managed policy to the role
    oidcEKSCSIRole.addManagedPolicy(iam.ManagedPolicy.fromAwsManagedPolicyName("service-role/AmazonEBSCSIDriverPolicy"))
    oidcEKSCSIRole.addManagedPolicy(iam.ManagedPolicy.fromAwsManagedPolicyName("service-role/AmazonEFSCSIDriverPolicy"))

    const ebscsi = new eks.CfnAddon(this, "addonEbsCsi",
        {
            addonName: "aws-ebs-csi-driver",
            clusterName: this.cluster.clusterName,
            serviceAccountRoleArn: oidcEKSCSIRole.roleArn
        }
    );

    const efscsi = new eks.CfnAddon(this, "addonEfsCsi",
        {
            addonName: "aws-efs-csi-driver",
            clusterName: this.cluster.clusterName,
            serviceAccountRoleArn: oidcEKSCSIRole.roleArn
        }
    );

    new cdk.CfnOutput(this, String("OIDC-issuer"), {
        value: this.cluster.clusterOpenIdConnectIssuer,
    });

    new cdk.CfnOutput(this, String("OIDC-issuerURL"), {
        value: this.cluster.clusterOpenIdConnectIssuerUrl,
    });

    new cdk.CfnOutput(this, "EKS Cluster Name", {
        value: this.cluster.clusterName,
    });
    new cdk.CfnOutput(this, "EKS Cluster Arn", {
        value: this.cluster.clusterArn,
    });

    const launchTemplate = new ec2.CfnLaunchTemplate(this, 'MyLaunchTemplate', {
        launchTemplateData: {
            instanceType: config.EC2_INSTANCE_TYPE,
            securityGroupIds: [this.cluster.clusterSecurityGroupId, securityGroupEKS.securityGroupId],
        }
    });
  
    // Create node group using the launch template
    this.cluster.addNodegroupCapacity('CustomNodeGroup', {
        amiType: eks.NodegroupAmiType.AL2_X86_64,
        desiredSize: config.EC2_NODES_COUNT,
        launchTemplateSpec: {
            id: launchTemplate.ref,
            version: launchTemplate.attrLatestVersionNumber,
        },
        subnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
    });

    this.eksSecGrp = securityGroupEKS;
  }
}