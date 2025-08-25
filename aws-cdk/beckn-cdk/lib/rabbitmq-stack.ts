import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as amazonmq from 'aws-cdk-lib/aws-amazonmq';
import * as dotenv from 'dotenv';
import { ConfigProps } from './config';

// Load environment variables from .env file
dotenv.config();

interface RabbitMqStackProps extends cdk.StackProps {
  config: ConfigProps;
  vpc: ec2.Vpc;
}

export class RabbitMqStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: RabbitMqStackProps) {
    super(scope, id, props);

    // Prompt for the RabbitMQ admin password using environment variable
    const rabbitMqPassword = new cdk.CfnParameter(this, 'RabbitMqPassword', {
      type: 'String',
      description: 'The password for the RabbitMQ broker admin user',
      noEcho: true,  // Ensure the password is hidden from the console
      default: props.config.RABBITMQ_PASSWORD || '',  // Use the password from .env or set a fallback
    });

    // Security group for RabbitMQ
    const rabbitMqSecurityGroup = new ec2.SecurityGroup(this, 'RabbitMqSecurityGroup', {
      vpc: props.vpc,
      description: 'Security group for RabbitMQ broker',
      allowAllOutbound: true,
    });

    rabbitMqSecurityGroup.addIngressRule(ec2.Peer.ipv4(props.vpc.vpcCidrBlock), ec2.Port.tcp(5672), 'Allow RabbitMQ traffic on port 5672');
    rabbitMqSecurityGroup.addIngressRule(ec2.Peer.ipv4(props.vpc.vpcCidrBlock), ec2.Port.tcp(15672), 'Allow RabbitMQ management traffic');

    // Select a single private subnet for the RabbitMQ Broker
    const privateSubnets = props.vpc.selectSubnets({ subnetType: ec2.SubnetType.PRIVATE_WITH_NAT }).subnets;

    // Ensure there's at least one subnet, and use the first one
    if (privateSubnets.length === 0) {
      throw new Error('No private subnets found in the VPC');
    }

    const selectedSubnet = privateSubnets[0];  // Use the first subnet

    // RabbitMQ Broker
    new amazonmq.CfnBroker(this, 'RabbitMqBroker', {
      brokerName: 'MyRabbitMqBroker',
      engineType: 'RABBITMQ',
      engineVersion: '3.10.25',
      deploymentMode: 'SINGLE_INSTANCE',
      publiclyAccessible: false,
      hostInstanceType: 'mq.m5.large',  // Adjust the instance type as needed
      subnetIds: [selectedSubnet.subnetId],  // Pass a single subnet
      securityGroups: [rabbitMqSecurityGroup.securityGroupId],
      users: [
        {
          username: 'becknadmin',  // Fixed username
          password: rabbitMqPassword.valueAsString,  // Password entered by the user or set from the .env file
        },
      ],
    });
  }
}
