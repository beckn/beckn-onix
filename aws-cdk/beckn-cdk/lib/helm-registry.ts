import * as cdk from 'aws-cdk-lib';
import * as eks from 'aws-cdk-lib/aws-eks';
import * as helm from 'aws-cdk-lib/aws-eks';
import { Stack, StackProps } from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { ConfigProps } from './config';

  interface HelmRegistryStackProps extends StackProps {
    config: ConfigProps;
    eksCluster: eks.Cluster;
    rdsHost: string;
    rdsPassword: string;
}

export class HelmRegistryStack extends Stack {
  constructor(scope: Construct, id: string, props: HelmRegistryStackProps) {
    super(scope, id, props);

    const eksCluster = props.eksCluster;
    const externalDomain = props.config.REGISTRY_EXTERNAL_DOMAIN;
    const certArn = props.config.CERT_ARN;
    const releaseName = props.config.REGISTRY_RELEASE_NAME;
    const repository = props.config.REPOSITORY;

    const rdsHost = props.rdsHost;
    const rdsPassword = props.rdsPassword;

    new helm.HelmChart(this, "registryhelm", {
        cluster: eksCluster,
        chart: "beckn-onix-registry",
        release: releaseName,
        wait: false,
        repository: repository,
        values: {
                externalDomain: externalDomain,
                database: {
                    host: rdsHost,
                    password: rdsPassword
                },
                ingress: {
                    tls: 
                    {
                        certificateArn: certArn,
                    },
                },
        }

    });
  }
}
