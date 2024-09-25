import * as cdk from 'aws-cdk-lib';
import * as eks from 'aws-cdk-lib/aws-eks';
import * as helm from 'aws-cdk-lib/aws-eks';
import { Stack, StackProps } from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { ConfigProps } from './config';

interface HelmGAtewayStackProps extends cdk.StackProps {
    config: ConfigProps;
    eksCluster: eks.Cluster;
    rdsHost: string;
    rdsPassword: string;
  }

export class HelmGatewayStack extends Stack {
  constructor(scope: Construct, id: string, props: HelmGAtewayStackProps) {
    super(scope, id, props);

    const eksCluster = props.eksCluster;
    const externalDomain = props.config.GATEWAY_EXTERNAL_DOMAIN;
    const certArn = props.config.CERT_ARN;
    const registryUrl = props.config.REGISTRY_URL;

    const releaseName = props.config.GATEWAY_RELEASE_NAME;
    const repository = props.config.REPOSITORY;

    const rdsHost = props.rdsHost;
    const rdsPassword = props.rdsPassword;

    new helm.HelmChart(this, "gatewayhelm", {
        cluster: eksCluster,
        chart: "beckn-onix-gateway",
        release: releaseName,
        wait: false,
        repository: repository,
        values: {
            externalDomain: externalDomain,
            registry_url: registryUrl,
            database: {
                host: rdsHost,
                password: rdsPassword,
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
