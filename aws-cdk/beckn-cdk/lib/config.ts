import * as dotenv from "dotenv";
import path = require("path");

dotenv.config({ path: path.resolve(__dirname, "../.env") });

export type ConfigProps = {
    REGION: string,
    ACCOUNT: string,
    REPOSITORY: string,
    REGISTRY_RELEASE_NAME: string;
    GATEWAY_RELEASE_NAME: string;
    BAP_RELEASE_NAME: string;
    BPP_RELEASE_NAME: string,
    RDS_USER: string,
    CERT_ARN: string,
    REGISTRY_URL: string,
    MAX_AZS: number,
    EKS_CLUSTER_NAME: string,
    CIDR: string,
    EC2_NODES_COUNT: number;
    EC2_INSTANCE_TYPE: string;
    ROLE_ARN: string;
    DOCDB_PASSWORD: string;
    RABBITMQ_PASSWORD: string;
    NAMESPACE: string;
    BAP_PUBLIC_KEY: string;
    BAP_PRIVATE_KEY: string;
    BPP_PUBLIC_KEY: string;
    BPP_PRIVATE_KEY: string;
    REGISTRY_EXTERNAL_DOMAIN: string,
    GATEWAY_EXTERNAL_DOMAIN: string;
    BAP_EXTERNAL_DOMAIN: string;
    BPP_EXTERNAL_DOMAIN: string;
    
};

export const getConfig = (): ConfigProps => ({
    REGION: process.env.REGION || "ap-south-1",
    ACCOUNT: process.env.ACCOUNT || "",
    REPOSITORY: process.env.BECKN_ONIX_HELM_REPOSITORY || "",
    MAX_AZS: Number(process.env.MAZ_AZs) || 2,
    REGISTRY_RELEASE_NAME: "beckn-onix-registry",
    GATEWAY_RELEASE_NAME: "beckn-onix-gateway",
    BAP_RELEASE_NAME: "beckn-onix-bap",
    BPP_RELEASE_NAME: "beckn-onix-bpp",
    RDS_USER: process.env.RDS_USER || "postgres",
    CERT_ARN: process.env.CERT_ARN || "", // user must provide it
    REGISTRY_URL: process.env.REGISTRY_URL || "", // beckn-onix reg url
    EKS_CLUSTER_NAME: process.env.EKS_CLUSTER_NAME || "beckn-onix",
    CIDR: process.env.CIDR || "10.20.0.0/16",
    EC2_NODES_COUNT: Number(process.env.EC2_NODES_COUNT) || 2,
    EC2_INSTANCE_TYPE: process.env.EC2_INSTANCE_TYPE || "t3.large",
    ROLE_ARN: process.env.ROLE_ARN || "",
    DOCDB_PASSWORD: process.env.DOCDB_PASSWORD || "",
    RABBITMQ_PASSWORD: process.env.RABBITMQ_PASSWORD || "",
    NAMESPACE: "-common-services",
    BAP_PUBLIC_KEY: process.env.BAP_PUBLIC_KEY || "",
    BAP_PRIVATE_KEY: process.env.BAP_PRIVATE_KEY || "",
    BPP_PUBLIC_KEY: process.env.BPP_PUBLIC_KEY || "",
    BPP_PRIVATE_KEY: process.env.BPP_PRIVATE_KEY || "",
    REGISTRY_EXTERNAL_DOMAIN: process.env.REGISTRY_EXTERNAL_DOMAIN || "", // user must provide it
    GATEWAY_EXTERNAL_DOMAIN: process.env.GATEWAY_EXTERNAL_DOMAIN || "", // user must provide it
    BAP_EXTERNAL_DOMAIN: process.env.BAP_EXTERNAL_DOMAIN || "", // user must provide it
    BPP_EXTERNAL_DOMAIN: process.env.BPP_EXTERNAL_DOMAIN || "", // user must provide it
});