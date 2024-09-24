### Verifying Deployed Beckn-ONIX Services in Amazon EKS

Once the Helm charts are successfully deployed (manually or through AWS CDK), you can verify that the services (Registry, Gateway, Redis, MongoDB, RabbitMQ, BAP and BPP) are running correctly in your Amazon EKS cluster by using the following commands.

**Configure [Kubectl client](https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html) with Amazon EKS Cluster**

#### 1. Verify Namespaces
Run the following command to check `namespaces`. 

**Note:** This output is from the Sandbox environment, so you will see that all services are deployed. However, you may observe namespaces in your environment based on the specific Beckn-ONIX service you are deploying.

```bash
$ kubectl get namespaces
NAME                  STATUS   AGE
bap-common-services   Active   5d21h
beckn-onix-bap        Active   5d21h
beckn-onix-bpp        Active   4d20h
beckn-onix-gateway    Active   6d19h
beckn-onix-registry   Active   6d20h
bpp-common-services   Active   4d21h
```

#### 2. Verify Pods Status

Run the following command to check the status of all pods in the `namespace` where the services are deployed:

```bash
$ kubectl -n beckn-onix-registry get pod
NAME                                   READY   STATUS    RESTARTS   AGE
beckn-onix-registry-5f96f7b755-49nz6   1/1     Running   0          2d1h
```

```bash
$ kubectl -n beckn-onix-gateway get pod
NAME                                  READY   STATUS    RESTARTS   AGE
beckn-onix-gateway-574d67df98-qbvtb   1/1     Running   0          2d1h
```

```bash
$ kubectl -n bap-common-services get pod
NAME                       READY   STATUS    RESTARTS   AGE
mongodb-597955cb85-kctrd   1/1     Running   0          5d21h
rabbitmq-0                 1/1     Running   0          2d1h
redis-master-0             1/1     Running   0          5d21h
```

```bash
$ kubectl -n bpp-common-services get pod
NAME                       READY   STATUS    RESTARTS   AGE
mongodb-597955cb85-nqs4r   1/1     Running   0          4d21h
rabbitmq-0                 1/1     Running   0          2d1h
redis-master-0             1/1     Running   0          2d1h
```

```bash
$ kubectl -n beckn-onix-bap get pod
NAME                          READY   STATUS    RESTARTS   AGE
bap-client-84c5d6b6fd-cb9qr   1/1     Running   0          2d1h
bap-network-d875cdb9c-btjcl   1/1     Running   0          2d1h
```

```bash
$ kubectl -n beckn-onix-bpp get pod
NAME                           READY   STATUS    RESTARTS   AGE
bpp-client-59f976cb94-4cmwh    1/1     Running   0          2d1h
bpp-network-5f88bb75d9-jc7g4   1/1     Running   0          2d1h
```

#### 3. Verify Ingress and Kubernetes Service
The Ingress resource provisions an Amazon Application Load Balancer (ALB) that routes external traffic to the appropriate Kubernetes service, which then directs the traffic to the underlying service pods.

```bash
$ kubectl -n beckn-onix-registry get ingress,svc
NAME                                                    CLASS   HOSTS   ADDRESS                                                       PORTS   AGE
ingress.networking.k8s.io/beckn-onix-registry-ingress   alb     *       beckn-onix-registry-1902090994.ap-south-1.elb.amazonaws.com   80      6d20h

NAME                              TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/beckn-onix-registry-svc   ClusterIP   10.100.55.190   <none>        3030/TCP   6d20h
```

```bash
$ kubectl -n beckn-onix-gateway get ingress,svc
NAME                                                   CLASS   HOSTS   ADDRESS                                                      PORTS   AGE
ingress.networking.k8s.io/beckn-onix-gateway-ingress   alb     *       beckn-onix-gateway-1452877031.ap-south-1.elb.amazonaws.com   80      6d19h

NAME                             TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/beckn-onix-gateway-svc   ClusterIP   10.100.44.118   <none>        4030/TCP   6d19h
```

```bash
$ kubectl -n beckn-onix-bap get ingress,svc
NAME                                            CLASS   HOSTS   ADDRESS                                                          PORTS   AGE
ingress.networking.k8s.io/bap-network-ingress   alb     *       beckn-onix-bap-network-1610405288.ap-south-1.elb.amazonaws.com   80      5d20h

NAME                      TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/bap-network-svc   ClusterIP   10.100.36.244   <none>        5001/TCP   5d21h
```

```bash
$ kubectl -n beckn-onix-bpp get ingress,svc
NAME                                            CLASS   HOSTS   ADDRESS                                                         PORTS   AGE
ingress.networking.k8s.io/bpp-network-ingress   alb     *       beckn-onix-bpp-network-736891093.ap-south-1.elb.amazonaws.com   80      4d21h

NAME                      TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/bpp-network-svc   ClusterIP   10.100.130.43   <none>        6001/TCP   4d21h
```

## Next Steps

After verifying that all Beckn-Onix services have been deployed successfully, proceed with the next steps to complete the setup:

1. **[Update DNS Records](post-deployment-dns-config.md)**

   To configure DNS settings for your services, follow the instructions provided in the [Post-Deployment DNS Configuration](post-deployment-dns-config.md) document. This will guide you through retrieving the necessary Load Balancer addresses and updating your DNS records.

Make sure to follow the detailed steps in the linked document to ensure that your DNS records are correctly configured for proper service routing.
