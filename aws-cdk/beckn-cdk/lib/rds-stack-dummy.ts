import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as rds from 'aws-cdk-lib/aws-rds';
import { Construct } from 'constructs';
import { ConfigProps } from './config';
import cluster from 'cluster';

export interface RdsStackProps extends cdk.StackProps {
  config: ConfigProps;
  vpc: ec2.Vpc;
}

export class RdsStack extends cdk.Stack {
  public readonly rdsSecret: string;
  public readonly rdsHost: string;

  constructor(scope: Construct, id: string, props: RdsStackProps) {
    super(scope, id, props);

    // Security group for RDS
    const dbSecurityGroup = new ec2.SecurityGroup(this, 'DatabaseSecurityGroup', {
      vpc: props.vpc,
      description: 'Security group for Aurora PostgreSQL database',
      allowAllOutbound: true,
    });

    dbSecurityGroup.addIngressRule(ec2.Peer.ipv4(props.vpc.vpcCidrBlock), ec2.Port.tcp(5432), 'Allow Postgres access');

    // Create Aurora PostgreSQL database cluster
    const cluster = new rds.DatabaseCluster(this, 'AuroraCluster', {
      engine: rds.DatabaseClusterEngine.auroraPostgres({
        version: rds.AuroraPostgresEngineVersion.VER_13_15,
      }),
      instances: 2,
      instanceProps: {
        vpc: props.vpc,
        vpcSubnets: {
          subnetType: ec2.SubnetType.PRIVATE_ISOLATED,
        },
        securityGroups: [dbSecurityGroup],
        instanceType: ec2.InstanceType.of(ec2.InstanceClass.BURSTABLE3, ec2.InstanceSize.MEDIUM),
      },
      credentials: rds.Credentials.fromGeneratedSecret('dbadmin'),
      defaultDatabaseName: 'MyDatabase',
      removalPolicy: cdk.RemovalPolicy.DESTROY, // Destroy cluster when stack is deleted (useful for development)
    });

    this.rdsHost = cluster.clusterEndpoint.hostname;
  }
}
