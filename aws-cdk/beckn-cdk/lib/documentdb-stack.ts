import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as docdb from 'aws-cdk-lib/aws-docdb';
import * as dotenv from 'dotenv';
import { ConfigProps } from './config';

// Load environment variables from .env file
dotenv.config();

interface DocumentDbStackProps extends cdk.StackProps {
  config: ConfigProps;
  vpc: ec2.Vpc;
}

export class DocumentDbStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: DocumentDbStackProps) {
    super(scope, id, props);

    // Use environment variable from .env file or fallback to a default value
    const docDbPassword = new cdk.CfnParameter(this, 'DocDbPassword', {
      type: 'String',
      description: 'The password for the DocumentDB cluster admin user',
      noEcho: true,
      default: props.config.DOCDB_PASSWORD || '',  // Use environment variable
    });

    // Security group for DocumentDB
    const docDbSecurityGroup = new ec2.SecurityGroup(this, 'DocDbSecurityGroup', {
      vpc: props.vpc,
      description: 'Security group for DocumentDB',
      allowAllOutbound: true,
    });

    docDbSecurityGroup.addIngressRule(ec2.Peer.ipv4(props.vpc.vpcCidrBlock), ec2.Port.tcp(27017), 'Allow DocumentDB traffic on port 27017');

    // DocumentDB subnet group
    const docDbSubnetGroup = new docdb.CfnDBSubnetGroup(this, 'DocDbSubnetGroup', {
      dbSubnetGroupDescription: 'Subnet group for DocumentDB',
      subnetIds: props.vpc.selectSubnets({ subnetType: ec2.SubnetType.PRIVATE_WITH_NAT }).subnetIds,
    });

    // DocumentDB cluster
    const docDbCluster = new docdb.CfnDBCluster(this, 'DocDbCluster', {
      masterUsername: 'beckn',
      masterUserPassword: docDbPassword.valueAsString,  // Password entered by the user
      dbClusterIdentifier: 'MyDocDbCluster',
      engineVersion: '4.0.0',
      vpcSecurityGroupIds: [docDbSecurityGroup.securityGroupId],
      dbSubnetGroupName: docDbSubnetGroup.ref,
    });

    // Create 2 DocumentDB instances
    new docdb.CfnDBInstance(this, 'DocDbInstance1', {
      dbClusterIdentifier: docDbCluster.ref,
      dbInstanceClass: 'db.r5.large',
    });

    new docdb.CfnDBInstance(this, 'DocDbInstance2', {
      dbClusterIdentifier: docDbCluster.ref,
      dbInstanceClass: 'db.r5.large',
    });
  }
}
