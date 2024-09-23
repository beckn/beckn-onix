import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as elb from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { ConfigProps } from './config';

export interface VpcStackProps extends cdk.StackProps {
  config: ConfigProps;
}

export class VpcStack extends cdk.Stack {
  public readonly vpc: ec2.Vpc;
  // public readonly alb: elb.ApplicationLoadBalancer;

  constructor(scope: Construct, id: string, props: VpcStackProps) {
    super(scope, id, props);

    const config = props.config;

    // Create a new VPC
    this.vpc = new ec2.Vpc(this, 'beckn-onix-vpc', {
      maxAzs: config.MAX_AZS,  // Maximum number of availability zones
      cidr: config.CIDR,
      natGateways: 1,  // Single NAT Gateway in the public subnet
      subnetConfiguration: [
        {
          cidrMask: 24,
          name: 'Public',
          subnetType: ec2.SubnetType.PUBLIC,
        },
        {
          cidrMask: 24,
          name: 'AppLayer',
          subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS,  // Use the newer "PRIVATE_WITH_EGRESS" instead of PRIVATE_WITH_NAT
        },
        {
          cidrMask: 24,
          name: 'DatabaseLayer',
          subnetType: ec2.SubnetType.PRIVATE_ISOLATED,
        }
      ]
    });

    // Output the VPC CIDR block for other stacks to reference
    new cdk.CfnOutput(this, 'VpcCidrBlock', {
      value: this.vpc.vpcCidrBlock,
      exportName: 'VpcCidrBlock-env',  // Export name to reference in other stacks
    });

    // Output the VPC ID for other stacks
    new cdk.CfnOutput(this, 'VpcId', {
      value: this.vpc.vpcId,
      exportName: 'VpcId',  // Export name to reference in other stacks
    });

    // Output the Public Subnet IDs
    new cdk.CfnOutput(this, 'PublicSubnetIds', {
      value: this.vpc.publicSubnets.map(subnet => subnet.subnetId).join(','),
      exportName: 'PublicSubnetIds',  // Export name to reference in other stacks
    });

    // Output the App Layer Subnet IDs (for application instances or services)
    new cdk.CfnOutput(this, 'AppLayerSubnetIds', {
      value: this.vpc.selectSubnets({ subnetGroupName: 'AppLayer' }).subnetIds.join(','),
      exportName: 'AppLayerSubnetIds',  // Export name to reference in other stacks
    });

    // Output the Database Layer Subnet IDs (for database instances)
    new cdk.CfnOutput(this, 'DatabaseSubnetIds', {
      value: this.vpc.selectSubnets({ subnetGroupName: 'DatabaseLayer' }).subnetIds.join(','),
      exportName: 'DatabaseSubnetIds',  // Export name to reference in other stacks
    });
  }
}


