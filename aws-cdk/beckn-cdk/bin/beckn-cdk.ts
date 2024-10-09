#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { StackProps } from 'aws-cdk-lib';
import { ConfigProps, getConfig } from '../lib/config';

import { VpcStack } from '../lib/vpc-stack';
import { RdsStack } from '../lib/rds-stack';
import { EksStack } from '../lib/eks-stack';
import { RedisStack } from '../lib/redis-stack';
import { DocumentDbStack } from '../lib/documentdb-stack';
import { RabbitMqStack } from '../lib/rabbitmq-stack';

import { HelmRegistryStack } from '../lib/helm-registry';
import { HelmGatewayStack } from '../lib/helm-gateway';
import { HelmCommonServicesStack } from '../lib/helm-beckn-common-services';
import { HelmBapStack } from '../lib/helm-bap';
import { HelmBppStack } from '../lib/helm-bpp';
import { LambdaStack } from '../lib/lambda-stack';


const config = getConfig();
const app = new cdk.App();

type AwsEnvStackProps = StackProps & {
  config: ConfigProps;
};

// Retrieve AWS Account ID and Region from the environment
const accountId = config.ACCOUNT;
const region = config.REGION;

if (!accountId || !region) {
  console.error("AWS_ACCOUNT_ID or AWS_REGION is missing from .env file");
  process.exit(1);
}

// Common environment configuration for all stacks
const env = { account: accountId, region: region };

// Function to deploy registry environment
const deployRegistry = () => {
  var envC = "registry";
  const vpcStack = new VpcStack(app, 'RegistryVpcStack', { config: config, env });
  const eksStack = new EksStack(app, 'RegistryEksStack', { config: config, vpc: vpcStack.vpc, env });
  const rdsStack = new RdsStack(app, 'RegistryRdsStack', { config: config, vpc: vpcStack.vpc, envC: envC, env });

  new HelmRegistryStack(app, 'HelmRegistryStack', {
    config: config,
    rdsHost: rdsStack.rdsHost,
    rdsPassword: rdsStack.rdsPassword,
    eksCluster: eksStack.cluster,
    env,
  });
};

// Function to deploy gateway environment
const deployGateway = () => {
  var envC = "gateway";
  const vpcStack = new VpcStack(app, 'GatewayVpcStack', { config: config, env });
  const eksStack = new EksStack(app, 'GatewayEksStack', { config: config, vpc: vpcStack.vpc, env });
  const rdsStack = new RdsStack(app, 'GatewayRdsStack', { config: config, vpc: vpcStack.vpc, envC: envC, env });

  new HelmGatewayStack(app, 'HelmGatewayStack', {
    config: config,
    rdsHost: rdsStack.rdsHost,
    rdsPassword: rdsStack.rdsPassword,
    eksCluster: eksStack.cluster,
    env,
  });
  
};

// Function to deploy BAP environment
const deployBAP = () => {
  const vpcStack = new VpcStack(app, 'BapVpcStack', { config: config, env });
  const eksStack = new EksStack(app, 'BapEksStack', {config: config, vpc: vpcStack.vpc, env });

  // if(service === 'managed'){
  //   new DocumentDbStack(app, 'DocumentDbStack', { config: config, vpc: vpcStack.vpc, env });
  //   new RedisStack(app, 'RedisStack', { vpc: vpcStack.vpc, env });
  //   new RabbitMqStack(app, 'RabbitMqStack', { config: config, vpc: vpcStack.vpc, env });
  // }else {

  // bitnami - common services on eks - self hosted
  new HelmCommonServicesStack(app, 'HelmBapCommonServicesStack', {
    config: config,
    eksCluster: eksStack.cluster,
    service: 'bap',
    env,
  });

  // }

  new HelmBapStack(app, 'HelmBapStack', {
    config: config,
    eksCluster: eksStack.cluster,
    vpc: vpcStack.vpc,
    eksSecGrp: eksStack.eksSecGrp,
    isSandbox: false,
    env,
  });

  return vpcStack;
};

// Function to deploy BPP environment
const deployBPP = () => {
  const vpcStack = new VpcStack(app, 'BppVpcStack', {config: config, env });
  const eksStack = new EksStack(app, 'BppEksStack', {config: config, vpc: vpcStack.vpc, env });

  // if(service === 'managed'){
  //   new DocumentDbStack(app, 'DocumentDbStack', { config: config, vpc: vpcStack.vpc, env });
  //   new RedisStack(app, 'RedisStack', { vpc: vpcStack.vpc, env });
  //   new RabbitMqStack(app, 'RabbitMqStack', { config: config, vpc: vpcStack.vpc, env });
  // }else {

  // if bitnami
  new HelmCommonServicesStack(app, 'HelmBapCommonServicesStack', {
    config: config,
    eksCluster: eksStack.cluster,
    service: 'bpp',
    env,
  });
  
  // }

  new HelmBppStack(app, 'HelmBppStack', {
    config: config,
    eksCluster: eksStack.cluster,
    vpc: vpcStack.vpc,
    eksSecGrp: eksStack.eksSecGrp,
    isSandbox: false,
    env,
  });

  return vpcStack;
};

