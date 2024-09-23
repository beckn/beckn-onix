import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as rds from 'aws-cdk-lib/aws-rds';
import { Construct } from 'constructs';
import { ConfigProps } from './config';
import cluster from 'cluster';
import { Secret } from 'aws-cdk-lib/aws-secretsmanager';

export interface RdsStackProps extends cdk.StackProps {
  config: ConfigProps;
  envC: string;
  vpc: ec2.Vpc;
}

export class RdsStack extends cdk.Stack {
  public readonly rdsSecret: string;
  public readonly rdsHost: string;
  public readonly rdsPassword: string;

  constructor(scope: Construct, id: string, props: RdsStackProps) {
    super(scope, id, props);

    const vpc = props.vpc;
    const dbName = props.envC;
    const rdsUser = props.config.RDS_USER; // take input from user / make it 
    const rdsPassword = this.createPassword();
    const rdsSecGrpIngress = props.config.CIDR;
    
    const securityGroupRDS = new ec2.SecurityGroup(this, 'RdsSecurityGroup', {
      vpc: vpc,
      allowAllOutbound: true,
      description: 'Security group for Aurora PostgreSQL database',
    });

    securityGroupRDS.addIngressRule(
      ec2.Peer.ipv4(rdsSecGrpIngress),
      ec2.Port.tcp(5432),
      "Allow Postgress Access"
    );

    const creds = new Secret(this, "rdsSecret", {
      secretObjectValue: {
        username: cdk.SecretValue.unsafePlainText(rdsUser.toString()),
        password: cdk.SecretValue.unsafePlainText(rdsPassword.toString()),
      },
    });

    const cluster = new rds.DatabaseCluster(this, 'AuroraCluster', {
      engine: rds.DatabaseClusterEngine.auroraPostgres({
        version: rds.AuroraPostgresEngineVersion.VER_14_6,
      }),
      credentials: rds.Credentials.fromSecret(creds),
      instances: 1,
      instanceProps: {
        vpc: props.vpc,
        vpcSubnets: {
          subnetType: ec2.SubnetType.PRIVATE_ISOLATED,
        },
        securityGroups: [securityGroupRDS],
        instanceType: ec2.InstanceType.of(ec2.InstanceClass.BURSTABLE3, ec2.InstanceSize.MEDIUM),
      },
      defaultDatabaseName: dbName,
    });

    this.rdsSecret = creds.secretArn;
    this.rdsHost = cluster.clusterEndpoint.hostname;
    this.rdsPassword = rdsPassword;

    new cdk.CfnOutput(this, 'RDSPasswordOutput', {
      value: rdsPassword,
      exportName: `RDSPassword-${dbName}`,
    })
  }

  //generate password function
  private createPassword(length: number = 12): string {
    const characters = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&()*+,-.:;<=>?[]^_`{|}~';
    let password = '';
    for (let i = 0; i < length; i++) {
      password += characters.charAt(Math.floor(Math.random() * characters.length));
    }
    return password;
  }
}
