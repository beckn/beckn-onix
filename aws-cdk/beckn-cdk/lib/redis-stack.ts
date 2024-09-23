import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as elasticache from 'aws-cdk-lib/aws-elasticache';

interface RedisStackProps extends cdk.StackProps {
  vpc: ec2.Vpc;
}

export class RedisStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: RedisStackProps) {
    super(scope, id, props);

    // Security group for ElastiCache
    const elasticacheSecurityGroup = new ec2.SecurityGroup(this, 'ElastiCacheSecurityGroup', {
      vpc: props.vpc,
      description: 'Security group for Redis',
      allowAllOutbound: true,
    });

    elasticacheSecurityGroup.addIngressRule(ec2.Peer.ipv4(props.vpc.vpcCidrBlock), ec2.Port.tcp(6379), 'Allow Redis traffic');

    // Redis subnet group
    const redisSubnetGroup = new elasticache.CfnSubnetGroup(this, 'RedisSubnetGroup', {
      description: 'Subnet group for Redis cluster',
      subnetIds: props.vpc.selectSubnets({ subnetType: ec2.SubnetType.PRIVATE_WITH_NAT }).subnetIds,
    });

    // Redis Cluster
    new elasticache.CfnCacheCluster(this, 'RedisCluster', {
      cacheNodeType: 'cache.t3.medium',  // Adjust the node type based on your needs
      engine: 'redis',
      numCacheNodes: 1,
      vpcSecurityGroupIds: [elasticacheSecurityGroup.securityGroupId],
      cacheSubnetGroupName: redisSubnetGroup.ref,
    });
  }
}
