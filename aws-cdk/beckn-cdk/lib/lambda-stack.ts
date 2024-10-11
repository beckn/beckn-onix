import * as cdk from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as cr from 'aws-cdk-lib/custom-resources';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as path from 'path';
import { Construct } from 'constructs';

export interface LambdaStackProps extends cdk.StackProps {
  vpc: ec2.Vpc;
  rdsHost: string;
  rdsUser: string;
  rdsPassword: string;
}

export class LambdaStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LambdaStackProps) {
    super(scope, id, props);

    const dbCreationLambda = new lambda.Function(this, 'DbCreationFunction', {
      runtime: lambda.Runtime.NODEJS_LATEST,
      code: lambda.Code.fromAsset(path.join(__dirname, 'lambda')), // Path to your Lambda function code
      handler: 'index.handler',
      environment: {
        DB_CONFIG: JSON.stringify({
          host: props.rdsHost,
          user: props.rdsUser,
          password: props.rdsPassword,
        }),
      },
      vpc: props.vpc,
      securityGroups: [/* your security group here */],
      vpcSubnets: {
        subnetType: ec2.SubnetType.PRIVATE_ISOLATED,
      },
    });

    dbCreationLambda.addToRolePolicy(new iam.PolicyStatement({
        actions: ['rds-data:ExecuteStatement'],
        resources: [`arn:aws:rds:${this.region}:${this.account}:cluster:your-cluster-name`], // Update with your RDS cluster ARN
    }));

    new cr.AwsCustomResource(this, 'InvokeDbCreation', {
        onCreate: {
          service: 'Lambda',
          action: 'invoke',
          parameters: {
            FunctionName: dbCreationLambda.functionArn,
            InvocationType: 'Event', // Use 'RequestResponse' if you want to wait for it to finish
          },
          physicalResourceId: cr.PhysicalResourceId.of(Date.now().toString()), // Unique ID to force recreation if needed
        },
        policy: cr.AwsCustomResourcePolicy.fromStatements([
          new iam.PolicyStatement({
            actions: ['lambda:InvokeFunction'],
            resources: [dbCreationLambda.functionArn],
          }),
        ]),
      });

    // Grant the Lambda function permissions to read the RDS password
    // props.rdsPassword.grantRead(dbCreationLambda);
  }
}