// Function to deploy sandbox environment (all stacks)
const deploySandbox = () => {
  var envC = "sandbox";
  const vpcStack = new VpcStack(app, 'VpcStack', {config: config, env });
  const eksStack = new EksStack(app, 'EksStack', {config: config, vpc: vpcStack.vpc, env });
  const rdsStack = new RdsStack(app, 'RdsStack', { config: config, vpc: vpcStack.vpc, envC: envC, env });

  new LambdaStack(app, 'LambdaStack', {
    rdsHost: rdsStack.rdsHost,
    rdsUser: config.RDS_USER,
    rdsPassword: rdsStack.rdsPassword,
    vpc: vpcStack.vpc, env 
  });
  
  new HelmRegistryStack(app, 'HelmRegistryStack', {
    config: config,
    rdsHost: rdsStack.rdsHost,
    rdsPassword: rdsStack.rdsPassword,
    eksCluster: eksStack.cluster,
    env,
  });

  new HelmGatewayStack(app, 'HelmGatewayStack', {
    config: config,
    rdsHost: rdsStack.rdsHost,
    rdsPassword: rdsStack.rdsPassword,
    eksCluster: eksStack.cluster,
    env,
  });
  
  // if(service === 'managed'){
  //   new DocumentDbStack(app, 'DocumentDbStack', { config: config, vpc: vpcStack.vpc, env });
  //   new RedisStack(app, 'RedisStack', { vpc: vpcStack.vpc, env });
  //   new RabbitMqStack(app, 'RabbitMqStack', { config: config, vpc: vpcStack.vpc, env });
  // } else {

  // default - bitnami
  new HelmCommonServicesStack(app, 'BapHelmCommonServicesStack', {
    config: config,
    eksCluster: eksStack.cluster,
    service: 'bap',
    env,
  });

  new HelmCommonServicesStack(app, 'BppHelmCommonServicesStack', {
    config: config,
    eksCluster: eksStack.cluster,
    service: 'bpp',
    env,
  });

  // }

  new HelmBapStack(app, 'HelmBapStack', {
    config: config,
    eksCluster: eksStack.cluster,
    vpc: vpcStack.vpc,
    eksSecGrp: eksStack.eksSecGrp,
    isSandbox: true,
    env,
  });

  new HelmBppStack(app, 'HelmBppStack', {
    config: config,
    eksCluster: eksStack.cluster,
    vpc: vpcStack.vpc,
    eksSecGrp: eksStack.eksSecGrp,
    isSandbox: true,
    env,
  });

  return vpcStack;
};

const deployManagedStack = (vpcStack: VpcStack) => {
  new DocumentDbStack(app, 'DocumentDbStack', { config: config, vpc: vpcStack.vpc, env });
  new RedisStack(app, 'RedisStack', { config: config, vpc: vpcStack.vpc, env });
  new RabbitMqStack(app, 'RabbitMqStack', { config: config, vpc: vpcStack.vpc, env });
}

// Retrieve the environment from CDK context
const environment = app.node.tryGetContext('env');
// const service = app.node.tryGetContext('service') || 'common'; // can be common or managed
const bap_bpp_managed = app.node.tryGetContext('bap_bpp_managed') || 'common';

// Deploy based on the selected environment
switch (environment) {
  case 'sandbox':
    console.log('Deploying sandbox environment...');
    const vpcStack_sandbox = deploySandbox();
    if(bap_bpp_managed === 'managed') deployManagedStack(vpcStack_sandbox);
    break;
  case 'registry':
    console.log('Deploying registry environment...');
    deployRegistry();
    break;
  case 'gateway':
    console.log('Deploying gateway environment...');
    deployGateway();
    break;
  case 'bap':
    console.log('Deploying BAP environment...');
    const vpcStack_bap = deployBAP();
    if(bap_bpp_managed === 'managed') deployManagedStack(vpcStack_bap);
    break;
  case 'bpp':
    console.log('Deploying BPP environment...');
    const vpcStack_bpp = deployBPP();
    if(bap_bpp_managed === 'managed') deployManagedStack(vpcStack_bpp);
    break;
  default:
    console.error('Unknown environment specified.');
    process.exit(1);
}
