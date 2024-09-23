import * as cdk from 'aws-cdk-lib';
import * as eks from 'aws-cdk-lib/aws-eks';
import * as helm from 'aws-cdk-lib/aws-eks';
import { Stack, StackProps } from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { ConfigProps } from './config';
import * as efs from 'aws-cdk-lib/aws-efs';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as iam from 'aws-cdk-lib/aws-iam';


interface HelmBapStackProps extends StackProps {
  config: ConfigProps;
  eksCluster: eks.Cluster;
  isSandbox: boolean;
  eksSecGrp: ec2.SecurityGroup;
  vpc: ec2.Vpc;
}

export class HelmBapStack extends Stack {
  constructor(scope: Construct, id: string, props: HelmBapStackProps) {
    super(scope, id, props);

    const eksCluster = props.eksCluster;
    const externalDomain = props.config.BAP_EXTERNAL_DOMAIN;
    const certArn = props.config.CERT_ARN;
    const releaseName = props.config.BAP_RELEASE_NAME;
    const repository = props.config.REPOSITORY;
    const registryUrl = props.config.REGISTRY_URL;
    const bapPrivateKey = props.config.BAP_PRIVATE_KEY;
    const bapPublicKey = props.config.BAP_PUBLIC_KEY;

    const isSandbox = props.isSandbox;

    const myFileSystemPolicy = new iam.PolicyDocument({
      statements: [new iam.PolicyStatement({
        actions: [
          'elasticfilesystem:ClientRootAccess',
          'elasticfilesystem:ClientWrite',
          'elasticfilesystem:ClientMount',
        ],
        principals: [new iam.ArnPrincipal('*')],
        resources: ['*'],
        conditions: {
          Bool: {
            'elasticfilesystem:AccessedViaMountTarget': 'true',
          },
        },
      })],
    });

    const efsBapFileSystemId = new efs.FileSystem(this, 'Beckn-Onix-Bap', {
      vpc: props.vpc,
      securityGroup: props.eksSecGrp,
      fileSystemPolicy: myFileSystemPolicy,
    });

    // let efsBapFileSystemId: string | undefined;
    // const existingFileSystemId = cdk.Fn.importValue('EfsBapFileSystemId');

    // if(existingFileSystemId){
    //   efsBapFileSystemId = existingFileSystemId;
    // } else{
    //   const efsBapFileSystem = new efs.FileSystem(this, 'Beckn-Onix-Bap', {
    //     vpc: props.vpc,
    //     securityGroup: props.eksSecGrp,
    //   });

    //   efsBapFileSystemId = efsBapFileSystem.fileSystemId;

    //   new cdk.CfnOutput(this, 'EfsBapFileSystemId', {
    //     value: efsBapFileSystemId,
    //     exportName: 'EfsBapFileSystemId',
    //   })
    // }

    // const efsBapFileSystemId = new efs.FileSystem(this, 'Beckn-Onix-Bap', {
    //   vpc: props.vpc,
    // });
    
    new helm.HelmChart(this, 'baphelm', {
      cluster: eksCluster,
      chart: 'beckn-onix-bap',
      release: releaseName,
      wait: false,
      repository: repository,
      values: {
        global: {
          isSandbox: isSandbox,
          externalDomain: externalDomain,
          registry_url: registryUrl,
          bap: {
            privateKey: bapPrivateKey,
            publicKey: bapPublicKey,
          },
          efs: {
            fileSystemId: efsBapFileSystemId.fileSystemId,
          },
          ingress: {
            tls: {
              certificateArn: certArn,
          },
        },
      },
    },
  }
);
    
    new cdk.CfnOutput(this, String("EksFileSystemId"), {
        value: efsBapFileSystemId.fileSystemId,
    });
  }
}
